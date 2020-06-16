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
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/autoscaling/autoscalingiface"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/go-logr/logr"
	"github.com/keikoproj/aws-sdk-go-cache/cache"
	iebackoff "github.com/keikoproj/inverse-exp-backoff"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	v1errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	upgrademgrv1alpha1 "github.com/keikoproj/upgrade-manager/api/v1alpha1"
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
	// EC2StateTagKey is the EC2 tag key for indicating the state
	EC2StateTagKey = "upgrademgr.keikoproj.io/state"

	// Environment variable keys
	asgNameKey      = "ASG_NAME"
	instanceIDKey   = "INSTANCE_ID"
	instanceNameKey = "INSTANCE_NAME"

	// KubeCtlBinary is the path to the kubectl executable
	KubeCtlBinary = "/usr/local/bin/kubectl"
	// ShellBinary is the path to the shell executable
	ShellBinary = "/bin/sh"
)

var (
	// TerminationTimeoutSeconds is the timeout threshold for waiting for a node object unjoin
	TerminationTimeoutSeconds = 3600
	// TerminationSleepIntervalSeconds is the polling interval for checking if a node object is unjoined
	TerminationSleepIntervalSeconds = 30
	// WaiterMaxDelay is the maximum delay for waiters inverse exponential backoff
	WaiterMaxDelay = time.Second * 90
	// WaiterMinDelay is the minimum delay for waiters inverse exponential backoff
	WaiterMinDelay = time.Second * 15
	// WaiterFactor is the delay reduction factor per retry
	WaiterFactor = 0.5
	// WaiterMaxAttempts is the maximum number of retries for waiters
	WaiterMaxAttempts = uint32(32)
)

// RollingUpgradeReconciler reconciles a RollingUpgrade object
type RollingUpgradeReconciler struct {
	client.Client
	Log             logr.Logger
	EC2Client       ec2iface.EC2API
	ASGClient       autoscalingiface.AutoScalingAPI
	generatedClient *kubernetes.Clientset
	NodeList        *corev1.NodeList
	admissionMap    sync.Map
	ruObjNameToASG  sync.Map
	ClusterState    ClusterState
	maxParallel     int
	CacheConfig     *cache.Config
}

func (r *RollingUpgradeReconciler) SetMaxParallel(max int) {
	if max >= 1 {
		r.Log.Info(fmt.Sprintf("max parallel reconciles = %v", max))
		r.maxParallel = max
	}
}

func (r *RollingUpgradeReconciler) runScript(script string, background bool, ruObj *upgrademgrv1alpha1.RollingUpgrade) (string, error) {
	r.info(ruObj, "Running script", "script", script)
	if background {
		r.info(ruObj, "Running script in background. Logs not available.")
		err := exec.Command(ShellBinary, "-c", script).Run()
		if err != nil {
			r.info(ruObj, "Script finished with error: %s", err)
		}

		return "", nil
	}

	out, err := exec.Command(ShellBinary, "-c", script).CombinedOutput()
	if err != nil {
		r.error(ruObj, err, "Script finished", "output", string(out))
	} else {
		r.info(ruObj, "Script finished", "output", string(out))
	}
	return string(out), err
}

func (r *RollingUpgradeReconciler) preDrainHelper(ruObj *upgrademgrv1alpha1.RollingUpgrade) error {
	if ruObj.Spec.PreDrain.Script != "" {
		script := ruObj.Spec.PreDrain.Script
		_, err := r.runScript(script, false, ruObj)
		if err != nil {
			msg := "Failed to run preDrain script"
			r.error(ruObj, err, msg)
			return errors.Wrap(err, msg)
		}
	}
	return nil
}

// Operates on any scripts that were provided after the draining of the node.
// kubeCtlCall is provided as an argument to decouple the method from the actual kubectl call
func (r *RollingUpgradeReconciler) postDrainHelper(ruObj *upgrademgrv1alpha1.RollingUpgrade, nodeName string, kubeCtlCall string) error {
	if ruObj.Spec.PostDrain.Script != "" {
		_, err := r.runScript(ruObj.Spec.PostDrain.Script, false, ruObj)
		if err != nil {
			msg := "Failed to run postDrain script: "
			r.error(ruObj, err, msg)
			result := errors.Wrap(err, msg)

			r.info(ruObj, "Uncordoning the node %s since it failed to run postDrain Script", "nodeName", nodeName)
			_, err = r.runScript(kubeCtlCall+" uncordon "+nodeName, false, ruObj)
			if err != nil {
				r.error(ruObj, err, "Failed to uncordon", "nodeName", nodeName)
			}
			return result
		}
	}
	r.info(ruObj, "Waiting for postDrainDelay", "postDrainDelay", ruObj.Spec.PostDrainDelaySeconds)
	time.Sleep(time.Duration(ruObj.Spec.PostDrainDelaySeconds) * time.Second)

	if ruObj.Spec.PostDrain.PostWaitScript != "" {
		_, err := r.runScript(ruObj.Spec.PostDrain.PostWaitScript, false, ruObj)
		if err != nil {
			msg := "Failed to run postDrainWait script: " + err.Error()
			r.error(ruObj, err, msg)
			result := errors.Wrap(err, msg)

			r.info(ruObj, "Uncordoning the node %s since it failed to run postDrain Script", "nodeName", nodeName)
			_, err = r.runScript(kubeCtlCall+" uncordon "+nodeName, false, ruObj)
			if err != nil {
				r.error(ruObj, err, "Failed to uncordon", "nodeName", nodeName)
			}
			return result
		}
	}
	return nil
}

