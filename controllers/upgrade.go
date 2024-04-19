/*
Copyright 2021 Intuit Inc.

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
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/go-logr/logr"
	"github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"github.com/keikoproj/upgrade-manager/controllers/common"
	awsprovider "github.com/keikoproj/upgrade-manager/controllers/providers/aws"
	kubeprovider "github.com/keikoproj/upgrade-manager/controllers/providers/kubernetes"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var (
	//DefaultWaitGroupTimeout is the timeout value for DrainGroup
	DefaultWaitGroupTimeout = time.Second * 5

	//LaunchTemplate latest string
	LaunchTemplateVersionLatest = "$Latest"
)

// DrainManager holds the information to perform drain operation in parallel.
type DrainManager struct {
	DrainErrors chan error      `json:"-"`
	DrainGroup  *sync.WaitGroup `json:"-"`
}

type RollingUpgradeContext struct {
	logr.Logger
	ScriptRunner        ScriptRunner
	Auth                *RollingUpgradeAuthenticator
	Cloud               *DiscoveredState
	RollingUpgrade      *v1alpha1.RollingUpgrade
	DrainManager        *DrainManager
	metricsMutex        *sync.Mutex
	DrainTimeout        int
	IgnoreDrainFailures bool
	ReplacementNodesMap *sync.Map
	MaxReplacementNodes int
	AllowReplacements   bool
	EarlyCordonNodes    bool
}

func (r *RollingUpgradeContext) RotateNodes() error {
	failedDrainInstances, err := r.Auth.DescribeTaggedInstanceIDs(instanceStateTagKey, failedDrainTagValue)
	if err != nil {
		r.Error(err, "failed to discover ec2 instances with drain-failed tag", "name", r.RollingUpgrade.NamespacedName())
	}

	// set status to running
	r.RollingUpgrade.SetCurrentStatus(v1alpha1.StatusRunning)
	r.RollingUpgrade.SetLabel(v1alpha1.LabelKeyRollingUpgradeCurrentStatus, v1alpha1.StatusRunning)
	common.SetMetricRollupInitOrRunning(r.RollingUpgrade.Name)

	// set start time
	if r.RollingUpgrade.StartTime() == "" {
		r.RollingUpgrade.SetStartTime(time.Now().Format(time.RFC3339))
	}

	// discover the state of AWS and K8s cluster.
	if err := r.Cloud.Discover(); err != nil {
		r.Info("failed to discover the cloud", "scalingGroup", r.RollingUpgrade.ScalingGroupName(), "name", r.RollingUpgrade.NamespacedName())
		r.RollingUpgrade.SetCurrentStatus(v1alpha1.StatusError)
		r.RollingUpgrade.SetLabel(v1alpha1.LabelKeyRollingUpgradeCurrentStatus, v1alpha1.StatusError)
		common.SetMetricRollupFailed(r.RollingUpgrade.Name)
		return err
	}

	var (
		scalingGroup = awsprovider.SelectScalingGroup(r.RollingUpgrade.ScalingGroupName(), r.Cloud.ScalingGroups)
	)
	if reflect.DeepEqual(scalingGroup, &autoscaling.Group{}) {
		return errors.Errorf("scaling group not found, scalingGroupName: %v", r.RollingUpgrade.ScalingGroupName())
	}
	r.Info(
		"scaling group details",
		"scalingGroup", r.RollingUpgrade.ScalingGroupName(),
		"desiredInstances", aws.Int64Value(scalingGroup.DesiredCapacity),
		"launchConfig", aws.StringValue(scalingGroup.LaunchConfigurationName),
		"name", r.RollingUpgrade.NamespacedName(),
	)

	r.RollingUpgrade.SetTotalNodes(len(scalingGroup.Instances))

	// check if all instances are rotated.
	if !r.IsScalingGroupDrifted() {
		r.RollingUpgrade.SetCurrentStatus(v1alpha1.StatusComplete)
		r.RollingUpgrade.SetLabel(v1alpha1.LabelKeyRollingUpgradeCurrentStatus, v1alpha1.StatusComplete)
		common.SetMetricRollupCompleted(r.RollingUpgrade.Name)
		r.endTimeUpdate()
		return nil
	}

	rotationTargets := r.SelectTargets(scalingGroup, failedDrainInstances)

	if len(rotationTargets) == 0 && len(failedDrainInstances) > 0 {
		// If there are failed instances, but no rotation targets, then select failed instances anyway
		r.Info("selecting from failed instances since there are no rotation targets", "failedDrainInstances", failedDrainInstances, "name", r.RollingUpgrade.NamespacedName())
		rotationTargets = r.SelectTargets(scalingGroup, []string{})
	}

	if ok, err := r.ReplaceNodeBatch(rotationTargets); !ok {
		return err
	}

	return nil
}

func (r *RollingUpgradeContext) ReplaceNodeBatch(batch []*autoscaling.Instance) (bool, error) {
	var (
		mode = r.RollingUpgrade.StrategyMode()
	)

	r.Info("rotating batch", "instances", awsprovider.GetInstanceIDs(batch), "name", r.RollingUpgrade.NamespacedName())

	//A map to retain the steps for multiple nodes
	nodeSteps := make(map[string][]v1alpha1.NodeStepDuration)

	inProcessingNodes := r.RollingUpgrade.Status.NodeInProcessing
	if inProcessingNodes == nil {
		inProcessingNodes = make(map[string]*v1alpha1.NodeInProcessing)
	}

	//Early-Cordon - Cordon all the nodes to prevent scheduling of new pods on older nodes.
	if r.EarlyCordonNodes {
		r.Info("early-cordon has been enabled, all the instances in the node group will be cordoned", "name", r.RollingUpgrade.NamespacedName())
		if ok, err := r.CordonUncordonAllNodes(true); !ok {
			return ok, err
		}
	}

	switch mode {
	case v1alpha1.UpdateStrategyModeEager:
		for _, target := range batch {
			instanceID := aws.StringValue(target.InstanceId)
			node := kubeprovider.SelectNodeByInstanceID(instanceID, r.Cloud.ClusterNodes)
			if node == nil {
				r.Info("node object not found in clusterNodes, skipping this node for now", "instanceID", instanceID, "name", r.RollingUpgrade.NamespacedName())
				continue
			}

			var (
				nodeName = node.GetName()
			)
			//Add statistics
			r.NodeStep(inProcessingNodes, nodeSteps, r.RollingUpgrade.Spec.AsgName, nodeName, v1alpha1.NodeRotationKickoff)
		}

		batchInstanceIDs, inServiceInstanceIDs := awsprovider.GetInstanceIDs(batch), awsprovider.GetInServiceInstanceIDs(batch)
		// Tag and set to StandBy only the InService instances.
		if len(inServiceInstanceIDs) > 0 {

			// Check if replacement nodes are causing cluster to balloon
			clusterIsBallooning, allowedBatchSize := r.ClusterBallooning(len(inServiceInstanceIDs))
			if clusterIsBallooning || allowedBatchSize == 0 {
				// Allowing more replacement nodes can cause cluster ballooning. Requeue CR.
				return true, nil
			}
			if len(inServiceInstanceIDs) != allowedBatchSize {
				r.Info("cluster is about to hit max-replacement-nodes capacity, reducing batchSize", "prevBatchSize", len(inServiceInstanceIDs), "currBatchSize", allowedBatchSize, "name", r.RollingUpgrade.NamespacedName())
				inServiceInstanceIDs = inServiceInstanceIDs[:allowedBatchSize]
			}

			// Add in-progress tag
			r.Info("setting instances to in-progress", "batch", batchInstanceIDs, "instances(InService)", inServiceInstanceIDs, "name", r.RollingUpgrade.NamespacedName())
			if err := r.Auth.TagEC2instances(inServiceInstanceIDs, instanceStateTagKey, inProgressTagValue); err != nil {
				r.Error(err, "failed to set instances to in-progress", "batch", batchInstanceIDs, "instances(InService)", inServiceInstanceIDs, "name", r.RollingUpgrade.NamespacedName())
				r.UpdateMetricsStatus(inProcessingNodes, nodeSteps)
				return false, err
			}
			// Standby
			r.Info("setting instances to stand-by", "batch", batchInstanceIDs, "instances(InService)", inServiceInstanceIDs, "name", r.RollingUpgrade.NamespacedName())
			if err := r.SetBatchStandBy(inServiceInstanceIDs); err != nil {
				r.Info("failed to set instances to stand-by", "instances", batch, "message", err.Error(), "name", r.RollingUpgrade.NamespacedName())
			}

			// requeue until there are no InService instances in the batch
			r.UpdateMetricsStatus(inProcessingNodes, nodeSteps)
			return true, nil
		} else {
			r.Info("no InService instances in the batch", "batch", batchInstanceIDs, "instances(InService)", inServiceInstanceIDs, "name", r.RollingUpgrade.NamespacedName())
		}

		// turns onto desired nodes
		for _, target := range batch {
			instanceID := aws.StringValue(target.InstanceId)
			node := kubeprovider.SelectNodeByInstanceID(instanceID, r.Cloud.ClusterNodes)
			if node == nil {
				r.Info("node object not found in clusterNodes, skipping this node for now", "instanceID", instanceID, "name", r.RollingUpgrade.NamespacedName())
				continue
			}
			var (
				nodeName = node.GetName()
			)
			r.NodeStep(inProcessingNodes, nodeSteps, r.RollingUpgrade.Spec.AsgName, nodeName, v1alpha1.NodeRotationDesiredNodeReady)
		}

		// Wait for desired nodes
		r.Info("waiting for desired nodes", "name", r.RollingUpgrade.NamespacedName())
		if !r.DesiredNodesReady() {
			r.UpdateMetricsStatus(inProcessingNodes, nodeSteps)
			return true, nil
		}
		r.Info("desired nodes are ready", "name", r.RollingUpgrade.NamespacedName())

	case v1alpha1.UpdateStrategyModeLazy:
		for _, target := range batch {
			instanceID := aws.StringValue(target.InstanceId)
			node := kubeprovider.SelectNodeByInstanceID(instanceID, r.Cloud.ClusterNodes)
			if node == nil {
				r.Info("node object not found in clusterNodes, skipping this node for now", "instanceID", instanceID, "name", r.RollingUpgrade.NamespacedName())
				continue
			}
			var (
				nodeName = node.GetName()
			)
			// add statistics
			r.NodeStep(inProcessingNodes, nodeSteps, r.RollingUpgrade.Spec.AsgName, nodeName, v1alpha1.NodeRotationKickoff)
		}
		// add in-progress tag
		batchInstanceIDs, inServiceInstanceIDs := awsprovider.GetInstanceIDs(batch), awsprovider.GetInServiceInstanceIDs(batch)
		r.Info("setting batch to in-progress", "batch", batchInstanceIDs, "instances(InService)", inServiceInstanceIDs, "name", r.RollingUpgrade.NamespacedName())
		if err := r.Auth.TagEC2instances(inServiceInstanceIDs, instanceStateTagKey, inProgressTagValue); err != nil {
			r.Error(err, "failed to set batch in-progress", "batch", batchInstanceIDs, "instances(InService)", inServiceInstanceIDs, "name", r.RollingUpgrade.NamespacedName())
			r.UpdateMetricsStatus(inProcessingNodes, nodeSteps)
			return false, err
		}
	}

	var (
		lastTerminationTime = r.RollingUpgrade.LastNodeTerminationTime()
		nodeInterval        = r.RollingUpgrade.NodeIntervalSeconds()
		lastDrainTime       = r.RollingUpgrade.LastNodeDrainTime()
		drainInterval       = r.RollingUpgrade.PostDrainDelaySeconds()
	)

	// check if we are still waiting on a termination delay
	if lastTerminationTime != nil && !lastTerminationTime.IsZero() && time.Since(lastTerminationTime.Time).Seconds() < float64(nodeInterval) {
		r.Info("reconcile requeue due to termination interval wait", "name", r.RollingUpgrade.NamespacedName())
		return true, nil
	}
	// check if we are still waiting on a drain delay
	if lastDrainTime != nil && !lastDrainTime.IsZero() && time.Since(lastDrainTime.Time).Seconds() < float64(drainInterval) {
		r.Info("reconcile requeue due to drain interval wait", "name", r.RollingUpgrade.NamespacedName())
		return true, nil
	}

	if reflect.DeepEqual(r.DrainManager.DrainGroup, &sync.WaitGroup{}) {
		for _, target := range batch {
			instanceID := aws.StringValue(target.InstanceId)
			node := kubeprovider.SelectNodeByInstanceID(instanceID, r.Cloud.ClusterNodes)
			if node == nil {
				r.Info("node object not found in clusterNodes, skipping this node for now", "instanceID", instanceID, "name", r.RollingUpgrade.NamespacedName())
				continue
			}
			var (
				nodeName     = node.GetName()
				scriptTarget = ScriptTarget{
					InstanceID:    instanceID,
					NodeName:      nodeName,
					UpgradeObject: r.RollingUpgrade,
				}
			)
			r.DrainManager.DrainGroup.Add(1)

			// Determine IgnoreDrainFailure and DrainTimeout values. CR spec takes the precedence.
			var (
				drainTimeout        int
				ignoreDrainFailures bool
			)
			if r.RollingUpgrade.DrainTimeout() == nil {
				drainTimeout = r.DrainTimeout
			} else {
				drainTimeout = *r.RollingUpgrade.DrainTimeout()
			}

			if r.RollingUpgrade.IsIgnoreDrainFailures() == nil {
				ignoreDrainFailures = r.IgnoreDrainFailures
			} else {
				ignoreDrainFailures = *r.RollingUpgrade.IsIgnoreDrainFailures()
			}

			// Drain the nodes in parallel
			go func() {
				defer r.DrainManager.DrainGroup.Done()

				// Turns onto PreDrain script
				r.NodeStep(inProcessingNodes, nodeSteps, r.RollingUpgrade.Spec.AsgName, nodeName, v1alpha1.NodeRotationPredrainScript)

				// Predrain script
				if err := r.ScriptRunner.PreDrain(scriptTarget); err != nil {
					r.DrainManager.DrainErrors <- errors.Errorf("PreDrain failed: instanceID - %v, %v", instanceID, err.Error())
				}

				// Issue drain concurrently - set lastDrainTime
				if node := kubeprovider.SelectNodeByInstanceID(instanceID, r.Cloud.ClusterNodes); node != nil {
					r.Info("draining the node", "instance", instanceID, "node name", node.Name, "name", r.RollingUpgrade.NamespacedName())

					// Turns onto NodeRotationDrain
					r.NodeStep(inProcessingNodes, nodeSteps, r.RollingUpgrade.Spec.AsgName, nodeName, v1alpha1.NodeRotationDrain)

					if err := r.Auth.DrainNode(node, time.Duration(r.RollingUpgrade.PostDrainDelaySeconds()), drainTimeout, r.Auth.Kubernetes); err != nil {
						// ignore drain failures if either of spec or controller args have set ignoreDrainFailures to true.
						if !ignoreDrainFailures {
							if err := r.Auth.TagEC2instances([]string{instanceID}, instanceStateTagKey, failedDrainTagValue); err != nil {
								r.Error(err, "failed to set instances to drain-failed", "batch", instanceID, "name", r.RollingUpgrade.NamespacedName())
							}
							r.DrainManager.DrainErrors <- errors.Errorf("DrainNode failed: instanceID - %v, %v", instanceID, err.Error())
							return
						}
					}
				}

				// Turns onto NodeRotationPostdrainScript
				r.NodeStep(inProcessingNodes, nodeSteps, r.RollingUpgrade.Spec.AsgName, nodeName, v1alpha1.NodeRotationPostdrainScript)

				// post drain script
				if err := r.ScriptRunner.PostDrain(scriptTarget); err != nil {
					r.DrainManager.DrainErrors <- errors.Errorf("PostDrain failed: instanceID - %v, %v", instanceID, err.Error())
				}

				// Turns onto NodeRotationPostWait
				r.NodeStep(inProcessingNodes, nodeSteps, r.RollingUpgrade.Spec.AsgName, nodeName, v1alpha1.NodeRotationPostWait)

				// Post Wait Script
				if err := r.ScriptRunner.PostWait(scriptTarget); err != nil {
					r.DrainManager.DrainErrors <- errors.Errorf("PostWait failed: instanceID - %v, %v", instanceID, err.Error())
				}
			}()
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.DrainManager.DrainGroup.Wait()
	}()

	select {
	case err := <-r.DrainManager.DrainErrors:
		r.UpdateMetricsStatus(inProcessingNodes, nodeSteps)

		r.Error(err, "failed to rotate the node", "name", r.RollingUpgrade.NamespacedName())
		return false, err

	case <-done:
		// goroutines completed, terminate and requeue
		r.RollingUpgrade.SetLastNodeDrainTime(&metav1.Time{Time: time.Now()})
		r.Info("instances drained successfully, terminating", "name", r.RollingUpgrade.NamespacedName())
		for _, target := range batch {
			instanceID := aws.StringValue(target.InstanceId)
			node := kubeprovider.SelectNodeByInstanceID(instanceID, r.Cloud.ClusterNodes)
			if node == nil {
				r.Info("node object not found in clusterNodes, skipping this node for now", "instanceID", instanceID, "name", r.RollingUpgrade.NamespacedName())
				continue
			}
			var (
				nodeName     = node.GetName()
				scriptTarget = ScriptTarget{
					InstanceID:    instanceID,
					NodeName:      nodeName,
					UpgradeObject: r.RollingUpgrade,
				}
			)

			// Turns onto NodeRotationTerminate
			r.NodeStep(inProcessingNodes, nodeSteps, r.RollingUpgrade.Spec.AsgName, nodeName, v1alpha1.NodeRotationTerminate)

			// Terminate - set lastTerminateTime
			r.Info("terminating instance", "instance", instanceID, "name", r.RollingUpgrade.NamespacedName())

			if err := r.Auth.TerminateInstance(target); err != nil {
				// terminate failures are retryable
				r.Info("failed to terminate instance", "instance", instanceID, "message", err.Error(), "name", r.RollingUpgrade.NamespacedName())
				r.UpdateMetricsStatus(inProcessingNodes, nodeSteps)
				return true, nil
			}

			// Once instances are terminated, decrease them from the count.
			count, _ := r.ReplacementNodesMap.Load("ReplacementNodes")
			if count != nil && count.(int) > 0 {
				r.ReplacementNodesMap.Store("ReplacementNodes", count.(int)-1)
				r.Info("decrementing replacementNodes count", "ReplacementNodes", count.(int)-1, "name", r.RollingUpgrade.NamespacedName())
				r.AllowReplacements = false
			}

			r.RollingUpgrade.SetLastNodeTerminationTime(&metav1.Time{Time: time.Now()})

			// Turns onto NodeRotationTerminate
			r.NodeStep(inProcessingNodes, nodeSteps, r.RollingUpgrade.Spec.AsgName, nodeName, v1alpha1.NodeRotationPostTerminate)

			// Post Terminate Script
			if err := r.ScriptRunner.PostTerminate(scriptTarget); err != nil {
				return false, err
			}

			//Calculate the terminating time,
			terminatedTime := metav1.Time{
				Time: metav1.Now().Add(time.Duration(r.RollingUpgrade.NodeIntervalSeconds()) * time.Second),
			}
			r.NodeStep(inProcessingNodes, nodeSteps, r.RollingUpgrade.Spec.AsgName, nodeName, v1alpha1.NodeRotationTerminated)
			r.DoNodeStep(inProcessingNodes, nodeSteps, r.RollingUpgrade.Spec.AsgName, nodeName, v1alpha1.NodeRotationCompleted, terminatedTime)

		}
		r.UpdateMetricsStatus(inProcessingNodes, nodeSteps)

	case <-time.After(DefaultWaitGroupTimeout):
		// goroutines timed out - requeue

		r.UpdateMetricsStatus(inProcessingNodes, nodeSteps)

		r.Info("instances are still draining", "name", r.RollingUpgrade.NamespacedName())
		return true, nil
	}
	return true, nil
}

func (r *RollingUpgradeContext) SelectTargets(scalingGroup *autoscaling.Group, excludedInstances []string) []*autoscaling.Instance {
	var (
		batchSize          = r.RollingUpgrade.MaxUnavailable()
		totalNodes         = int(aws.Int64Value(scalingGroup.DesiredCapacity))
		inprogressTargets  = make([]*autoscaling.Instance, 0)
		unrpocessedTargets = make([]*autoscaling.Instance, 0)
		finalTargets       = make([]*autoscaling.Instance, 0)
	)
	unavailableInt := CalculateMaxUnavailable(batchSize, totalNodes)

	// first process all in progress instances
	r.Info("selecting batch for rotation", "batch size", unavailableInt, "name", r.RollingUpgrade.NamespacedName())
	if len(excludedInstances) > 0 {
		r.Info("ignoring failed drain instances", "instances", excludedInstances, "name", r.RollingUpgrade.NamespacedName())
	}
	for _, instance := range r.Cloud.InProgressInstances {
		if selectedInstance := awsprovider.SelectScalingGroupInstance(instance, scalingGroup); !reflect.DeepEqual(selectedInstance, &autoscaling.Instance{}) {
			//In-progress instances shouldn't be considered if they are in terminating state.
			if !common.ContainsEqualFold(awsprovider.TerminatingInstanceStates, aws.StringValue(selectedInstance.LifecycleState)) {
				inprogressTargets = append(inprogressTargets, selectedInstance)
			}
		}
	}

	if len(inprogressTargets) > 0 {
		r.Info("found in-progress instances", "instances", awsprovider.GetInstanceIDs(inprogressTargets), "name", r.RollingUpgrade.NamespacedName())
	}

	// continue to select other instances, if any.
	instances, err := r.Cloud.AmazonClientSet.DescribeInstancesWithoutTagValue(instanceStateTagKey, inProgressTagValue)
	if err != nil {
		r.Info("unable to select targets, will retry.", "name", r.RollingUpgrade.NamespacedName())
		return nil
	}

	for _, instance := range instances {
		//don't consider instances when - terminating, empty, duplicates, excluded (errored our previously), not drifted.
		if selectedInstance := awsprovider.SelectScalingGroupInstance(instance, scalingGroup); !reflect.DeepEqual(selectedInstance, &autoscaling.Instance{}) {
			if r.IsInstanceDrifted(selectedInstance) && !common.ContainsEqualFold(awsprovider.TerminatingInstanceStates, aws.StringValue(selectedInstance.LifecycleState)) {
				if !common.ContainsEqualFold(awsprovider.GetInstanceIDs(unrpocessedTargets), aws.StringValue(selectedInstance.InstanceId)) && !common.ContainsEqualFold(excludedInstances, aws.StringValue(selectedInstance.InstanceId)) {
					unrpocessedTargets = append(unrpocessedTargets, selectedInstance)
				}
			}
		}
	}

	r.Info("found unprocessed instances", "instances", unrpocessedTargets, "name", r.RollingUpgrade.NamespacedName())

	if r.RollingUpgrade.UpdateStrategyType() == v1alpha1.RandomUpdateStrategy {

		finalTargets = append(inprogressTargets, unrpocessedTargets...)

	} else if r.RollingUpgrade.UpdateStrategyType() == v1alpha1.UniformAcrossAzUpdateStrategy {

		var uniformAZTargets = make([]*autoscaling.Instance, 0)

		// split targets into groups based on their AZ
		targetsByAZMap := map[string][]*autoscaling.Instance{}
		for _, target := range unrpocessedTargets {
			az := aws.StringValue(target.AvailabilityZone)
			targetsByAZMap[az] = append(targetsByAZMap[az], target)
		}

		// round-robin across the AZs with targets uniformly first and then best effort with remaining
		for {
			if len(uniformAZTargets) == len(unrpocessedTargets) {
				break
			}

			for az := range targetsByAZMap {
				targetsByAZGroupSize := len(targetsByAZMap[az])
				if targetsByAZGroupSize > 0 {
					uniformAZTargets = append(uniformAZTargets, targetsByAZMap[az][targetsByAZGroupSize-1]) // append last target
					targetsByAZMap[az] = targetsByAZMap[az][:targetsByAZGroupSize-1]                        // pop last target
				}
			}
		}

		finalTargets = append(inprogressTargets, uniformAZTargets...)
	}

	if unavailableInt > len(finalTargets) {
		unavailableInt = len(finalTargets)
	}

	return finalTargets[:unavailableInt]
}

func (r *RollingUpgradeContext) IsInstanceDrifted(instance *autoscaling.Instance) bool {

	var (
		scalingGroupName = r.RollingUpgrade.ScalingGroupName()
		scalingGroup     = awsprovider.SelectScalingGroup(scalingGroupName, r.Cloud.ScalingGroups)
		instanceID       = aws.StringValue(instance.InstanceId)
	)

	// if an instance is in terminating state, ignore.
	if common.ContainsEqualFold(awsprovider.TerminatingInstanceStates, aws.StringValue(instance.LifecycleState)) {
		return false
	}

	// check if there is atleast one node that meets the force-referesh criteria
	if r.RollingUpgrade.IsForceRefresh() {
		node := kubeprovider.SelectNodeByInstanceID(instanceID, r.Cloud.ClusterNodes)
		if node == nil {
			r.Info("node object not found in clusterNodes, skipping this node for now", "instanceID", instanceID, "name", r.RollingUpgrade.NamespacedName())
			return false
		}
		var (
			nodeCreationTime    = node.CreationTimestamp.Time
			upgradeCreationTime = r.RollingUpgrade.CreationTimestamp.Time
		)
		if nodeCreationTime.Before(upgradeCreationTime) {
			r.Info("rolling upgrade configured for forced refresh", "instance", instanceID, "name", r.RollingUpgrade.NamespacedName())
			return true
		}
	}

	if scalingGroup.LaunchConfigurationName != nil {
		if instance.LaunchConfigurationName == nil {
			return true
		}
		launchConfigName := aws.StringValue(scalingGroup.LaunchConfigurationName)
		instanceConfigName := aws.StringValue(instance.LaunchConfigurationName)
		if !strings.EqualFold(launchConfigName, instanceConfigName) {
			return true
		}
	} else if scalingGroup.LaunchTemplate != nil {
		if instance.LaunchTemplate == nil {
			r.Info("instance is drifted, instance launchtemplate is empty", "name", r.RollingUpgrade.NamespacedName())
			return true
		}

		var (
			launchTemplateName      = aws.StringValue(scalingGroup.LaunchTemplate.LaunchTemplateName)
			instanceTemplateName    = aws.StringValue(instance.LaunchTemplate.LaunchTemplateName)
			instanceTemplateVersion = aws.StringValue(instance.LaunchTemplate.Version)
			templateVersion         = aws.StringValue(scalingGroup.LaunchTemplate.Version)
		)

		// replace latest string with latest version number
		if strings.EqualFold(templateVersion, LaunchTemplateVersionLatest) {
			templateVersion = awsprovider.GetTemplateLatestVersion(r.Cloud.LaunchTemplates, launchTemplateName)
		}

		if !strings.EqualFold(launchTemplateName, instanceTemplateName) {
			r.Info("instance is drifted, mismatch in launchtemplate name", "instanceID", instanceID, "instanceLT", instanceTemplateName, "asgLT", launchTemplateName, "name", r.RollingUpgrade.NamespacedName())
			return true
		} else if !strings.EqualFold(instanceTemplateVersion, templateVersion) {
			r.Info("instance is drifted, mismatch in launchtemplate version", "instanceID", instanceID, "instanceLT-version", instanceTemplateVersion, "asgLT-version", templateVersion, "name", r.RollingUpgrade.NamespacedName())
			return true
		}

	} else if scalingGroup.MixedInstancesPolicy != nil {
		if instance.LaunchTemplate == nil {
			r.Info("instance is drifted, instance launchtemplate is empty", "name", r.RollingUpgrade.NamespacedName())
			return true
		}

		var (
			launchTemplateName      = aws.StringValue(scalingGroup.MixedInstancesPolicy.LaunchTemplate.LaunchTemplateSpecification.LaunchTemplateName)
			instanceTemplateName    = aws.StringValue(instance.LaunchTemplate.LaunchTemplateName)
			instanceTemplateVersion = aws.StringValue(instance.LaunchTemplate.Version)
			templateVersion         = aws.StringValue(scalingGroup.MixedInstancesPolicy.LaunchTemplate.LaunchTemplateSpecification.Version)
		)

		// replace latest string with latest version number
		if strings.EqualFold(templateVersion, LaunchTemplateVersionLatest) {
			templateVersion = awsprovider.GetTemplateLatestVersion(r.Cloud.LaunchTemplates, launchTemplateName)
		}

		if !strings.EqualFold(launchTemplateName, instanceTemplateName) {
			r.Info("instance is drifted, mismatch in launchtemplate name", "instanceID", instanceID, "instanceLT", instanceTemplateName, "asgLT", launchTemplateName, "name", r.RollingUpgrade.NamespacedName())
			return true
		} else if !strings.EqualFold(instanceTemplateVersion, templateVersion) {
			r.Info("instance is drifted, mismatch in launchtemplate version", "instanceID", instanceID, "instanceLT-version", instanceTemplateVersion, "asgLT-version", templateVersion, "name", r.RollingUpgrade.NamespacedName())
			return true
		}
	}

	return false
}

func (r *RollingUpgradeContext) IsScalingGroupDrifted() bool {
	var (
		driftCount      = 0
		scalingGroup    = awsprovider.SelectScalingGroup(r.RollingUpgrade.ScalingGroupName(), r.Cloud.ScalingGroups)
		desiredCapacity = int(aws.Int64Value(scalingGroup.DesiredCapacity))
	)
	r.Info("checking if rolling upgrade is completed", "name", r.RollingUpgrade.NamespacedName())

	for _, instance := range scalingGroup.Instances {
		if r.IsInstanceDrifted(instance) {
			driftCount++
		}
	}
	if driftCount != 0 {
		r.Info("drift detected in scaling group", "driftedInstancesCount/DesiredInstancesCount", fmt.Sprintf("(%v/%v)", driftCount, desiredCapacity), "name", r.RollingUpgrade.NamespacedName())
		r.SetProgress(desiredCapacity-driftCount, desiredCapacity)
		return true
	}
	r.SetProgress(desiredCapacity, desiredCapacity)
	r.Info("no drift in scaling group", "name", r.RollingUpgrade.NamespacedName())
	return false
}

func (r *RollingUpgradeContext) DesiredNodesReady() bool {
	var (
		scalingGroup     = awsprovider.SelectScalingGroup(r.RollingUpgrade.ScalingGroupName(), r.Cloud.ScalingGroups)
		desiredInstances = aws.Int64Value(scalingGroup.DesiredCapacity)
		readyNodes       = 0
	)

	// wait for desired instances
	inServiceInstanceIDs := awsprovider.GetInServiceInstanceIDs(scalingGroup.Instances)
	if len(inServiceInstanceIDs) != int(desiredInstances) {
		r.Info("desired number of instances are not InService", "desired", int(desiredInstances), "inServiceCount", len(inServiceInstanceIDs), "name", r.RollingUpgrade.NamespacedName())
		return false
	}

	// wait for desired nodes
	if r.Cloud.ClusterNodes != nil && !reflect.DeepEqual(r.Cloud.ClusterNodes, &corev1.NodeList{}) {
		for _, node := range r.Cloud.ClusterNodes {
			instanceID := kubeprovider.GetNodeInstanceID(node)
			if common.ContainsEqualFold(inServiceInstanceIDs, instanceID) && kubeprovider.IsNodeReady(node) && kubeprovider.IsNodePassesReadinessGates(node, r.RollingUpgrade.Spec.ReadinessGates) {
				readyNodes++
			}
		}
	}
	if readyNodes != int(desiredInstances) {
		r.Info("desired number of nodes are not ready", "desired", int(desiredInstances), "readyNodesCount", readyNodes, "name", r.RollingUpgrade.NamespacedName())
		return false
	}

	return true
}

func CalculateMaxUnavailable(batchSize intstr.IntOrString, totalNodes int) int {
	var unavailableInt int
	if batchSize.Type == intstr.String {
		if strings.Contains(batchSize.StrVal, "%") {
			unavailableInt, _ = intstr.GetValueFromIntOrPercent(&batchSize, totalNodes, true)
		} else {
			unavailableInt, _ = strconv.Atoi(batchSize.StrVal)
		}
	} else {
		unavailableInt = batchSize.IntValue()
	}

	// batch size should be atleast 1
	if unavailableInt == 0 {
		unavailableInt = 1
	}

	// batch size should be atmost the number of nodes
	if unavailableInt > totalNodes {
		unavailableInt = totalNodes
	}

	return unavailableInt
}

func (r *RollingUpgradeContext) SetProgress(nodesProcessed int, totalNodes int) {
	if totalNodes > 0 && nodesProcessed >= 0 {
		r.RollingUpgrade.SetTotalNodes(totalNodes)
		r.RollingUpgrade.SetNodesProcessed(nodesProcessed)

		completePercentage := int(math.Round(float64(nodesProcessed) / float64(totalNodes) * 100))
		r.RollingUpgrade.SetCompletePercentage(completePercentage)

		// expose total nodes and nodes processed to prometheus
		common.SetTotalNodesMetric(r.RollingUpgrade.ScalingGroupName(), totalNodes)
		common.SetNodesProcessedMetric(r.RollingUpgrade.ScalingGroupName(), nodesProcessed)
	}

}

func (r *RollingUpgradeContext) endTimeUpdate() {
	// set end time
	r.RollingUpgrade.SetEndTime(time.Now().Format(time.RFC3339))

	// set total processing time
	startTime, err1 := time.Parse(time.RFC3339, r.RollingUpgrade.StartTime())
	endTime, err2 := time.Parse(time.RFC3339, r.RollingUpgrade.EndTime())
	if err1 != nil || err2 != nil {
		r.Info("failed to calculate totalProcessingTime")
	} else {
		var totalProcessingTime = endTime.Sub(startTime)
		r.RollingUpgrade.SetTotalProcessingTime(totalProcessingTime.String())

		// expose total processing time to prometheus
		common.TotalProcessingTime(r.RollingUpgrade.ScalingGroupName(), totalProcessingTime)
	}
}

// AWS API call for setting an instance to StandBy has a limit of 19. Hence we have to call the API in batches.
func (r *RollingUpgradeContext) SetBatchStandBy(instanceIDs []string) error {
	var err error
	instanceBatch := common.GetChunks(instanceIDs, awsprovider.InstanceStandByLimit)
	for _, batch := range instanceBatch {
		if err = r.Auth.SetInstancesStandBy(batch, r.RollingUpgrade.Spec.AsgName); err != nil {
			return err
		}
	}
	return nil
}

// Checks for how many replacement nodes exists across all the IGs in the cluster
func (r *RollingUpgradeContext) ClusterBallooning(batchSize int) (bool, int) {
	count, _ := r.ReplacementNodesMap.LoadOrStore("ReplacementNodes", 0)
	newReplacementCount := count.(int) + batchSize
	partialReplacementCount := r.MaxReplacementNodes - count.(int)

	// By default, no limits on replacement nodes.
	if r.MaxReplacementNodes == 0 {
		return false, batchSize
	}

	// Handle 3 different cases. 1) When entire batch can have replacement nodes. 2) When partial batch can have replacement nodes 3) When there is no availability for replacement nodes and CR has to re-queue
	if newReplacementCount <= r.MaxReplacementNodes {
		r.ReplacementNodesMap.Store("ReplacementNodes", newReplacementCount)
		r.Info("incrementing replacementNodes count", "ReplacementNodes", newReplacementCount, "name", r.RollingUpgrade.NamespacedName())
		r.AllowReplacements = true
	} else if partialReplacementCount < batchSize && partialReplacementCount > 0 {
		r.ReplacementNodesMap.Store("ReplacementNodes", count.(int)+partialReplacementCount)
		r.Info("incrementing replacementNodes count", "ReplacementNodes", count.(int)+partialReplacementCount, "name", r.RollingUpgrade.NamespacedName())
		r.AllowReplacements = true
		batchSize = partialReplacementCount
	} else if !r.AllowReplacements {
		r.Info("cluster has hit max-replacement-nodes capacity, requeuing rollingUpgrade CR. ", "replacementNodes", count.(int), "maxReplacementNodes", r.MaxReplacementNodes, "name", r.RollingUpgrade.NamespacedName())
		return true, 0
	}
	return false, batchSize
}

func (r *RollingUpgradeContext) CordonUncordonAllNodes(cordonNode bool) (bool, error) {
	scalingGroup := awsprovider.SelectScalingGroup(r.RollingUpgrade.ScalingGroupName(), r.Cloud.ScalingGroups)
	var instanceIDs []string
	var err error

	if cordonNode {
		instanceIDs, err = r.Cloud.AmazonClientSet.DescribeInstancesWithoutTagValue(instanceStateTagKey, earlyCordonedTagValue)
		if err != nil {
			r.Error(err, "failed to describe instances for early-cordoning", "name", r.RollingUpgrade.NamespacedName())
			return false, errors.Wrap(err, "failed to describe instances for early-cordoning")
		}
	} else {
		instanceIDs, err = r.Auth.DescribeTaggedInstanceIDs(instanceStateTagKey, earlyCordonedTagValue)
		if err != nil {
			r.Info("failed to discover ec2 instances with early-cordoned tag", "name", r.RollingUpgrade.NamespacedName())
		}

		r.Info("removing early-cordoning tag while uncordoning instances", "name", r.RollingUpgrade.NamespacedName())
		if err := r.Auth.UntagEC2instances(instanceIDs, instanceStateTagKey, earlyCordonedTagValue); err != nil {
			r.Info("failed to delete early-cordoned tag for instances", "name", r.RollingUpgrade.NamespacedName())
		}
		// add unit test as well.

	}

	for _, instanceID := range instanceIDs {
		if instance := awsprovider.SelectScalingGroupInstance(instanceID, scalingGroup); !reflect.DeepEqual(instance, &autoscaling.Instance{}) {
			//Don't consider if the instance is in terminating state.
			if !common.ContainsEqualFold(awsprovider.TerminatingInstanceStates, aws.StringValue(instance.LifecycleState)) {
				node := kubeprovider.SelectNodeByInstanceID(*instance.InstanceId, r.Cloud.ClusterNodes)
				if node == nil {
					r.Info("node object not found in clusterNodes, unable to early-cordon node", "instanceID", instance.InstanceId, "name", r.RollingUpgrade.NamespacedName())
					continue
				}
				//Early cordon only the dirfted instances and not the instances that have same scaling-config as the scaling-group
				if !r.IsInstanceDrifted(instance) {
					break
				}
				r.Info("early cordoning node", "instanceID", instance.InstanceId, "name", r.RollingUpgrade.NamespacedName())
				if err := r.Auth.CordonUncordonNode(node, r.Auth.Kubernetes, cordonNode); err != nil {
					r.Error(err, "failed to early cordon the nodes", "instanceID", instance.InstanceId, "name", r.RollingUpgrade.NamespacedName())
					return false, err
				}
				// Set instance-state to early-cordoned tag
				r.Info("tagging instances with cordoned=true", "instanceID", instance.InstanceId, "name", r.RollingUpgrade.NamespacedName())
				if err := r.Auth.TagEC2instances([]string{*instance.InstanceId}, instanceStateTagKey, earlyCordonedTagValue); err != nil {
					r.Error(err, "failed to tag instances with cordoned=true", "instanceID", instance.InstanceId, "name", r.RollingUpgrade.NamespacedName())
					return true, err
				}
			}
		}
	}
	return true, nil
}
