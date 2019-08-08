/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/autoscaling/autoscalingiface"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"

	"github.com/go-logr/logr"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	upgrademgrv1alpha1 "github.com/orkaproj/upgrade-manager/api/v1alpha1"
)

const (
	// StatusRunning marks the CR to be running.
	StatusRunning = "running"
	// StatusComplete marks the CR as completed.
	StatusComplete = "completed"
	// StatusError marks the CR as errored out.
	StatusError = "error"
	// JanitorAnnotation is for completed objects.
	JanitorAnnotation = "janitor/ttl"
	// ClearCompletedFrequency is the time after which a completed rollingUpgrade object is deleted.
	ClearCompletedFrequency = "1d"
	// ClearErrorFrequency is the time after which an errored rollingUpgrade object is deleted.
	ClearErrorFrequency = "7d"

	// Environment variable keys
	asgNameKey      = "ASG_NAME"
	instanceIDKey   = "INSTANCE_ID"
	instanceNameKey = "INSTANCE_NAME"

	// KubeCtlBinary is the path to the kubectl executable
	KubeCtlBinary = "/usr/local/bin/kubectl"
	// ShellBinary is the path to the shell executable
	ShellBinary = "/bin/sh"
)

// RollingUpgradeReconciler reconciles a RollingUpgrade object
type RollingUpgradeReconciler struct {
	client.Client
	Log             logr.Logger
	generatedClient *kubernetes.Clientset
	Asg             *autoscaling.Group
	NodeList        *corev1.NodeList
	admissionMap    sync.Map
	ruObjNameToASG  sync.Map
}

func runScript(script string, background bool, objName string) (string, error) {
	log.Printf("%s: Running script %s", objName, script)

	if background {
		log.Printf("%s: Running script in background. Logs not available.", objName)
		exec.Command(ShellBinary, "-c", script).Run()
		return "", nil
	}

	out, err := exec.Command("/bin/sh", "-c", script).CombinedOutput()
	if err != nil {
		log.Printf("Script finished with output: %s\n,  error: %s", out, err)
	} else {
		log.Printf("%s: Script finished with output: %s", objName, out)
	}
	return string(out), err
}

func (r *RollingUpgradeReconciler) preDrainHelper(ruObj *upgrademgrv1alpha1.RollingUpgrade) error {
	if ruObj.Spec.PreDrain.Script != "" {
		script := ruObj.Spec.PreDrain.Script
		_, err := runScript(script, false, ruObj.Name)
		if err != nil {
			msg := "Failed to run preDrain script: " + err.Error()
			log.Printf("%s: %s", ruObj.Name, msg)
			return errors.New(msg)
		}
	}
	return nil
}

// Operates on any scripts that were provided after the draining of the node.
// kubeCtlCall is provided as an argument to decouple the method from the actual kubectl call
func (r *RollingUpgradeReconciler) postDrainHelper(ruObj *upgrademgrv1alpha1.RollingUpgrade, nodeName string, kubeCtlCall string) error {
	if ruObj.Spec.PostDrain.Script != "" {
		_, err := runScript(ruObj.Spec.PostDrain.Script, false, ruObj.Name)
		if err != nil {
			msg := "Failed to run postDrain script: " + err.Error()
			log.Printf("%s: %s", ruObj.Name, msg)

			log.Printf("%s: Uncordoning the node %s since it failed to run postDrain Script", ruObj.Name, nodeName)
			runScript(kubeCtlCall+" uncordon "+nodeName, false, ruObj.Name)
			return errors.New(msg)
		}
	}
	log.Printf("%s: Waiting for postDrainDelay", ruObj.Name)
	time.Sleep(time.Duration(ruObj.Spec.PostDrainDelaySeconds) * time.Second)

	if ruObj.Spec.PostDrain.PostWaitScript != "" {
		_, err := runScript(ruObj.Spec.PostDrain.PostWaitScript, false, ruObj.Name)
		if err != nil {
			msg := "Failed to run postDrainWait script: " + err.Error()
			log.Printf("%s: %s", ruObj.Name, msg)

			log.Printf("%s: Uncordoning the node %s since it failed to run postDrainWait Script", ruObj.Name, nodeName)
			runScript(kubeCtlCall+" uncordon "+nodeName, false, ruObj.Name)
			return errors.New(msg)
		}
	}
	return nil
}