// DrainNode runs "kubectl drain" on the given node
// kubeCtlCall is provided as an argument to decouple the method from the actual kubectl call
func (r *RollingUpgradeReconciler) DrainNode(ruObj *upgrademgrv1alpha1.RollingUpgrade,
	nodeName string,
	kubeCtlCall string,
	drainTimeout int) error {
	// Running kubectl drain node.
	err := r.preDrainHelper(ruObj)
	if err != nil {
		return errors.New(ruObj.Name + ": Predrain script failed: " + err.Error())
	}

	errChan := make(chan error)
	ctx := context.TODO()
	var cancel context.CancelFunc

	// Add a context with timeout only if a valid drain timeout value is specified
	// default value used for drain timeout is -1
	if drainTimeout >= 0 {
		r.info(ruObj, "Creating a context with timeout", "drainTimeout", drainTimeout)
		// Define a cancellation after drainTimeout
		ctx, cancel = context.WithTimeout(ctx, time.Duration(drainTimeout)*time.Second)
		defer cancel()
	} else {
		r.info(ruObj, "Skipped creating context with timeout.", "drainTimeout", drainTimeout)
	}

	r.info(ruObj, "Invoking kubectl drain for the node", "nodeName", nodeName)
	go r.CallKubectlDrain(ctx, nodeName, kubeCtlCall, ruObj, errChan)

	// Listening to signals from the CallKubectlDrain go routine
	select {
	case <-ctx.Done():
		r.error(ruObj, ctx.Err(), "Kubectl drain timed out for node", "nodeName", nodeName)
	case err := <-errChan:
		if err != nil {
			r.error(ruObj, err, "Kubectl drain errored for node", "nodeName", nodeName)
			return err
		}
		r.info(ruObj, "Kubectl drain completed for node", "nodeName", nodeName)
	}

	return r.postDrainHelper(ruObj, nodeName, kubeCtlCall)
}

// CallKubectlDrain runs the "kubectl drain" for a given node
// Node will be terminated even if pod eviction is not completed when the drain timeout is exceeded
func (r *RollingUpgradeReconciler) CallKubectlDrain(ctx context.Context, nodeName, kubeCtlCall string, ruObj *upgrademgrv1alpha1.RollingUpgrade, errChan chan error) {

	// kops behavior implements the same behavior by using these flags when draining nodes
	// https://github.com/kubernetes/kops/blob/7a629c77431dda02d02aadf00beb0bed87518cbf/pkg/instancegroups/instancegroups.go lines 337-340
	script := fmt.Sprintf("%s drain %s --ignore-daemonsets=true --delete-local-data=true --force --grace-period=-1", kubeCtlCall, nodeName)
	out, err := r.runScript(script, false, ruObj)
	if err != nil {
		if strings.HasPrefix(out, "Error from server (NotFound)") {
			r.error(ruObj, err, "Not executing postDrainHelper. Node not found.", "output", out)
			errChan <- nil
			return
		}
		errChan <- errors.New(ruObj.Name + " :Failed to drain: " + err.Error())
		return
	}
	errChan <- nil
}

func (r *RollingUpgradeReconciler) WaitForDesiredInstances(ruObj *upgrademgrv1alpha1.RollingUpgrade) error {
	var err error
	var ieb *iebackoff.IEBackoff
	for ieb, err = iebackoff.NewIEBackoff(WaiterMaxDelay, WaiterMinDelay, 0.5, WaiterMaxAttempts); err == nil; err = ieb.Next() {
		err = r.populateAsg(ruObj)
		if err != nil {
			return err
		}

		asg, err := r.GetAutoScalingGroup(ruObj.Name)
		if err != nil {
			return fmt.Errorf("Unable to load ASG with name: %s", ruObj.Name)
		}

		inServiceCount := getInServiceCount(asg.Instances)
		if inServiceCount == aws.Int64Value(asg.DesiredCapacity) {
			r.info(ruObj, "desired capacity is met", "inServiceCount", inServiceCount)
			return nil
		}

		r.info(ruObj, "new instance has not yet joined the scaling group")
	}
	return errors.Wrapf(err, "%v: WaitForDesiredInstances timed out while waiting for instance to be added", ruObj.Name)
}

func (r *RollingUpgradeReconciler) WaitForDesiredNodes(ruObj *upgrademgrv1alpha1.RollingUpgrade) error {
	var err error
	var ieb *iebackoff.IEBackoff
	for ieb, err = iebackoff.NewIEBackoff(WaiterMaxDelay, WaiterMinDelay, 0.5, WaiterMaxAttempts); err == nil; err = ieb.Next() {
		err = r.populateAsg(ruObj)
		if err != nil {
			return err
		}

		err = r.populateNodeList(ruObj, r.generatedClient.CoreV1().Nodes())
		if err != nil {
			r.error(ruObj, err, "unable to populate node list")
		}

		asg, err := r.GetAutoScalingGroup(ruObj.Name)
		if err != nil {
			return fmt.Errorf("Unable to load ASG with name: %s", ruObj.Name)
		}

		// get list of inService instance IDs
		inServiceInstances := getInServiceIds(asg.Instances)
		desiredCapacity := aws.Int64Value(asg.DesiredCapacity)

		// check all of them are nodes and are ready
		var foundCount int64 = 0
		for _, node := range r.NodeList.Items {
			tokens := strings.Split(node.Spec.ProviderID, "/")
			instanceID := tokens[len(tokens)-1]
			if contains(inServiceInstances, instanceID) && isNodeReady(node) {
				foundCount++
			}
		}

		if foundCount == desiredCapacity {
			r.info(ruObj, "desired capacity is met", "inServiceCount", foundCount)
			return nil
		}

		r.info(ruObj, "new node has not yet joined the cluster")
	}
	return errors.Wrapf(err, "%v: WaitForDesiredNodes timed out while waiting for nodes to join", ruObj.Name)
}

