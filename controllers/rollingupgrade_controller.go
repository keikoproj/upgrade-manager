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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/autoscaling/autoscalingiface"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/go-logr/logr"
	"github.com/keikoproj/aws-sdk-go-cache/cache"
	iebackoff "github.com/keikoproj/inverse-exp-backoff"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/keikoproj/upgrade-manager/api/v1alpha1"
	upgrademgrv1alpha1 "github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"github.com/keikoproj/upgrade-manager/controllers/common"
)

const (
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

	// InService is a state of an instance
	InService = "InService"
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
	// CacheTTL is ttl for ASG cache.
	CacheTTL = 30 * time.Second
)

// RollingUpgradeReconciler reconciles a RollingUpgrade object
type RollingUpgradeReconciler struct {
	client.Client
	Log             logr.Logger
	EC2Client       ec2iface.EC2API
	ASGClient       autoscalingiface.AutoScalingAPI
	generatedClient *kubernetes.Clientset
	NodeList        *corev1.NodeList
	LaunchTemplates []*ec2.LaunchTemplate
	inProcessASGs   sync.Map
	admissionMap    sync.Map
	ruObjNameToASG  AsgCache
	ClusterState    ClusterState
	maxParallel     int
	CacheConfig     *cache.Config
	ScriptRunner    ScriptRunner
}

type AsgCache struct {
	cache sync.Map
}

func (c *AsgCache) IsExpired(name string) bool {
	if val, ok := c.cache.Load(name); !ok {
		return true
	} else {
		cached := val.(CachedValue)
		return time.Now().After(cached.expiration)
	}
}

func (c *AsgCache) Load(name string) (*autoscaling.Group, bool) {
	if val, ok := c.cache.Load(name); !ok {
		return nil, false
	} else {
		cached := val.(CachedValue)
		return cached.val.(*autoscaling.Group), true
	}
}

func (c *AsgCache) Store(name string, asg *autoscaling.Group) {
	c.cache.Store(name, CachedValue{asg, time.Now().Add(CacheTTL)})
}

func (c *AsgCache) Delete(name string) {
	c.cache.Delete(name)
}

type CachedValue struct {
	val        interface{}
	expiration time.Time
}

func (r *RollingUpgradeReconciler) SetMaxParallel(max int) {
	if max >= 1 {
		r.Log.Info(fmt.Sprintf("max parallel reconciles = %v", max))
		r.maxParallel = max
	}
}

func (r *RollingUpgradeReconciler) preDrainHelper(instanceID, nodeName string, ruObj *upgrademgrv1alpha1.RollingUpgrade) error {
	return r.ScriptRunner.PreDrain(instanceID, nodeName, ruObj)
}

// Operates on any scripts that were provided after the draining of the node.
// kubeCtlCall is provided as an argument to decouple the method from the actual kubectl call
func (r *RollingUpgradeReconciler) postDrainHelper(instanceID,
	nodeName string,
	ruObj *upgrademgrv1alpha1.RollingUpgrade,
	nodeSteps map[string][]v1alpha1.NodeStepDuration,
	inProcessingNodes map[string]*v1alpha1.NodeInProcessing,
	mutex *sync.Mutex) error {
	err := r.ScriptRunner.PostDrain(instanceID, nodeName, ruObj)
	if err != nil {
		return err
	}

	r.info(ruObj, "Waiting for postDrainDelay", "postDrainDelay", ruObj.Spec.PostDrainDelaySeconds)
	time.Sleep(time.Duration(ruObj.Spec.PostDrainDelaySeconds) * time.Second)

	ruObj.Status.NodeStep(inProcessingNodes, nodeSteps, ruObj.Spec.AsgName, nodeName, v1alpha1.NodeRotationPostWait, mutex)

	return r.ScriptRunner.PostWait(instanceID, nodeName, ruObj)

}