// DrainNode runs "kubectl drain" on the given node
// kubeCtlCall is provided as an argument to decouple the method from the actual kubectl call
func (r *RollingUpgradeReconciler) DrainNode(ruObj *upgrademgrv1alpha1.RollingUpgrade, nodeName string, kubeCtlCall string) error {
	// Running kubectl drain node.
	err := r.preDrainHelper(ruObj)
	if err != nil {
		return errors.New(ruObj.Name + ": Predrain script failed: " + err.Error())
	}

	// kops behavior implements the same behavior by using these flags when draining nodes
	// https://github.com/kubernetes/kops/blob/7a629c77431dda02d02aadf00beb0bed87518cbf/pkg/instancegroups/instancegroups.go lines 337-340
	out, err := runScript(kubeCtlCall+" drain "+nodeName+" --ignore-daemonsets=true --delete-local-data=true --force --grace-period=-1", false, ruObj.Name)
	if err != nil {
		if strings.HasPrefix(out, "Error from server (NotFound)") {
			log.Printf("%s: Not executing postDrainHelper. Node not found: %s", ruObj.Name, out)
			return nil
		}

		return errors.New(ruObj.Name + ": Failed to drain: " + err.Error())
	}

	return r.postDrainHelper(ruObj, nodeName, kubeCtlCall)
}

// TerminateNode actually terminates the given node.
func (r *RollingUpgradeReconciler) TerminateNode(ruObj *upgrademgrv1alpha1.RollingUpgrade, instanceID string, svc ec2iface.EC2API) error {

	input := &ec2.TerminateInstancesInput{
		InstanceIds: []*string{
			aws.String(instanceID),
		},
	}

	result, err := svc.TerminateInstances(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case "InvalidInstanceID.NotFound":
				log.Printf("Instance %s not found. Moving on\n", instanceID)
				return nil
			default:
				log.Println(aerr.Error())
				return err
			}
		}
	}

	log.Printf("%s: Termination output: %v\n", ruObj.Name, result)

	time.Sleep(time.Duration(ruObj.Spec.NodeIntervalSeconds) * time.Second)
	if ruObj.Spec.PostTerminate.Script != "" {
		out, err := runScript(ruObj.Spec.PostTerminate.Script, false, ruObj.Name)
		if err != nil {
			if strings.HasPrefix(out, "Error from server (NotFound)") {
				log.Printf("%s: Node not found when running postTerminate: %s. Ignoring ...", ruObj.Name, out)
				return nil
			}
			msg := "Failed to run postTerminate script: " + err.Error()
			log.Printf("%s: %s", ruObj.Name, msg)
			return errors.New(msg)
		}
	}
	return nil
}

func (r *RollingUpgradeReconciler) setDefaults(ruObj *upgrademgrv1alpha1.RollingUpgrade) {
	if ruObj.Spec.Region == "" {
		ruObj.Spec.Region = "us-west-2"
	}
}

func (r *RollingUpgradeReconciler) getNodeName(i *autoscaling.Instance, nodeList *corev1.NodeList, ruObj *upgrademgrv1alpha1.RollingUpgrade) string {
	node := r.getNodeFromAsg(i, nodeList, ruObj)
	if node == nil {
		log.Printf("%s: Node name for instance %s not found\n", ruObj.Name, *i.InstanceId)
		return ""
	}
	return node.Name
}