func (r *RollingUpgradeReconciler) WaitForTermination(ruObj *upgrademgrv1alpha1.RollingUpgrade, nodeName string, nodeInterface v1.NodeInterface) (bool, error) {

	started := time.Now()
	for {
		if time.Since(started) >= (time.Second * time.Duration(TerminationTimeoutSeconds)) {
			r.info(ruObj, "WaitForTermination timed out while waiting for node to unjoin")
			return false, nil
		}

		_, err := nodeInterface.Get(nodeName, metav1.GetOptions{})
		if v1errors.IsNotFound(err) {
			r.info(ruObj, "node is unjoined from cluster, upgrade will proceed", "nodeName", nodeName)
			break
		}

		r.info(ruObj, "node is still joined to cluster, will wait and retry",
			"nodeName", nodeName, "terminationSleepIntervalSeconds", TerminationSleepIntervalSeconds)

		time.Sleep(time.Duration(TerminationSleepIntervalSeconds) * time.Second)
	}
	return true, nil
}

func (r *RollingUpgradeReconciler) GetAutoScalingGroup(rollupName string) (*autoscaling.Group, error) {
	asg := &autoscaling.Group{}
	val, ok := r.ruObjNameToASG.Load(rollupName)
	if !ok {
		return asg, fmt.Errorf("Unable to load ASG with name: %s", rollupName)
	}
	asg = val.(*autoscaling.Group)
	return asg, nil
}

// SetStandby sets the autoscaling instance to standby mode.
func (r *RollingUpgradeReconciler) SetStandby(ruObj *upgrademgrv1alpha1.RollingUpgrade, instanceID string) error {
	r.info(ruObj, "Setting to stand-by", ruObj.Name, instanceID)
	input := &autoscaling.EnterStandbyInput{
		AutoScalingGroupName:           aws.String(ruObj.Spec.AsgName),
		InstanceIds:                    aws.StringSlice([]string{instanceID}),
		ShouldDecrementDesiredCapacity: aws.Bool(false),
	}

	asg, err := r.GetAutoScalingGroup(ruObj.Name)
	if err != nil {
		return err
	}

	instanceState, err := getGroupInstanceState(asg, instanceID)
	if err != nil {
		return err
	}

	if !isInServiceLifecycleState(instanceState) {
		r.info(ruObj, "Cannot set instance to stand-by, instance is in state", "instanceState", instanceState, "instanceID", instanceID)
		return nil
	}

	_, err = r.ASGClient.EnterStandby(input)
	if err != nil {
		r.error(ruObj, err, "Failed to enter standby", "instanceID", instanceID)
	}
	return nil
}

// TerminateNode actually terminates the given node.
func (r *RollingUpgradeReconciler) TerminateNode(ruObj *upgrademgrv1alpha1.RollingUpgrade, instanceID string) error {

	input := &autoscaling.TerminateInstanceInAutoScalingGroupInput{
		InstanceId:                     aws.String(instanceID),
		ShouldDecrementDesiredCapacity: aws.Bool(false),
	}
	var err error
	var ieb *iebackoff.IEBackoff
	for ieb, err = iebackoff.NewIEBackoff(WaiterMaxDelay, WaiterMinDelay, 0.5, WaiterMaxAttempts); err == nil; err = ieb.Next() {
		_, err := r.ASGClient.TerminateInstanceInAutoScalingGroup(input)
		if err == nil {
			break
		}
		if aerr, ok := err.(awserr.Error); ok {
			if strings.Contains(aerr.Message(), "not found") {
				r.info(ruObj, "Instance not found. Moving on", "instanceID", instanceID)
				return nil
			}
			switch aerr.Code() {
			case autoscaling.ErrCodeScalingActivityInProgressFault:
				r.error(ruObj, aerr, autoscaling.ErrCodeScalingActivityInProgressFault, "instanceID", instanceID)
			case autoscaling.ErrCodeResourceContentionFault:
				r.error(ruObj, aerr, autoscaling.ErrCodeResourceContentionFault, "instanceID", instanceID)
			default:
				r.error(ruObj, aerr, aerr.Code(), "instanceID", instanceID)
				return err
			}
		}
	}
	if err != nil {
		return err
	}
	r.info(ruObj, "Instance terminated.", "instanceID", instanceID)
	r.info(ruObj, "starting post termination sleep", "instanceID", instanceID, "nodeIntervalSeconds", ruObj.Spec.NodeIntervalSeconds)
	time.Sleep(time.Duration(ruObj.Spec.NodeIntervalSeconds) * time.Second)
	if ruObj.Spec.PostTerminate.Script != "" {
		out, err := r.runScript(ruObj.Spec.PostTerminate.Script, false, ruObj)
		if err != nil {
			if strings.HasPrefix(out, "Error from server (NotFound)") {
				r.error(ruObj, err, "Node not found when running postTerminate. Ignoring ...", "output", out, "instanceID", instanceID)
				return nil
			}
			msg := "Failed to run postTerminate script"
			r.error(ruObj, err, msg, "instanceID", instanceID)
			return errors.Wrap(err, msg)
		}
	}
	return nil
}