// DrainNode runs "kubectl drain" on the given node
// kubeCtlCall is provided as an argument to decouple the method from the actual kubectl call
func (r *RollingUpgradeReconciler) DrainNode(ruObj *upgrademgrv1alpha1.RollingUpgrade,
	nodeName string,
	instanceID string,
	drainTimeout int,
	nodeSteps map[string][]v1alpha1.NodeStepDuration,
	inProcessingNodes map[string]*v1alpha1.NodeInProcessing,
	mutex *sync.Mutex) error {

	ruObj.Status.NodeStep(inProcessingNodes, nodeSteps, ruObj.Spec.AsgName, nodeName, v1alpha1.NodeRotationPredrainScript, mutex)

	// Running kubectl drain node.
	err := r.preDrainHelper(instanceID, nodeName, ruObj)
	if err != nil {
		return fmt.Errorf("%s: pre-drain script failed: %w", ruObj.NamespacedName(), err)
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

	ruObj.Status.NodeStep(inProcessingNodes, nodeSteps, ruObj.Spec.AsgName, nodeName, v1alpha1.NodeRotationDrain, mutex)

	r.info(ruObj, "Invoking kubectl drain for the node", "nodeName", nodeName)
	go r.CallKubectlDrain(nodeName, ruObj, errChan)

	// Listening to signals from the CallKubectlDrain go routine
	select {
	case <-ctx.Done():
		r.error(ruObj, ctx.Err(), "Kubectl drain timed out for node", "nodeName", nodeName)
		if !ruObj.Spec.IgnoreDrainFailures {
			return ctx.Err()
		}
	case err := <-errChan:
		if err != nil && !ruObj.Spec.IgnoreDrainFailures {
			r.error(ruObj, err, "Kubectl drain errored for node", "nodeName", nodeName)
			return err
		}
		r.info(ruObj, "Kubectl drain completed for node", "nodeName", nodeName)
	}

	ruObj.Status.NodeStep(inProcessingNodes, nodeSteps, ruObj.Spec.AsgName, nodeName, v1alpha1.NodeRotationPostdrainScript, mutex)

	return r.postDrainHelper(instanceID, nodeName, ruObj, nodeSteps, inProcessingNodes, mutex)
}

// CallKubectlDrain runs the "kubectl drain" for a given node
// Node will be terminated even if pod eviction is not completed when the drain timeout is exceeded
func (r *RollingUpgradeReconciler) CallKubectlDrain(nodeName string, ruObj *upgrademgrv1alpha1.RollingUpgrade, errChan chan error) {
	out, err := r.ScriptRunner.drainNode(nodeName, ruObj)
	if err != nil {
		if strings.Contains(out, "Error from server (NotFound): nodes") {
			r.error(ruObj, err, "Not executing postDrainHelper. Node not found.", "output", out)
			errChan <- nil
			return
		}
		errChan <- fmt.Errorf("%s failed to drain: %w", ruObj.NamespacedName(), err)
		return
	}
	errChan <- nil
}

func (r *RollingUpgradeReconciler) WaitForDesiredInstances(ruObj *upgrademgrv1alpha1.RollingUpgrade) error {
	var err error
	var ieb *iebackoff.IEBackoff
	for ieb, err = iebackoff.NewIEBackoff(WaiterMaxDelay, WaiterMinDelay, 0.5, WaiterMaxAttempts); err == nil; err = ieb.Next() {
		err = r.populateAsg(ruObj, false)
		if err != nil {
			return err
		}

		asg, err := r.GetAutoScalingGroup(ruObj.NamespacedName())
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
	return fmt.Errorf("%s: WaitForDesiredInstances timed out while waiting for instance to be added: %w", ruObj.NamespacedName(), err)
}

// we put old instances in standby and then wait for new instances to be InService so that desired instances is met
func (r *RollingUpgradeReconciler) WaitForDesiredNodes(ruObj *upgrademgrv1alpha1.RollingUpgrade) error {
	var err error
	var ieb *iebackoff.IEBackoff
	for ieb, err = iebackoff.NewIEBackoff(WaiterMaxDelay, WaiterMinDelay, 0.5, WaiterMaxAttempts); err == nil; err = ieb.Next() {
		err = r.populateAsg(ruObj, false)
		if err != nil {
			return err
		}

		err = r.populateNodeList(ruObj, r.generatedClient.CoreV1().Nodes())
		if err != nil {
			r.error(ruObj, err, "unable to populate node list")
		}

		asg, err := r.GetAutoScalingGroup(ruObj.NamespacedName())
		if err != nil {
			return fmt.Errorf("Unable to load ASG with name: %s", ruObj.Name)
		}

		// get list of inService instance IDs
		inServiceInstanceIds := getInServiceIds(asg.Instances)
		desiredCapacity := aws.Int64Value(asg.DesiredCapacity)

		// check all asg instances are ready nodes
		var foundCount int64 = 0
		for _, node := range r.NodeList.Items {
			if contains(inServiceInstanceIds, r.instanceId(node)) && isNodeReady(node) && isNodePassingReadinessGates(node, ruObj.Spec.ReadinessGates) {
				foundCount++
			}
		}

		if foundCount == desiredCapacity {
			r.info(ruObj, "desired capacity is met", "inServiceCount", foundCount)
			return nil
		}

		r.info(ruObj, "new node is not yet ready")
	}
	return fmt.Errorf("%s: WaitForDesiredNodes timed out while waiting for nodes to join: %w", ruObj.NamespacedName(), err)
}

// read aws instance id from nodes spec.providerID
func (r *RollingUpgradeReconciler) instanceId(node corev1.Node) string {
	tokens := strings.Split(node.Spec.ProviderID, "/")
	return tokens[len(tokens)-1]
}

func (r *RollingUpgradeReconciler) WaitForTermination(ruObj *upgrademgrv1alpha1.RollingUpgrade, nodeName string, nodeInterface v1.NodeInterface) (bool, error) {
	if nodeName == "" {
		return true, nil
	}

	started := time.Now()
	for {
		if time.Since(started) >= (time.Second * time.Duration(TerminationTimeoutSeconds)) {
			r.info(ruObj, "WaitForTermination timed out while waiting for node to unjoin")
			return false, nil
		}

		_, err := nodeInterface.Get(nodeName, metav1.GetOptions{})
		if k8serrors.IsNotFound(err) {
			r.info(ruObj, "node is unjoined from cluster, upgrade will proceed", "nodeName", nodeName)
			break
		}

		r.info(ruObj, "node is still joined to cluster, will wait and retry",
			"nodeName", nodeName, "terminationSleepIntervalSeconds", TerminationSleepIntervalSeconds)

		time.Sleep(time.Duration(TerminationSleepIntervalSeconds) * time.Second)
	}
	return true, nil
}

func (r *RollingUpgradeReconciler) GetAutoScalingGroup(namespacedName string) (*autoscaling.Group, error) {
	val, ok := r.ruObjNameToASG.Load(namespacedName)
	if !ok {
		return &autoscaling.Group{}, fmt.Errorf("Unable to load ASG with name: %s", namespacedName)
	}
	return val, nil
}

// SetStandby sets the autoscaling instance to standby mode.
func (r *RollingUpgradeReconciler) SetStandby(ruObj *upgrademgrv1alpha1.RollingUpgrade, instanceID string) error {
	r.info(ruObj, "Setting to stand-by", ruObj.Name, instanceID)

	asg, err := r.GetAutoScalingGroup(ruObj.NamespacedName())
	if err != nil {
		return err
	}

	instanceState, err := getInstanceStateInASG(asg, instanceID)
	if err != nil {
		r.info(ruObj, fmt.Sprintf("WARNING: %v", err))
		return nil
	}

	if instanceState == autoscaling.LifecycleStateStandby {
		return nil
	}

	if !isInServiceLifecycleState(instanceState) {
		r.info(ruObj, "Cannot set instance to stand-by, instance is in state", "instanceState", instanceState, "instanceID", instanceID)
		return nil
	}

	input := &autoscaling.EnterStandbyInput{
		AutoScalingGroupName:           aws.String(ruObj.Spec.AsgName),
		InstanceIds:                    aws.StringSlice([]string{instanceID}),
		ShouldDecrementDesiredCapacity: aws.Bool(false),
	}

	_, err = r.ASGClient.EnterStandby(input)
	if err != nil {
		r.error(ruObj, err, "Failed to enter standby", "instanceID", instanceID)
	}

	// we modified the asg, so our cache is now invalid
	err = r.populateAsg(ruObj, true)
	if err != nil {
		return err
	}

	return nil
}

// TerminateNode actually terminates the given node.
func (r *RollingUpgradeReconciler) TerminateNode(ruObj *upgrademgrv1alpha1.RollingUpgrade,
	instanceID string,
	nodeName string,
	nodeSteps map[string][]v1alpha1.NodeStepDuration,
	inProcessingNodes map[string]*v1alpha1.NodeInProcessing,
	mutex *sync.Mutex) error {

	input := &autoscaling.TerminateInstanceInAutoScalingGroupInput{
		InstanceId:                     aws.String(instanceID),
		ShouldDecrementDesiredCapacity: aws.Bool(false),
	}
	ruObj.Status.NodeStep(inProcessingNodes, nodeSteps, ruObj.Spec.AsgName, nodeName, v1alpha1.NodeRotationTerminate, mutex)

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
	ruObj.Status.NodeStep(inProcessingNodes, nodeSteps, ruObj.Spec.AsgName, nodeName, v1alpha1.NodeRotationPostTerminate, mutex)
	return r.ScriptRunner.PostTerminate(instanceID, nodeName, ruObj)
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

func (r *RollingUpgradeReconciler) populateAsg(ruObj *upgrademgrv1alpha1.RollingUpgrade, force bool) error {
	// if value is still in cache, do nothing.
	if !r.ruObjNameToASG.IsExpired(ruObj.NamespacedName()) && !force {
		return nil
	}

	input := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{
			aws.String(ruObj.Spec.AsgName),
		},
	}
	result, err := r.ASGClient.DescribeAutoScalingGroups(input)
	if err != nil {
		r.error(ruObj, err, "Failed to describe autoscaling group")
		return fmt.Errorf("%s: failed to describe autoscaling group: %w", ruObj.NamespacedName(), err)
	}

	if len(result.AutoScalingGroups) == 0 {
		r.info(ruObj, "%s: No ASG found with name %s!\n", ruObj.Name, ruObj.Spec.AsgName)
		return fmt.Errorf("%s: no ASG found", ruObj.NamespacedName())
	} else if len(result.AutoScalingGroups) > 1 {
		r.info(ruObj, "%s: Too many asgs found with name %d!\n", ruObj.Name, len(result.AutoScalingGroups))
		return fmt.Errorf("%s: Too many ASGs: %d", ruObj.NamespacedName(), len(result.AutoScalingGroups))
	}

	asg := result.AutoScalingGroups[0]
	r.ruObjNameToASG.Store(ruObj.NamespacedName(), asg)

	return nil
}

func (r *RollingUpgradeReconciler) populateLaunchTemplates(ruObj *upgrademgrv1alpha1.RollingUpgrade) error {
	launchTemplates := []*ec2.LaunchTemplate{}
	err := r.EC2Client.DescribeLaunchTemplatesPages(&ec2.DescribeLaunchTemplatesInput{}, func(page *ec2.DescribeLaunchTemplatesOutput, lastPage bool) bool {
		launchTemplates = append(launchTemplates, page.LaunchTemplates...)
		return page.NextToken != nil
	})
	if err != nil {
		r.error(ruObj, err, "Failed to populate launch template list")
		return fmt.Errorf("failed to populate launch template list for %s: %w", ruObj.NamespacedName(), err)
	}
	r.LaunchTemplates = launchTemplates
	return nil
}

// store all nodes in a cache to avoid fetching them multiple times
func (r *RollingUpgradeReconciler) populateNodeList(ruObj *upgrademgrv1alpha1.RollingUpgrade, nodeInterface v1.NodeInterface) error {
	nodeList, err := nodeInterface.List(metav1.ListOptions{})
	if err != nil {
		msg := "Failed to get all nodes in the cluster: " + err.Error()
		r.info(ruObj, msg)
		return fmt.Errorf("%s: Failed to get all nodes in the cluster: %w", ruObj.NamespacedName(), err)
	}
	r.NodeList = nodeList
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

// runRestack performs rollout of new nodes.
// returns number of processed instances and optional error.
func (r *RollingUpgradeReconciler) runRestack(ctx *context.Context, ruObj *upgrademgrv1alpha1.RollingUpgrade) (int, error) {
	err := r.populateAsg(ruObj, false)
	if err != nil {
		return 0, fmt.Errorf("%s: Unable to populate the ASG object: %w", ruObj.NamespacedName(), err)
	}

	asg, err := r.GetAutoScalingGroup(ruObj.NamespacedName())
	if err != nil {
		return 0, fmt.Errorf("Unable to load ASG with name: %s", ruObj.Name)
	}

	r.info(ruObj, "Nodes in ASG that *might* need to be updated", "asgName", *asg.AutoScalingGroupName, "asgSize", len(asg.Instances))

	totalNodes := len(asg.Instances)
	// No further processing is required if ASG doesn't have an instance running
	if totalNodes == 0 {
		r.info(ruObj, fmt.Sprintf("Total nodes needing update for %s is 0. Restack complete.", *asg.AutoScalingGroupName))
		return 0, nil
	}

	nodeSelector := getNodeSelector(asg, ruObj)

	r.inProcessASGs.Store(*asg.AutoScalingGroupName, "running")
	r.ClusterState.initializeAsg(*asg.AutoScalingGroupName, asg.Instances)
	defer r.ClusterState.deleteAllInstancesInAsg(*asg.AutoScalingGroupName)

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
			return processedInstances, fmt.Errorf(errorMessage)
		}

		// update the instances
		err := r.UpdateInstances(ctx, ruObj, instances, launchDefinition)
		processedInstances += len(instances)
		if err != nil {
			return processedInstances, err
		}
	}
	return processedInstances, nil
}

func (r *RollingUpgradeReconciler) finishExecution(err error, nodesProcessed int, ctx *context.Context, ruObj *upgrademgrv1alpha1.RollingUpgrade) {
	var level string
	var finalStatus string

	if err == nil {
		finalStatus = upgrademgrv1alpha1.StatusComplete
		level = EventLevelNormal
		r.info(ruObj, "Marked object as", "finalStatus", finalStatus)
		common.SetMetricRollupCompleted(ruObj.Name)
	} else {
		finalStatus = upgrademgrv1alpha1.StatusError
		level = EventLevelWarning
		r.error(ruObj, err, "Marked object as", "finalStatus", finalStatus)
		common.SetMetricRollupFailed(ruObj.Name)
	}

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

	r.createK8sV1Event(ruObj, EventReasonRUFinished, level, map[string]string{
		"status":   finalStatus,
		"asgName":  ruObj.Spec.AsgName,
		"strategy": string(ruObj.Spec.Strategy.Type),
		"info":     fmt.Sprintf("Rolling Upgrade as finished (status=%s)", finalStatus),
	})

	MarkObjForCleanup(ruObj)
	if err := r.Status().Update(*ctx, ruObj); err != nil {
		// Check if the err is "StorageError: invalid object". If so, the object was deleted...
		if strings.Contains(err.Error(), "StorageError: invalid object") {
			r.info(ruObj, "Object most likely deleted")
		} else {
			r.error(ruObj, err, "failed to update status")
		}
	}

	r.ClusterState.deleteAllInstancesInAsg(ruObj.Spec.AsgName)
	r.info(ruObj, "Deleted the entries of ASG in the cluster store", "asgName", ruObj.Spec.AsgName)
	r.inProcessASGs.Delete(ruObj.Spec.AsgName)
	r.admissionMap.Delete(ruObj.NamespacedName())
	r.info(ruObj, "Deleted from admission map", "admissionMap", &r.admissionMap)
}

// Process actually performs the ec2-instance restacking.
func (r *RollingUpgradeReconciler) Process(ctx *context.Context,
	ruObj *upgrademgrv1alpha1.RollingUpgrade) {

	if ruObj.Status.CurrentStatus == upgrademgrv1alpha1.StatusComplete ||
		ruObj.Status.CurrentStatus == upgrademgrv1alpha1.StatusError {
		r.info(ruObj, "No more processing", "currentStatus", ruObj.Status.CurrentStatus)

		if exists := ruObj.ObjectMeta.Annotations[JanitorAnnotation]; exists == "" {
			r.info(ruObj, "Marking object for deletion")
			MarkObjForCleanup(ruObj)
		}

		r.admissionMap.Delete(ruObj.NamespacedName())
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
	err := r.populateAsg(ruObj, false)
	if err != nil {
		r.finishExecution(err, 0, ctx, ruObj)
		return
	}

	//TODO(shri): Ensure that no node is Unschedulable at this time.
	err = r.populateNodeList(ruObj, r.generatedClient.CoreV1().Nodes())
	if err != nil {
		r.finishExecution(err, 0, ctx, ruObj)
		return
	}

	if err := r.populateLaunchTemplates(ruObj); err != nil {
		r.finishExecution(err, 0, ctx, ruObj)
		return
	}

	asg, err := r.GetAutoScalingGroup(ruObj.NamespacedName())
	if err != nil {
		r.error(ruObj, err, "Unable to load ASG for rolling upgrade")
		r.finishExecution(err, 0, ctx, ruObj)
		return
	}

	// Update the CR with some basic info before staring the restack.
	ruObj.Status.StartTime = time.Now().Format(time.RFC3339)
	ruObj.Status.CurrentStatus = upgrademgrv1alpha1.StatusRunning
	ruObj.Status.NodesProcessed = 0
	ruObj.Status.TotalNodes = len(asg.Instances)
	common.SetMetricRollupInitOrRunning(ruObj.Name)

	if err := r.Status().Update(*ctx, ruObj); err != nil {
		r.error(ruObj, err, "failed to update status")
	}

	// Run the restack that actually performs the rolling update.
	nodesProcessed, err := r.runRestack(ctx, ruObj)
	if err != nil {
		r.error(ruObj, err, "Failed to runRestack")
		r.finishExecution(err, nodesProcessed, ctx, ruObj)
		return
	}

	//Validation step: check if all the nodes have the latest launchconfig.
	r.info(ruObj, "Validating the launch definition of nodes and ASG")
	if err := r.validateNodesLaunchDefinition(ruObj); err != nil {
		r.error(ruObj, err, "Launch definition validation failed")
		r.finishExecution(err, nodesProcessed, ctx, ruObj)
		return
	}

	// no error -> report success
	r.finishExecution(nil, nodesProcessed, ctx, ruObj)
}

//Check if ec2Instances and the ASG have same launch config.
func (r *RollingUpgradeReconciler) validateNodesLaunchDefinition(ruObj *upgrademgrv1alpha1.RollingUpgrade) error {
	//Get ASG launch config
	var err error
	err = r.populateAsg(ruObj, false)
	if err != nil {
		return fmt.Errorf("%s: Unable to populate the ASG object: %w", ruObj.NamespacedName(), err)
	}
	asg, err := r.GetAutoScalingGroup(ruObj.NamespacedName())
	if err != nil {
		return fmt.Errorf("%s: Unable to load ASG with name: %w", ruObj.NamespacedName(), err)
	}
	launchDefinition := NewLaunchDefinition(asg)
	launchConfigASG, launchTemplateASG := launchDefinition.launchConfigurationName, launchDefinition.launchTemplate

	//Get ec2 instances and their launch configs.
	ec2instances := asg.Instances
	for _, ec2Instance := range ec2instances {
		ec2InstanceID, ec2InstanceLaunchConfig, ec2InstanceLaunchTemplate := ec2Instance.InstanceId, ec2Instance.LaunchConfigurationName, ec2Instance.LaunchTemplate
		if aws.StringValue(ec2Instance.LifecycleState) != InService {
			continue
		}
		if aws.StringValue(launchConfigASG) != aws.StringValue(ec2InstanceLaunchConfig) {
			return fmt.Errorf("launch config mismatch, %s instance config - %s, does not match the asg config", aws.StringValue(ec2InstanceID), aws.StringValue(ec2InstanceLaunchConfig))
		} else if launchTemplateASG != nil && ec2InstanceLaunchTemplate != nil {
			if aws.StringValue(launchTemplateASG.LaunchTemplateId) != aws.StringValue(ec2InstanceLaunchTemplate.LaunchTemplateId) {
				return fmt.Errorf("launch template mismatch, %s instance template - %s, does not match the asg template", aws.StringValue(ec2InstanceID), aws.StringValue(ec2InstanceLaunchTemplate.LaunchTemplateId))
			}
		}
	}
	return nil
}

// MarkObjForCleanup sets the annotation on the given object for deletion.
func MarkObjForCleanup(ruObj *upgrademgrv1alpha1.RollingUpgrade) {
	if ruObj.ObjectMeta.Annotations == nil {
		ruObj.ObjectMeta.Annotations = map[string]string{}
	}

	switch ruObj.Status.CurrentStatus {
	case upgrademgrv1alpha1.StatusComplete:
		ruObj.ObjectMeta.Annotations[JanitorAnnotation] = ClearCompletedFrequency
	case upgrademgrv1alpha1.StatusError:
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
			r.admissionMap.Delete(req.NamespacedName)
			r.info(ruObj, "Deleted object from map", "name", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	// If the resource is being deleted, remove it from the admissionMap
	if !ruObj.DeletionTimestamp.IsZero() {
		r.info(ruObj, "Object is being deleted. No more processing")
		r.admissionMap.Delete(ruObj.NamespacedName())
		r.ruObjNameToASG.Delete(ruObj.NamespacedName())
		r.info(ruObj, "Deleted object from admission map")
		return reconcile.Result{}, nil
	}

	// set the state of instances in the ASG to new in the cluster store
	_, exists := r.inProcessASGs.Load(ruObj.Spec.AsgName)
	if exists {
		r.info(ruObj, "ASG "+ruObj.Spec.AsgName+" is being processed. Requeuing")
		return reconcile.Result{Requeue: true, RequeueAfter: time.Duration(60) * time.Second}, nil
	}

	// Setting default values for the Strategy in rollup object
	r.setDefaultsForRollingUpdateStrategy(ruObj)
	r.info(ruObj, "Default strategy settings applied.", "updateStrategy", ruObj.Spec.Strategy)

	err = r.validateRollingUpgradeObj(ruObj)
	if err != nil {
		r.error(ruObj, err, "Validation failed")
		return reconcile.Result{}, err
	}

	result, ok := r.admissionMap.Load(ruObj.NamespacedName())
	if ok {
		if result == "processing" {
			r.info(ruObj, "Found obj in map:", "name", ruObj.NamespacedName())
			r.info(ruObj, "Object already being processed", "name", ruObj.NamespacedName())
		} else {
			r.info(ruObj, "Sync map with invalid entry for ", "name", ruObj.NamespacedName())
		}
	} else {
		r.info(ruObj, "Adding obj to map: ", "name", ruObj.NamespacedName())
		r.admissionMap.Store(ruObj.NamespacedName(), "processing")
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
	return tagEC2instance(instanceID, EC2StateTagKey, state, r.EC2Client)
}

// validateRollingUpgradeObj validates rollup object for the type, maxUnavailable and drainTimeout
func (r *RollingUpgradeReconciler) validateRollingUpgradeObj(ruObj *upgrademgrv1alpha1.RollingUpgrade) error {
	strategy := ruObj.Spec.Strategy

	var nilStrategy = upgrademgrv1alpha1.UpdateStrategy{}
	if strategy == nilStrategy {
		return nil
	}
	// validating the maxUnavailable value
	if strategy.MaxUnavailable.Type == intstr.Int {
		if strategy.MaxUnavailable.IntVal <= 0 {
			err := fmt.Errorf("%s: Invalid value for maxUnavailable - %d",
				ruObj.Name, strategy.MaxUnavailable.IntVal)
			r.error(ruObj, err, "Invalid value for maxUnavailable", "value", strategy.MaxUnavailable.IntVal)
			return err
		}
	} else if strategy.MaxUnavailable.Type == intstr.String {
		strVal := strategy.MaxUnavailable.StrVal
		intValue, _ := strconv.Atoi(strings.Trim(strVal, "%"))
		if intValue <= 0 || intValue > 100 {
			err := fmt.Errorf("%s: Invalid value for maxUnavailable - %s",
				ruObj.Name, strategy.MaxUnavailable.StrVal)
			r.error(ruObj, err, "Invalid value for maxUnavailable", "value", strategy.MaxUnavailable.StrVal)
			return err
		}
	}

	// validating the strategy type
	if strategy.Type != upgrademgrv1alpha1.RandomUpdateStrategy &&
		strategy.Type != upgrademgrv1alpha1.UniformAcrossAzUpdateStrategy {
		err := fmt.Errorf("%s: Invalid value for strategy type - %s", ruObj.NamespacedName(), strategy.Type)
		r.error(ruObj, err, "Invalid value for strategy type", "value", strategy.Type)
		return err
	}
	return nil
}

// setDefaultsForRollingUpdateStrategy sets the default values for type, maxUnavailable and drainTimeout
func (r *RollingUpgradeReconciler) setDefaultsForRollingUpdateStrategy(ruObj *upgrademgrv1alpha1.RollingUpgrade) {
	if ruObj.Spec.Strategy.Type == "" {
		ruObj.Spec.Strategy.Type = upgrademgrv1alpha1.RandomUpdateStrategy
	}
	if ruObj.Spec.Strategy.Mode == "" {
		// default to lazy mode
		ruObj.Spec.Strategy.Mode = upgrademgrv1alpha1.UpdateStrategyModeLazy
	}
	// Set default max unavailable to 1.
	if ruObj.Spec.Strategy.MaxUnavailable.Type == intstr.Int && ruObj.Spec.Strategy.MaxUnavailable.IntVal == 0 {
		ruObj.Spec.Strategy.MaxUnavailable.IntVal = 1
	}
	if ruObj.Spec.Strategy.DrainTimeout == 0 {
		ruObj.Spec.Strategy.DrainTimeout = -1
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
	launchDefinition *launchDefinition) error {

	totalNodes := len(instances)
	if totalNodes == 0 {
		return nil
	}

	//A map to retain the steps for multiple nodes
	nodeSteps := make(map[string][]v1alpha1.NodeStepDuration)
	inProcessingNodes := make(map[string]*v1alpha1.NodeInProcessing)
	mutex := &sync.Mutex{}

	ch := make(chan error)

	for _, instance := range instances {
		// log it before we start updating the instance
		r.createK8sV1Event(ruObj, EventReasonRUInstanceStarted, EventLevelNormal, map[string]string{
			"status":   "in-progress",
			"asgName":  ruObj.Spec.AsgName,
			"strategy": string(ruObj.Spec.Strategy.Type),
			"msg":      fmt.Sprintf("Started Updating Instance %s, in AZ: %s", *instance.InstanceId, *instance.AvailabilityZone),
		})
		go r.UpdateInstance(ctx, ruObj, instance, launchDefinition, ch, nodeSteps, inProcessingNodes, mutex)
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

	ruObj.Status.UpdateStatistics(nodeSteps)
	ruObj.Status.UpdateLastBatchNodes(inProcessingNodes)

	if len(instanceUpdateErrors) > 0 {
		return NewUpdateInstancesError(instanceUpdateErrors)
	}
	return nil
}

func (r *RollingUpgradeReconciler) UpdateInstanceEager(
	ruObj *upgrademgrv1alpha1.RollingUpgrade,
	nodeName,
	targetInstanceID string,
	nodeSteps map[string][]v1alpha1.NodeStepDuration,
	inProcessingNodes map[string]*v1alpha1.NodeInProcessing,
	mutex *sync.Mutex) error {

	// Set instance to standby
	err := r.SetStandby(ruObj, targetInstanceID)
	if err != nil {
		return err
	}

	ruObj.Status.NodeStep(inProcessingNodes, nodeSteps, ruObj.Spec.AsgName, nodeName, v1alpha1.NodeRotationDesiredNodeReady, mutex)

	// Wait for new instance to be created
	err = r.WaitForDesiredInstances(ruObj)
	if err != nil {
		return err
	}

	// Wait for in-service nodes to be ready and match desired
	err = r.WaitForDesiredNodes(ruObj)
	if err != nil {
		return err
	}

	// Drain and wait for draining node.
	return r.DrainTerminate(ruObj, nodeName, targetInstanceID, nodeSteps, inProcessingNodes, mutex)
}

func (r *RollingUpgradeReconciler) DrainTerminate(
	ruObj *upgrademgrv1alpha1.RollingUpgrade,
	nodeName,
	targetInstanceID string,
	nodeSteps map[string][]v1alpha1.NodeStepDuration,
	inProcessingNodes map[string]*v1alpha1.NodeInProcessing,
	mutex *sync.Mutex) error {

	// Drain and wait for draining node.
	if nodeName != "" {
		if err := r.DrainNode(ruObj, nodeName, targetInstanceID, ruObj.Spec.Strategy.DrainTimeout, nodeSteps, inProcessingNodes, mutex); err != nil {
			return err
		}
	}

	// Terminate instance.
	err := r.TerminateNode(ruObj, targetInstanceID, nodeName, nodeSteps, inProcessingNodes, mutex)
	if err != nil {
		return err
	}

	ruObj.Status.NodeStep(inProcessingNodes, nodeSteps, ruObj.Spec.AsgName, nodeName, v1alpha1.NodeRotationCompleted, mutex)

	return nil
}

// UpdateInstance runs the rolling upgrade on one instance from an autoscaling group
func (r *RollingUpgradeReconciler) UpdateInstance(ctx *context.Context,
	ruObj *upgrademgrv1alpha1.RollingUpgrade,
	i *autoscaling.Instance,
	launchDefinition *launchDefinition,
	ch chan error,
	nodeSteps map[string][]v1alpha1.NodeStepDuration,
	inProcessingNodes map[string]*v1alpha1.NodeInProcessing,
	mutex *sync.Mutex) {
	targetInstanceID := aws.StringValue(i.InstanceId)
	// If an instance was marked as "in-progress" in ClusterState, it has to be marked
	// completed so that it can get considered again in a subsequent rollup CR.
	defer r.ClusterState.markUpdateCompleted(targetInstanceID)

	// Check if the rollingupgrade object still exists
	_, ok := r.admissionMap.Load(ruObj.NamespacedName())
	if !ok {
		r.info(ruObj, "Object either force completed or deleted. Ignoring node update")
		ruObj.Status.NodesProcessed = ruObj.Status.NodesProcessed + 1
		ch <- nil
		return
	}

	// If the running node has the same launchconfig as the asg,
	// there is no need to refresh it.
	if !r.requiresRefresh(ruObj, i, launchDefinition) {
		ruObj.Status.NodesProcessed = ruObj.Status.NodesProcessed + 1
		if err := r.Status().Update(*ctx, ruObj); err != nil {
			r.error(ruObj, err, "failed to update status")
		}
		ch <- nil
		return
	}

	nodeName := r.getNodeName(i, r.NodeList, ruObj)

	// set the EC2 tag indicating the state to in-progress
	err := r.setStateTag(ruObj, targetInstanceID, "in-progress")
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			if awsErr.Code() == "InvalidInstanceID.NotFound" {
				ch <- nil
				return
			}
		}
		ch <- err
		return
	}

	mode := ruObj.Spec.Strategy.Mode.String()
	if strings.ToLower(mode) == upgrademgrv1alpha1.UpdateStrategyModeEager.String() {
		r.info(ruObj, "starting replacement with eager mode", "mode", mode)
		//Add statistics
		ruObj.Status.NodeStep(inProcessingNodes, nodeSteps, ruObj.Spec.AsgName, nodeName, v1alpha1.NodeRotationKickoff, mutex)
		err = r.UpdateInstanceEager(ruObj, nodeName, targetInstanceID, nodeSteps, inProcessingNodes, mutex)
	} else if strings.ToLower(mode) == upgrademgrv1alpha1.UpdateStrategyModeLazy.String() {
		r.info(ruObj, "starting replacement with lazy mode", "mode", mode)
		//Add statistics
		ruObj.Status.NodeStep(inProcessingNodes, nodeSteps, ruObj.Spec.AsgName, nodeName, v1alpha1.NodeRotationKickoff, mutex)
		err = r.DrainTerminate(ruObj, nodeName, targetInstanceID, nodeSteps, inProcessingNodes, mutex)
	} else {
		err = fmt.Errorf("%s: unhandled strategy mode: %s", ruObj.NamespacedName(), mode)
	}

	if err != nil {
		ch <- err
		return
	}

	unjoined, err := r.WaitForTermination(ruObj, nodeName, r.generatedClient.CoreV1().Nodes())
	if err != nil {
		ch <- err
		return
	}

	if !unjoined {
		r.info(ruObj, "termination waiter completed but node is still joined, will proceed with upgrade", "nodeName", nodeName)
	}

	err = r.setStateTag(ruObj, targetInstanceID, "completed")
	if err != nil {
		r.info(ruObj, "Setting tag on the instance post termination failed.", "nodeName", nodeName)
	}
	ruObj.Status.NodesProcessed = ruObj.Status.NodesProcessed + 1
	if err := r.Status().Update(*ctx, ruObj); err != nil {
		// Check if the err is "StorageError: invalid object". If so, the object was deleted...
		if strings.Contains(err.Error(), "StorageError: invalid object") {
			r.info(ruObj, "Object mostly deleted")
		} else {
			r.error(ruObj, err, "failed to update status")
		}
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

func (r *RollingUpgradeReconciler) getTemplateLatestVersion(templateName string) string {
	for _, t := range r.LaunchTemplates {
		name := aws.StringValue(t.LaunchTemplateName)
		if name == templateName {
			versionInt := aws.Int64Value(t.LatestVersionNumber)
			return strconv.FormatInt(versionInt, 10)
		}
	}
	return "0"
}

// is the instance not using expected launch template ?
func (r *RollingUpgradeReconciler) requiresRefresh(ruObj *upgrademgrv1alpha1.RollingUpgrade, ec2Instance *autoscaling.Instance,
	definition *launchDefinition) bool {

	instanceID := aws.StringValue(ec2Instance.InstanceId)
	if ruObj.Spec.ForceRefresh {
		if ok, nodeCreationTS := r.getNodeCreationTimestamp(ec2Instance); ok {
			if nodeCreationTS.Before(ruObj.CreationTimestamp.Time) {
				r.info(ruObj, "rolling upgrade configured for forced refresh")
				return true
			}
		}

		r.info(ruObj, "node", instanceID, "created after rollingupgrade object. Ignoring forceRefresh")
		return false
	}
	if definition.launchConfigurationName != nil {
		if *(definition.launchConfigurationName) != aws.StringValue(ec2Instance.LaunchConfigurationName) {
			r.info(ruObj, "node", instanceID, "launch configuration name differs")
			return true
		}
	} else if definition.launchTemplate != nil {
		instanceLaunchTemplate := ec2Instance.LaunchTemplate
		targetLaunchTemplate := definition.launchTemplate

		if instanceLaunchTemplate == nil {
			r.info(ruObj, "node", instanceID, "instance switching to launch template")
			return true
		}

		var (
			instanceTemplateId   = aws.StringValue(instanceLaunchTemplate.LaunchTemplateId)
			templateId           = aws.StringValue(targetLaunchTemplate.LaunchTemplateId)
			instanceTemplateName = aws.StringValue(instanceLaunchTemplate.LaunchTemplateName)
			templateName         = aws.StringValue(targetLaunchTemplate.LaunchTemplateName)
			instanceVersion      = aws.StringValue(instanceLaunchTemplate.Version)
			templateVersion      = r.getTemplateLatestVersion(templateName)
		)

		if instanceTemplateId != templateId {
			r.info(ruObj, "node", instanceID, "launch template id differs", "instanceTemplateId", instanceTemplateId, "templateId", templateId)
			return true
		}
		if instanceTemplateName != templateName {
			r.info(ruObj, "node", instanceID, "launch template name differs", "instanceTemplateName", instanceTemplateName, "templateName", templateName)
			return true
		}

		if instanceVersion != templateVersion {
			r.info(ruObj, "node", instanceID, "launch template version differs", "instanceVersion", instanceVersion, "templateVersion", templateVersion)
			return true
		}
	}

	r.info(ruObj, "node", instanceID, "node refresh not required")
	return false
}

// logger creates logger for rolling upgrade.
func (r *RollingUpgradeReconciler) logger(ruObj *upgrademgrv1alpha1.RollingUpgrade) logr.Logger {
	return r.Log.WithValues("rollingupgrade", ruObj.NamespacedName())
}

// info logs message with Info level for the specified rolling upgrade.
func (r *RollingUpgradeReconciler) info(ruObj *upgrademgrv1alpha1.RollingUpgrade, msg string, keysAndValues ...interface{}) {
	r.logger(ruObj).Info(msg, keysAndValues...)
}

// error logs message with Error level for the specified rolling upgrade.
func (r *RollingUpgradeReconciler) error(ruObj *upgrademgrv1alpha1.RollingUpgrade, err error, msg string, keysAndValues ...interface{}) {
	r.logger(ruObj).Error(err, msg, keysAndValues...)
}