func (r *RollingUpgradeReconciler) getNodeFromAsg(i *autoscaling.Instance, nodeList *corev1.NodeList, ruObj *upgrademgrv1alpha1.RollingUpgrade) *corev1.Node {
	for _, n := range nodeList.Items {
		tokens := strings.Split(n.Spec.ProviderID, "/")
		justID := tokens[len(tokens)-1]
		if *i.InstanceId == justID {
			log.Printf("%s: Found instance %s, name %s\n", ruObj.Name, justID, n.Name)
			return &n
		}
	}

	log.Printf("%s: Node for instance %s not found\n", ruObj.Name, *i.InstanceId)
	return nil
}

func (r *RollingUpgradeReconciler) populateAsg(ruObj *upgrademgrv1alpha1.RollingUpgrade, svc autoscalingiface.AutoScalingAPI) error {
	// Initialize a session in the given region that the SDK will use to load
	// credentials.
	input := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{
			aws.String(ruObj.Spec.AsgName),
		},
	}

	result, err := svc.DescribeAutoScalingGroups(input)
	if err != nil {
		log.Println(err.Error())
		return errors.New(ruObj.Name + ": Failed to describe autoscaling group: " + err.Error())
	}

	if len(result.AutoScalingGroups) == 0 {
		log.Printf("%s: No ASG found with name %s!\n", ruObj.Name, ruObj.Spec.AsgName)
		return errors.New("No ASG found")
	} else if len(result.AutoScalingGroups) > 1 {
		log.Printf("%s: Too many asgs found with name %d!\n", ruObj.Name, len(result.AutoScalingGroups))
		return errors.New("Too many asgs")
	}

	asg := result.AutoScalingGroups[0]
	r.ruObjNameToASG.Store(ruObj.Name, asg)

	return nil
}

func (r *RollingUpgradeReconciler) populateNodeList(ruObj *upgrademgrv1alpha1.RollingUpgrade, nodeInterface v1.NodeInterface) error {
	nodeList, err := nodeInterface.List(metav1.ListOptions{})
	if err != nil {
		msg := "Failed to get all nodes in the cluster: " + err.Error()
		log.Printf("%s: %s", ruObj.Name, msg)
		return errors.New(ruObj.Name + ": Failed to get all nodes in the cluster: " + err.Error())
	}

	nodesFound := ""
	for _, n := range nodeList.Items {
		nodesFound = n.Name + "," + nodesFound
	}
	log.Printf("%s: Kubernetes nodes found: %s", ruObj.Name, nodesFound)

	r.NodeList = nodeList
	return nil
}

// Loads specific environment variables for scripts to use
// on a given rollingUpgrade and autoscaling instance
func loadEnvironmentVariables(ruObj *upgrademgrv1alpha1.RollingUpgrade, nodeInstance *corev1.Node) error {
	if err := os.Setenv(asgNameKey, ruObj.Spec.AsgName); err != nil {
		return errors.New(ruObj.Name + ": Could not load " + asgNameKey + ": " + err.Error())
	}
	tokens := strings.Split(nodeInstance.Spec.ProviderID, "/")
	justID := tokens[len(tokens)-1]
	if err := os.Setenv(instanceIDKey, justID); err != nil {
		return errors.New(ruObj.Name + ": Could not load " + instanceIDKey + ": " + err.Error())
	}
	if err := os.Setenv(instanceNameKey, nodeInstance.GetName()); err != nil {
		return errors.New(ruObj.Name + ": Could not load " + instanceNameKey + ": " + err.Error())
	}
	return nil
}