func (r *RollingUpgradeReconciler) getNodeName(i *autoscaling.Instance, nodeList *corev1.NodeList, ruObj *upgrademgrv1alpha1.RollingUpgrade) string {
	node := r.getNodeFromAsg(i, nodeList, ruObj)
	if node == nil {
		r.info(ruObj, "Node name for instance not found", "instanceID", *i.InstanceId)
		return ""
	}
	return node.Name
}

func (r *RollingUpgradeReconciler) getNodeFromAsg(i *autoscaling.Instance, nodeList *corev1.NodeList, ruObj *upgrademgrv1alpha1.RollingUpgrade) *corev1.Node {
	for _, n := range nodeList.Items {
		tokens := strings.Split(n.Spec.ProviderID, "/")
		justID := tokens[len(tokens)-1]
		if *i.InstanceId == justID {
			r.info(ruObj, "Found instance", "instanceID", justID, "instanceName", n.Name)
			return &n
		}
	}

	r.info(ruObj, "Node for instance not found", "instanceID", *i.InstanceId)
	return nil
}

func (r *RollingUpgradeReconciler) populateAsg(ruObj *upgrademgrv1alpha1.RollingUpgrade) error {
	input := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{
			aws.String(ruObj.Spec.AsgName),
		},
	}
	result, err := r.ASGClient.DescribeAutoScalingGroups(input)
	if err != nil {
		r.error(ruObj, err, "Failed to describe autoscaling group")
		return errors.Wrap(err, ruObj.Name+": Failed to describe autoscaling group")
	}

	if len(result.AutoScalingGroups) == 0 {
		r.info(ruObj, "%s: No ASG found with name %s!\n", ruObj.Name, ruObj.Spec.AsgName)
		return errors.New("No ASG found")
	} else if len(result.AutoScalingGroups) > 1 {
		r.info(ruObj, "%s: Too many asgs found with name %d!\n", ruObj.Name, len(result.AutoScalingGroups))
		return errors.New("Too many ASGs")
	}

	asg := result.AutoScalingGroups[0]
	r.ruObjNameToASG.Store(ruObj.Name, asg)

	return nil
}