func (r *RollingUpgradeReconciler) runRestack(ctx *context.Context, ruObj *upgrademgrv1alpha1.RollingUpgrade, svc ec2iface.EC2API, KubeCtlCall string) (int, error) {
	value, ok := r.ruObjNameToASG.Load(ruObj.Name)
	if !ok {
		msg := "Failed to find rollingUpgrade name in map."
		log.Printf(msg)
		return 0, errors.New(msg)
	}

	asg := value.(*autoscaling.Group)
	log.Printf("Nodes in ASG %s that *might* need to be updated: %d\n", *asg.AutoScalingGroupName, len(asg.Instances))
	currentLaunchConfigName := aws.StringValue(asg.LaunchConfigurationName)
	nodesProcessed := 0
	for _, i := range asg.Instances {
		targetLaunchConfigName := aws.StringValue(i.LaunchConfigurationName)
		targetInstanceID := aws.StringValue(i.InstanceId)
		nodesProcessed++

		// If the running node has the same launchconfig as the asg,
		// there is no need to refresh it.
		if targetLaunchConfigName != "" {
			// if LaunchConfig is blank, the instance is not inline with the active LaunchConfig
			if targetLaunchConfigName == currentLaunchConfigName {
				log.Printf("Ignoring %s since it has the correct launch-config", targetInstanceID)
				ruObj.Status.NodesProcessed = ruObj.Status.NodesProcessed + 1
				r.Update(*ctx, ruObj)
				continue
			}
		}

		nodeName := r.getNodeName(i, r.NodeList, ruObj)
		if nodeName == "" {
			continue
		}

		// Load the environment variables for scripts to run
		err := loadEnvironmentVariables(ruObj, r.getNodeFromAsg(i, r.NodeList, ruObj))
		if err != nil {
			return 0, err
		}

		// Drain and wait for draining node.
		err = r.DrainNode(ruObj, nodeName, KubeCtlCall)
		if err != nil {
			return 0, err
		}

		// Terminate instance.
		err = r.TerminateNode(ruObj, targetInstanceID, svc)
		if err != nil {
			runScript(KubeCtlCall+" uncordon "+nodeName, false, ruObj.Name)
			return 0, err
		}

		ruObj.Status.NodesProcessed = ruObj.Status.NodesProcessed + 1
		r.Update(*ctx, ruObj)
	}

	return nodesProcessed, nil
}

func (r *RollingUpgradeReconciler) finishExecution(finalStatus string, nodesProcessed int, ctx *context.Context, ruObj *upgrademgrv1alpha1.RollingUpgrade) (reconcile.Result, error) {
	log.Printf("Marked object %s as %s", ruObj.Name, finalStatus)
	endTime := time.Now()
	ruObj.Status.EndTime = endTime.Format(time.RFC3339)
	ruObj.Status.CurrentStatus = finalStatus
	ruObj.Status.NodesProcessed = nodesProcessed

	startTime, err := time.Parse(time.RFC3339, ruObj.Status.StartTime)
	if err != nil {
		log.Printf("Failed to calculate totalProcessingTime for %s", ruObj.Name)
	} else {
		ruObj.Status.TotalProcessingTime = endTime.Sub(startTime).String()
	}

	MarkObjForCleanup(ruObj)
	r.Update(*ctx, ruObj)
	r.admissionMap.Delete(ruObj.Name)
	log.Printf("Deleted %s from admission map %p", ruObj.Name, &r.admissionMap)
	return reconcile.Result{}, nil
}