func (r *RollingUpgradeReconciler) populateNodeList(ruObj *upgrademgrv1alpha1.RollingUpgrade, nodeInterface v1.NodeInterface) error {
	nodeList, err := nodeInterface.List(metav1.ListOptions{})
	if err != nil {
		msg := "Failed to get all nodes in the cluster: " + err.Error()
		r.info(ruObj, msg)
		return errors.Wrap(err, ruObj.Name+": Failed to get all nodes in the cluster")
	}
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

func (r *RollingUpgradeReconciler) getInProgressInstances(instances []*autoscaling.Instance) ([]*autoscaling.Instance, error) {
	var inProgressInstances []*autoscaling.Instance
	taggedInstances, err := getTaggedInstances(EC2StateTagKey, "in-progress", r.EC2Client)
	if err != nil {
		return inProgressInstances, err
	}
	for _, instance := range instances {
		if contains(taggedInstances, aws.StringValue(instance.InstanceId)) {
			inProgressInstances = append(inProgressInstances, instance)
		}
	}
	return inProgressInstances, nil
}

func (r *RollingUpgradeReconciler) runRestack(ctx *context.Context, ruObj *upgrademgrv1alpha1.RollingUpgrade, KubeCtlCall string) (int, error) {

	asg, err := r.GetAutoScalingGroup(ruObj.Name)
	if err != nil {
		return 0, fmt.Errorf("Unable to load ASG with name: %s", ruObj.Name)
	}

	r.info(ruObj, "Nodes in ASG that *might* need to be updated", "asgName", *asg.AutoScalingGroupName, "asgSize", len(asg.Instances))

	// No further processing is required if ASG doesn't have an instance running
	totalNodes := len(asg.Instances)
	// No further processing is required if ASG doesn't have an instance running
	if totalNodes == 0 {
		r.info(ruObj, "Total nodes found for %s is 0", ruObj.Name)
		return 0, nil
	}

	nodeSelector := getNodeSelector(asg, ruObj)

	// set the state of instances in the ASG to new in the cluster store
	r.ClusterState.initializeAsg(*asg.AutoScalingGroupName, asg.Instances)

	launchDefinition := NewLaunchDefinition(asg)

	processedInstances := 0

	inProgress, err := r.getInProgressInstances(asg.Instances)
	if err != nil {
		r.error(ruObj, err, "Failed to acquire in-progress instances")
	}

	for processedInstances < totalNodes {
		var instances []*autoscaling.Instance
		if len(inProgress) == 0 {
			// Fetch instances to update from node selector
			instances = nodeSelector.SelectNodesForRestack(r.ClusterState)
			r.info(ruObj, fmt.Sprintf("selected instances for rotation: %+v", instances))
		} else {
			// Prefer in progress instances over new ones
			instances = inProgress
			inProgress = []*autoscaling.Instance{}
			r.info(ruObj, fmt.Sprintf("found in progress instances: %+v", instances))
		}

		if instances == nil {
			errorMessage := fmt.Sprintf(
				"No instances available for update across all AZ's for %s. Processed %d of total %d instances",
				ruObj.Name, processedInstances, totalNodes)
			// No instances fetched from any AZ, stop processing
			r.info(ruObj, errorMessage)

			// this should never be case, return error
			return processedInstances, errors.New(errorMessage)
		}

		// update the instances
		err := r.UpdateInstances(ctx, ruObj, instances, launchDefinition, KubeCtlCall)
		processedInstances += len(instances)
		if err != nil {
			return processedInstances, err
		}
	}
	r.ClusterState.deleteEntryOfAsg(*asg.AutoScalingGroupName)
	return processedInstances, nil
}

func (r *RollingUpgradeReconciler) finishExecution(finalStatus string, nodesProcessed int, ctx *context.Context, ruObj *upgrademgrv1alpha1.RollingUpgrade) {
	// delete the entry instances of the ASG
	r.ClusterState.deleteEntryOfAsg(ruObj.Spec.AsgName)
	r.info(ruObj, "Deleted the entries of ASG in the cluster store", "asgName", ruObj.Spec.AsgName)

	r.info(ruObj, "Marked object as", "finalStatus", finalStatus)
	endTime := time.Now()
	ruObj.Status.EndTime = endTime.Format(time.RFC3339)
	ruObj.Status.CurrentStatus = finalStatus
	ruObj.Status.NodesProcessed = nodesProcessed

	ruObj.Status.Conditions = append(ruObj.Status.Conditions,
		upgrademgrv1alpha1.RollingUpgradeCondition{
			Type:   upgrademgrv1alpha1.UpgradeComplete,
			Status: corev1.ConditionTrue,
		})

	startTime, err := time.Parse(time.RFC3339, ruObj.Status.StartTime)
	if err != nil {
		r.info(ruObj, "Failed to calculate totalProcessingTime")
	} else {
		ruObj.Status.TotalProcessingTime = endTime.Sub(startTime).String()
	}
	// end event
	var level string
	if finalStatus == StatusComplete {
		level = EventLevelNormal
	} else {
		level = EventLevelWarning
	}
	r.createK8sV1Event(ruObj, EventReasonRUFinished, level, map[string]string{
		"status":   finalStatus,
		"asgName":  ruObj.Spec.AsgName,
		"strategy": string(ruObj.Spec.Strategy.Type),
		"info":     fmt.Sprintf("Rolling Upgrade as finished (status=%s)", finalStatus),
	})

	MarkObjForCleanup(ruObj)
	if err := r.Status().Update(*ctx, ruObj); err != nil {
		r.error(ruObj, err, "failed to update status")
	}
	r.admissionMap.Delete(ruObj.Name)
	r.info(ruObj, "Deleted from admission map ", "admissionMap", &r.admissionMap)
}

// Process actually performs the ec2-instance restacking.
func (r *RollingUpgradeReconciler) Process(ctx *context.Context,
	ruObj *upgrademgrv1alpha1.RollingUpgrade) {

	if ruObj.Status.CurrentStatus == StatusComplete ||
		ruObj.Status.CurrentStatus == StatusError {
		r.info(ruObj, "No more processing", "currentStatus", ruObj.Status.CurrentStatus)

		if exists := ruObj.ObjectMeta.Annotations[JanitorAnnotation]; exists == "" {
			r.info(ruObj, "Marking object for deletion")
			MarkObjForCleanup(ruObj)
		}

		r.admissionMap.Delete(ruObj.Name)
		r.info(ruObj, "Deleted object from admission map")
		return
	}
	// start event
	r.createK8sV1Event(ruObj, EventReasonRUStarted, EventLevelNormal, map[string]string{
		"status":   "started",
		"asgName":  ruObj.Spec.AsgName,
		"strategy": string(ruObj.Spec.Strategy.Type),
		"msg":      "Rolling Upgrade has started",
	})
	r.CacheConfig.FlushCache("autoscaling")
	err := r.populateAsg(ruObj)
	if err != nil {
		r.finishExecution(StatusError, 0, ctx, ruObj)
		return
	}

	//TODO(shri): Ensure that no node is Unschedulable at this time.
	err = r.populateNodeList(ruObj, r.generatedClient.CoreV1().Nodes())
	if err != nil {
		r.finishExecution(StatusError, 0, ctx, ruObj)
		return
	}

	asg, err := r.GetAutoScalingGroup(ruObj.Name)
	if err != nil {
		r.error(ruObj, err, "Unable to load ASG for rolling upgrade")
		r.finishExecution(StatusError, 0, ctx, ruObj)
		return
	}

	// Update the CR with some basic info before staring the restack.
	ruObj.Status.StartTime = time.Now().Format(time.RFC3339)
	ruObj.Status.CurrentStatus = StatusRunning
	ruObj.Status.NodesProcessed = 0
	ruObj.Status.TotalNodes = len(asg.Instances)

	if err := r.Status().Update(*ctx, ruObj); err != nil {
		r.error(ruObj, err, "failed to update status")
	}

	// Run the restack that actually performs the rolling update.
	nodesProcessed, err := r.runRestack(ctx, ruObj, KubeCtlBinary)
	if err != nil {
		r.error(ruObj, err, "Failed to runRestack")
		r.finishExecution(StatusError, nodesProcessed, ctx, ruObj)
		return
	}

	r.finishExecution(StatusComplete, nodesProcessed, ctx, ruObj)
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

// +kubebuilder:rbac:groups=upgrademgr.keikoproj.io,resources=rollingupgrades,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=upgrademgr.keikoproj.io,resources=rollingupgrades/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;patch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=list
// +kubebuilder:rbac:groups=core,resources=events,verbs=create
// +kubebuilder:rbac:groups=core,resources=pods/eviction,verbs=create
// +kubebuilder:rbac:groups=extensions;apps,resources=daemonsets;replicasets;statefulsets,verbs=get
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get

// Reconcile reads that state of the cluster for a RollingUpgrade object and makes changes based on the state read
// and the details in the RollingUpgrade.Spec
func (r *RollingUpgradeReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()

	// Fetch the RollingUpgrade instance
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{}
	err := r.Get(ctx, req.NamespacedName, ruObj)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Object not found, return. Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			r.admissionMap.Delete(req.Name)
			r.info(ruObj, "Deleted object from map", "name", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	// If the resource is being deleted, remove it from the admissionMap
	if !ruObj.DeletionTimestamp.IsZero() {
		r.info(ruObj, "Object is being deleted. No more processing")
		r.admissionMap.Delete(ruObj.Name)
		r.ruObjNameToASG.Delete(ruObj.Name)
		r.info(ruObj, "Deleted object from admission map")
		return reconcile.Result{}, nil
	}

	// Setting default values for the Strategy in rollup object
	r.setDefaultsForRollingUpdateStrategy(ruObj)
	r.info(ruObj, "Default strategy settings applied.", "updateStrategy", ruObj.Spec.Strategy)

	err = r.validateRollingUpgradeObj(ruObj)
	if err != nil {
		r.error(ruObj, err, "Validation failed")
		return reconcile.Result{}, err
	}

	result, ok := r.admissionMap.Load(ruObj.Name)
	if ok {
		if result == "processing" {
			r.info(ruObj, "Found obj in map:", "name", ruObj.Name)
			r.info(ruObj, "Object already being processed", "name", ruObj.Name)
		} else {
			r.info(ruObj, "Sync map with invalid entry for ", "name", ruObj.Name)
		}
	} else {
		r.info(ruObj, "Adding obj to map: ", "name", ruObj.Name)
		r.admissionMap.Store(ruObj.Name, "processing")
		go r.Process(&ctx, ruObj)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager creates a new manager.
func (r *RollingUpgradeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.generatedClient = kubernetes.NewForConfigOrDie(mgr.GetConfig())
	return ctrl.NewControllerManagedBy(mgr).
		For(&upgrademgrv1alpha1.RollingUpgrade{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.maxParallel}).
		Complete(r)
}

func (r *RollingUpgradeReconciler) setStateTag(ruObj *upgrademgrv1alpha1.RollingUpgrade, instanceID string, state string) error {
	r.info(ruObj, "setting instance state", "instanceID", instanceID, "instanceState", state)
	err := tagEC2instance(instanceID, EC2StateTagKey, state, r.EC2Client)
	if err != nil {
		return err
	}
	return nil
}

// validateRollingUpgradeObj validates rollup object for the type, maxUnavailable and drainTimeout
func (r *RollingUpgradeReconciler) validateRollingUpgradeObj(ruObj *upgrademgrv1alpha1.RollingUpgrade) error {
	strategy := ruObj.Spec.Strategy

	var nilStrategy = upgrademgrv1alpha1.UpdateStrategy{}
	if strategy == nilStrategy {
		return nil
	}
	// validating the maxUnavailable value
	if strategy.MaxUnavailable.Type == 0 {
		if strategy.MaxUnavailable.IntVal <= 0 {
			err := errors.New(fmt.Sprintf("%s: Invalid value for maxUnavailable - %d",
				ruObj.Name, strategy.MaxUnavailable.IntVal))
			r.error(ruObj, err, "Invalid value for maxUnavailable", "value", strategy.MaxUnavailable.IntVal)
			return err
		}
	} else if strategy.MaxUnavailable.Type == 1 {
		strVallue := strategy.MaxUnavailable.StrVal
		intValue, _ := strconv.Atoi(strings.Trim(strVallue, "%"))
		if intValue <= 0 || intValue > 100 {
			err := errors.New(fmt.Sprintf("%s: Invalid value for maxUnavailable - %s",
				ruObj.Name, strategy.MaxUnavailable.StrVal))
			r.error(ruObj, err, "Invalid value for maxUnavailable", "value", strategy.MaxUnavailable.StrVal)
			return err
		}
	}

	// validating the strategy type
	if strategy.Type != upgrademgrv1alpha1.RandomUpdateStrategy &&
		strategy.Type != upgrademgrv1alpha1.UniformAcrossAzUpdateStrategy {
		err := errors.New(fmt.Sprintf("%s: Invalid value for strategy type - %s", ruObj.Name, strategy.Type))
		r.error(ruObj, err, "Invalid value for strategy type", "value", strategy.Type)
		return err
	}
	return nil
}

// setDefaultsForRollingUpdateStrategy sets the default values for type, maxUnavailable and drainTimeout
func (r *RollingUpgradeReconciler) setDefaultsForRollingUpdateStrategy(ruObj *upgrademgrv1alpha1.RollingUpgrade) {

	// Setting the default values for the update strategy when strategy is not set
	// Default behaviour should be to update one node at a time and should wait for kubectl drain completion
	var nilStrategy = upgrademgrv1alpha1.UpdateStrategy{}
	if ruObj.Spec.Strategy == nilStrategy {
		r.info(ruObj, "Update strategy not set on the rollup object, setting the default strategy.")
		strategy := upgrademgrv1alpha1.UpdateStrategy{
			Type:           upgrademgrv1alpha1.RandomUpdateStrategy,
			Mode:           upgrademgrv1alpha1.UpdateStrategyModeLazy,
			MaxUnavailable: intstr.IntOrString{IntVal: 1},
			DrainTimeout:   -1,
		}
		ruObj.Spec.Strategy = strategy
	} else {
		if ruObj.Spec.Strategy.Type == "" {
			ruObj.Spec.Strategy.Type = upgrademgrv1alpha1.RandomUpdateStrategy
		}
		if ruObj.Spec.Strategy.Mode == "" {
			// default to lazy mode
			ruObj.Spec.Strategy.Mode = upgrademgrv1alpha1.UpdateStrategyModeLazy
		}
		// intstr.IntOrString has the default value 0 with int types
		if ruObj.Spec.Strategy.MaxUnavailable.Type == 0 && ruObj.Spec.Strategy.MaxUnavailable.IntVal == 0 {
			ruObj.Spec.Strategy.MaxUnavailable = intstr.IntOrString{Type: 0, IntVal: 1}
		}
		if ruObj.Spec.Strategy.DrainTimeout == 0 {
			ruObj.Spec.Strategy.DrainTimeout = -1
		}
	}
}

type UpdateInstancesError struct {
	InstanceUpdateErrors []error
}

func (error UpdateInstancesError) Error() string {
	return fmt.Sprintf("Error updating instances, ErrorCount: %d, Errors: %v",
		len(error.InstanceUpdateErrors), error.InstanceUpdateErrors)
}

func NewUpdateInstancesError(instanceUpdateErrors []error) *UpdateInstancesError {
	return &UpdateInstancesError{InstanceUpdateErrors: instanceUpdateErrors}
}

func (r *RollingUpgradeReconciler) UpdateInstances(ctx *context.Context,
	ruObj *upgrademgrv1alpha1.RollingUpgrade,
	instances []*autoscaling.Instance,
	launchDefinition *launchDefinition,
	KubeCtlCall string) error {

	totalNodes := len(instances)
	if totalNodes == 0 {
		return nil
	}

	ch := make(chan error)

	for _, instance := range instances {
		// log it before we start updating the instance
		r.createK8sV1Event(ruObj, EventReasonRUInstanceStarted, EventLevelNormal, map[string]string{
			"status":   "in-progress",
			"asgName":  ruObj.Spec.AsgName,
			"strategy": string(ruObj.Spec.Strategy.Type),
			"msg":      fmt.Sprintf("Started Updating Instance %s, in AZ: %s", *instance.InstanceId, *instance.AvailabilityZone),
		})
		go r.UpdateInstance(ctx, ruObj, instance, launchDefinition, KubeCtlCall, ch)
	}

	// wait for upgrades to complete
	nodesProcessed := 0
	var instanceUpdateErrors []error

	for err := range ch {
		nodesProcessed++
		switch err {
		case nil:
			// do nothing
		default:
			instanceUpdateErrors = append(instanceUpdateErrors, err)
		}
		// log the event
		r.createK8sV1Event(ruObj, EventReasonRUInstanceFinished, EventLevelNormal, map[string]string{
			"status":   "in-progress",
			"asgName":  ruObj.Spec.AsgName,
			"strategy": string(ruObj.Spec.Strategy.Type),
			"msg":      fmt.Sprintf("Finished Updating Instance %d/%d (Errors=%d)", nodesProcessed, totalNodes, len(instanceUpdateErrors)),
		})
		// break if we are done with all the nodes
		if nodesProcessed == totalNodes {
			break
		}
	}

	if len(instanceUpdateErrors) > 0 {
		return NewUpdateInstancesError(instanceUpdateErrors)
	}
	return nil
}

func (r *RollingUpgradeReconciler) UpdateInstanceEager(
	ruObj *upgrademgrv1alpha1.RollingUpgrade,
	nodeName,
	targetInstanceID,
	KubeCtlCall string,
	ch chan error) {

	// Set instance to standby
	err := r.SetStandby(ruObj, targetInstanceID)
	if err != nil {
		ch <- err
		return
	}

	// Wait for new instance to be created
	err = r.WaitForDesiredInstances(ruObj)
	if err != nil {
		ch <- err
		return
	}

	// Wait for in-service nodes to be ready and match desired
	err = r.WaitForDesiredNodes(ruObj)
	if err != nil {
		ch <- err
		return
	}

	// Drain and wait for draining node.
	r.DrainTerminate(ruObj, nodeName, targetInstanceID, KubeCtlCall, ch)

}

func (r *RollingUpgradeReconciler) DrainTerminate(
	ruObj *upgrademgrv1alpha1.RollingUpgrade,
	nodeName,
	targetInstanceID,
	KubeCtlCall string,
	ch chan error) {

	// Drain and wait for draining node.
	err := r.DrainNode(ruObj, nodeName, KubeCtlCall, ruObj.Spec.Strategy.DrainTimeout)
	if err != nil && !ruObj.Spec.IgnoreDrainFailures {
		ch <- err
		return
	}

	// Terminate instance.
	err = r.TerminateNode(ruObj, targetInstanceID)
	if err != nil {
		ch <- err
		return
	}

}

// UpdateInstance runs the rolling upgrade on one instance from an autoscaling group
func (r *RollingUpgradeReconciler) UpdateInstance(ctx *context.Context,
	ruObj *upgrademgrv1alpha1.RollingUpgrade,
	i *autoscaling.Instance,
	launchDefinition *launchDefinition,
	KubeCtlCall string,
	ch chan error) {
	// If an instance was marked as "in-progress" in ClusterState, it has to be marked
	// completed so that it can get considered again in a subsequent rollup CR.
	defer r.ClusterState.markUpdateCompleted(*i.InstanceId)

	// If the running node has the same launchconfig as the asg,
	// there is no need to refresh it.
	targetInstanceID := aws.StringValue(i.InstanceId)
	if !r.requiresRefresh(ruObj, i, launchDefinition) {
		ruObj.Status.NodesProcessed = ruObj.Status.NodesProcessed + 1
		if err := r.Status().Update(*ctx, ruObj); err != nil {
			r.error(ruObj, err, "failed to update status")
		}
		ch <- nil
		return
	}

	nodeName := r.getNodeName(i, r.NodeList, ruObj)
	if nodeName == "" {
		ch <- nil
		return
	}

	// Load the environment variables for scripts to run
	err := loadEnvironmentVariables(ruObj, r.getNodeFromAsg(i, r.NodeList, ruObj))
	if err != nil {
		ch <- err
		return
	}

	// set the EC2 tag indicating the state to in-progress
	err = r.setStateTag(ruObj, targetInstanceID, "in-progress")
	if err != nil {
		ch <- err
		return
	}

	mode := ruObj.Spec.Strategy.Mode.String()
	if strings.ToLower(mode) == upgrademgrv1alpha1.UpdateStrategyModeEager.String() {
		r.info(ruObj, "starting replacement with eager mode", "mode", mode)
		r.UpdateInstanceEager(ruObj, nodeName, targetInstanceID, KubeCtlCall, ch)
	} else if strings.ToLower(mode) == upgrademgrv1alpha1.UpdateStrategyModeLazy.String() {
		r.info(ruObj, "starting replacement with lazy mode", "mode", mode)
		r.DrainTerminate(ruObj, nodeName, targetInstanceID, KubeCtlCall, ch)
	}

	unjoined, err := r.WaitForTermination(ruObj, nodeName, r.generatedClient.CoreV1().Nodes())
	if err != nil {
		ch <- err
		return
	}

	if !unjoined {
		r.info(ruObj, "termination waiter completed but node is still joined, will proceed with upgrade", "nodeName", nodeName)
	}

	// set the EC2 tag indicating the state to completed
	err = r.setStateTag(ruObj, targetInstanceID, "completed")
	if err != nil {
		ch <- err
		return
	}

	ruObj.Status.NodesProcessed = ruObj.Status.NodesProcessed + 1
	if err := r.Status().Update(*ctx, ruObj); err != nil {
		r.error(ruObj, err, "failed to update status")
	}

	ch <- nil
}

func (r *RollingUpgradeReconciler) getNodeCreationTimestamp(ec2Instance *autoscaling.Instance) (bool, time.Time) {
	for _, node := range r.NodeList.Items {
		tokens := strings.Split(node.Spec.ProviderID, "/")
		instanceID := tokens[len(tokens)-1]
		if instanceID == aws.StringValue(ec2Instance.InstanceId) {
			return true, node.ObjectMeta.CreationTimestamp.Time
		}
	}
	return false, time.Time{}
}

func (r *RollingUpgradeReconciler) requiresRefresh(ruObj *upgrademgrv1alpha1.RollingUpgrade, ec2Instance *autoscaling.Instance,
	definition *launchDefinition) bool {

	if ruObj.Spec.ForceRefresh {
		if ok, nodeCreationTS := r.getNodeCreationTimestamp(ec2Instance); ok {
			if nodeCreationTS.Before(ruObj.CreationTimestamp.Time) {
				r.info(ruObj, "rolling upgrade configured for forced refresh")
				return true
			}
		}

		r.info(ruObj, "node", aws.StringValue(ec2Instance.InstanceId), "created after rollingupgrade object. Ignoring forceRefresh")
		return false
	}
	if definition.launchConfigurationName != nil {
		if *(definition.launchConfigurationName) != aws.StringValue(ec2Instance.LaunchConfigurationName) {
			r.info(ruObj, "launch configuration name differs")
			return true
		}
	} else if definition.launchTemplate != nil && ec2Instance.LaunchTemplate != nil {
		instanceLaunchTemplate := ec2Instance.LaunchTemplate
		targetLaunchTemplate := definition.launchTemplate
		if aws.StringValue(instanceLaunchTemplate.LaunchTemplateId) != aws.StringValue(targetLaunchTemplate.LaunchTemplateId) {
			r.info(ruObj, "launch template id differs")
			return true
		}
		if aws.StringValue(instanceLaunchTemplate.LaunchTemplateName) != aws.StringValue(targetLaunchTemplate.LaunchTemplateName) {
			r.info(ruObj, "launch template name differs")
			return true
		}
		if aws.StringValue(instanceLaunchTemplate.Version) != aws.StringValue(targetLaunchTemplate.Version) {
			r.info(ruObj, "launch template version differs")
			return true
		}
	}

	r.info(ruObj, "node refresh not required")
	return false
}

// logger creates logger for rolling upgrade.
func (r *RollingUpgradeReconciler) logger(ruObj *upgrademgrv1alpha1.RollingUpgrade) logr.Logger {
	return r.Log.WithValues("rollingupgrade", ruObj.Name)
}

// info logs message with Info level for the specified rolling upgrade.
func (r *RollingUpgradeReconciler) info(ruObj *upgrademgrv1alpha1.RollingUpgrade, msg string, keysAndValues ...interface{}) {
	r.logger(ruObj).Info(msg, keysAndValues...)
}

// error logs message with Error level for the specified rolling upgrade.
func (r *RollingUpgradeReconciler) error(ruObj *upgrademgrv1alpha1.RollingUpgrade, err error, msg string, keysAndValues ...interface{}) {
	r.logger(ruObj).Error(err, msg, keysAndValues...)
}