// Process actually performs the ec2-instance restacking.
func (r *RollingUpgradeReconciler) Process(ctx *context.Context, ruObj *upgrademgrv1alpha1.RollingUpgrade) (reconcile.Result, error) {
	logr := r.Log.WithValues("rollingupgrade", ruObj.Name)

	// If the object is being deleted, nothing to do.
	if !ruObj.DeletionTimestamp.IsZero() {
		logr.Info("Object is being deleted. No more processing")
		r.admissionMap.Delete(ruObj.Name)
		logr.Info("Deleted object from admission map")
		return reconcile.Result{}, nil
	}

	if ruObj.Status.CurrentStatus == StatusComplete ||
		ruObj.Status.CurrentStatus == StatusError {
		logr.Info("No more processing", "Object state", ruObj.Status.CurrentStatus)

		if exists := ruObj.ObjectMeta.Annotations[JanitorAnnotation]; exists == "" {
			logr.Info("Marking object for deletion")
			MarkObjForCleanup(ruObj)
			r.Update(*ctx, ruObj)
		}

		r.admissionMap.Delete(ruObj.Name)
		logr.Info("Deleted object from admission map")
		return reconcile.Result{}, nil
	}

	r.setDefaults(ruObj)

	sess, _ := session.NewSession(&aws.Config{
		Region: aws.String(ruObj.Spec.Region)},
	)
	svc := autoscaling.New(sess)
	err := r.populateAsg(ruObj, svc)
	if err != nil {
		return r.finishExecution(StatusError, 0, ctx, ruObj)
	}

	//TODO(shri): Ensure that no node is Unschedulable at this time.
	err = r.populateNodeList(ruObj, r.generatedClient.CoreV1().Nodes())
	if err != nil {
		return r.finishExecution(StatusError, 0, ctx, ruObj)
	}

	// Update the CR with some basic info before staring the restack.
	ruObj.Status.StartTime = time.Now().Format(time.RFC3339)
	ruObj.Status.CurrentStatus = StatusRunning
	ruObj.Status.NodesProcessed = 0

	value, ok := r.ruObjNameToASG.Load(ruObj.Name)
	if !ok {
		msg := "Failed to find rollingUpgrade name in map."
		log.Printf(msg)
		return r.finishExecution(StatusError, 0, ctx, ruObj)
	}
	asg := value.(*autoscaling.Group)
	ruObj.Status.TotalNodes = len(asg.Instances)
	r.Update(*ctx, ruObj)

	ec2Sess, _ := session.NewSession(&aws.Config{
		Region: aws.String(ruObj.Spec.Region)},
	)
	ec2Svc := ec2.New(ec2Sess)

	// Run the restack that acutally performs the rolling update.
	nodesProcessed, err := r.runRestack(ctx, ruObj, ec2Svc, KubeCtlBinary)
	if err != nil {
		return r.finishExecution(StatusError, nodesProcessed, ctx, ruObj)
	}

	return r.finishExecution(StatusComplete, nodesProcessed, ctx, ruObj)
}

// MarkObjForCleanup sets the annotation on the given object for deletion.
func MarkObjForCleanup(ruObj *upgrademgrv1alpha1.RollingUpgrade) {
	if ruObj.ObjectMeta.Annotations == nil {
		ruObj.ObjectMeta.Annotations = map[string]string{}
	}

	switch ruObj.Status.CurrentStatus {
	case StatusComplete:
		ruObj.ObjectMeta.Annotations[JanitorAnnotation] = ClearCompletedFrequency
	case StatusError:
		ruObj.ObjectMeta.Annotations[JanitorAnnotation] = ClearErrorFrequency
	}
}

// +kubebuilder:rbac:groups=upgrademgr.orkaproj.io,resources=rollingupgrades,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=upgrademgr.orkaproj.io,resources=rollingupgrades/status,verbs=get;update;patch

// Reconcile reads that state of the cluster for a RollingUpgrade object and makes changes based on the state read
// and the details in the RollingUpgrade.Spec
func (r *RollingUpgradeReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	logr := r.Log.WithValues("rollingupgrade", req.NamespacedName)

	// Fetch the RollingUpgrade instance
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{}
	err := r.Get(ctx, req.NamespacedName, ruObj)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Object not found, return. Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			r.admissionMap.Delete(req.Name)
			logr.Info("Deleted object from map", "name", req.Name)
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	result, ok := r.admissionMap.Load(ruObj.Name)
	if ok {
		if result == "processing" {
			logr.Info("Found obj in map:", "name", ruObj.Name)
			logr.Info("Object already being processed", "name", ruObj.Name)
		} else {
			logr.Info("Sync map with invalid entry for ", "name", ruObj.Name)
		}
	} else {
		go r.Process(&ctx, ruObj)
		logr.Info("Adding obj to map: ", "name", ruObj.Name)
		r.admissionMap.Store(ruObj.Name, "processing")
	}

	return ctrl.Result{}, nil
}

// SetupWithManager creates a new manager.
func (r *RollingUpgradeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.generatedClient = kubernetes.NewForConfigOrDie(mgr.GetConfig())
	return ctrl.NewControllerManagedBy(mgr).
		For(&upgrademgrv1alpha1.RollingUpgrade{}).
		Complete(r)
}
