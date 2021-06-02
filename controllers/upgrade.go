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
)

// DrainManager holds the information to perform drain operation in parallel.
type DrainManager struct {
	DrainErrors chan error      `json:"-"`
	DrainGroup  *sync.WaitGroup `json:"-"`
}

type RollingUpgradeContext struct {
	logr.Logger
	ScriptRunner   ScriptRunner
	Auth           *RollingUpgradeAuthenticator
	Cloud          *DiscoveredState
	RollingUpgrade *v1alpha1.RollingUpgrade
	DrainManager   *DrainManager
	metricsMutex   *sync.Mutex
}

// TODO: main node rotation logic
func (r *RollingUpgradeContext) RotateNodes() error {
	var (
		lastTerminationTime = r.RollingUpgrade.LastNodeTerminationTime()
		nodeInterval        = r.RollingUpgrade.NodeIntervalSeconds()
		lastDrainTime       = r.RollingUpgrade.LastNodeDrainTime()
		drainInterval       = r.RollingUpgrade.PostDrainDelaySeconds()
	)
	r.RollingUpgrade.SetCurrentStatus(v1alpha1.StatusRunning)
	common.SetMetricRollupInitOrRunning(r.RollingUpgrade.Name)

	// set status start time
	if r.RollingUpgrade.StartTime() == "" {
		r.RollingUpgrade.SetStartTime(time.Now().Format(time.RFC3339))
	}

	if !lastTerminationTime.IsZero() || !lastDrainTime.IsZero() {

		// Check if we are still waiting on a termination delay
		if time.Since(lastTerminationTime.Time).Seconds() < float64(nodeInterval) {
			r.Info("reconcile requeue due to termination interval wait", "name", r.RollingUpgrade.NamespacedName())
			return nil
		}

		// Check if we are still waiting on a drain delay
		if time.Since(lastDrainTime.Time).Seconds() < float64(drainInterval) {
			r.Info("reconcile requeue due to drain interval wait", "name", r.RollingUpgrade.NamespacedName())
			return nil
		}
	}

	var (
		scalingGroup = awsprovider.SelectScalingGroup(r.RollingUpgrade.ScalingGroupName(), r.Cloud.ScalingGroups)
	)

	r.RollingUpgrade.SetTotalNodes(len(scalingGroup.Instances))

	// check if all instances are rotated.
	if !r.IsScalingGroupDrifted() {
		r.RollingUpgrade.SetCurrentStatus(v1alpha1.StatusComplete)
		common.SetMetricRollupCompleted(r.RollingUpgrade.Name)
		return nil
	}

	rotationTargets := r.SelectTargets(scalingGroup)
	if ok, err := r.ReplaceNodeBatch(rotationTargets); !ok {
		return err
	}

	return nil
}

func (r *RollingUpgradeContext) ReplaceNodeBatch(batch []*autoscaling.Instance) (bool, error) {
	var (
		mode = r.RollingUpgrade.StrategyMode()
	)

	r.Info("rotating batch", "instance IDs", awsprovider.GetInstanceIDs(batch), "name", r.RollingUpgrade.NamespacedName())

	nodeSteps := make(map[string][]v1alpha1.NodeStepDuration)

	inProcessingNodes := make(map[string]*v1alpha1.NodeInProcessing)

	switch mode {
	case v1alpha1.UpdateStrategyModeEager:
		for _, target := range batch {
			var (
				instanceID = aws.StringValue(target.InstanceId)
				node       = kubeprovider.SelectNodeByInstanceID(instanceID, r.Cloud.ClusterNodes)
				nodeName   = node.GetName()
			)
			//Add statistics
			r.NodeStep(inProcessingNodes, nodeSteps, r.RollingUpgrade.Spec.AsgName, nodeName, v1alpha1.NodeRotationKickoff)
		}

		batchInstanceIDs, inServiceInstanceIDs := awsprovider.GetInstanceIDs(batch), awsprovider.GetInServiceInstanceIDs(batch)
		// Tag and set to StandBy only the InService instances.
		if len(inServiceInstanceIDs) > 0 {
			// Add in-progress tag
			r.Info("setting instances to in-progress", "batch", batchInstanceIDs, "instances(InService)", inServiceInstanceIDs, "name", r.RollingUpgrade.NamespacedName())
			if err := r.Auth.TagEC2instances(inServiceInstanceIDs, instanceStateTagKey, inProgressTagValue); err != nil {
				r.Error(err, "failed to set instances to in-progress", "batch", batchInstanceIDs, "instances(InService)", inServiceInstanceIDs, "name", r.RollingUpgrade.NamespacedName())
				return false, err
			}
			// Standby
			r.Info("setting instances to stand-by", "batch", batchInstanceIDs, "instances(InService)", inServiceInstanceIDs, "name", r.RollingUpgrade.NamespacedName())
			if err := r.Auth.SetInstancesStandBy(inServiceInstanceIDs, r.RollingUpgrade.Spec.AsgName); err != nil {
				r.Info("failed to set instances to stand-by", "batch", batchInstanceIDs, "instances(InService)", inServiceInstanceIDs, "message", err.Error(), "name", r.RollingUpgrade.NamespacedName())
			}
			// requeue until there are no InService instances in the batch
			return true, nil
		} else {
			r.Info("no InService instances in the batch", "batch", batchInstanceIDs, "instances(InService)", inServiceInstanceIDs, "name", r.RollingUpgrade.NamespacedName())
		}

		// turns onto desired nodes
		for _, target := range batch {
			var (
				instanceID = aws.StringValue(target.InstanceId)
				node       = kubeprovider.SelectNodeByInstanceID(instanceID, r.Cloud.ClusterNodes)
				nodeName   = node.GetName()
			)
			r.NodeStep(inProcessingNodes, nodeSteps, r.RollingUpgrade.Spec.AsgName, nodeName, v1alpha1.NodeRotationDesiredNodeReady)
		}

		// Wait for desired nodes
		r.Info("waiting for desired nodes", "name", r.RollingUpgrade.NamespacedName())
		if !r.DesiredNodesReady() {
			r.Info("new node is yet to join the cluster", "name", r.RollingUpgrade.NamespacedName())
			return true, nil
		}
		r.Info("desired nodes are ready", "name", r.RollingUpgrade.NamespacedName())

	case v1alpha1.UpdateStrategyModeLazy:
		for _, target := range batch {
			var (
				instanceID = aws.StringValue(target.InstanceId)
				node       = kubeprovider.SelectNodeByInstanceID(instanceID, r.Cloud.ClusterNodes)
				nodeName   = node.GetName()
			)
			// add statistics
			r.NodeStep(inProcessingNodes, nodeSteps, r.RollingUpgrade.Spec.AsgName, nodeName, v1alpha1.NodeRotationKickoff)
		}
		// add in-progress tag
		batchInstanceIDs, inServiceInstanceIDs := awsprovider.GetInstanceIDs(batch), awsprovider.GetInServiceInstanceIDs(batch)
		r.Info("setting batch to in-progress", "batch", batchInstanceIDs, "instances(InService)", inServiceInstanceIDs, "name", r.RollingUpgrade.NamespacedName())
		if err := r.Auth.TagEC2instances(inServiceInstanceIDs, instanceStateTagKey, inProgressTagValue); err != nil {
			r.Error(err, "failed to set batch in-progress", "batch", batchInstanceIDs, "instances(InService)", inServiceInstanceIDs, "name", r.RollingUpgrade.NamespacedName())
			return false, err
		}
	}

	if reflect.DeepEqual(r.DrainManager.DrainGroup, &sync.WaitGroup{}) {
		for _, target := range batch {
			var (
				instanceID   = aws.StringValue(target.InstanceId)
				node         = kubeprovider.SelectNodeByInstanceID(instanceID, r.Cloud.ClusterNodes)
				nodeName     = node.GetName()
				scriptTarget = ScriptTarget{
					InstanceID:    instanceID,
					NodeName:      nodeName,
					UpgradeObject: r.RollingUpgrade,
				}
			)
			r.DrainManager.DrainGroup.Add(1)

			go func() {
				defer r.DrainManager.DrainGroup.Done()

				// Turns onto PreDrain script
				r.NodeStep(inProcessingNodes, nodeSteps, r.RollingUpgrade.Spec.AsgName, nodeName, v1alpha1.NodeRotationPredrainScript)

				// Predrain script
				if err := r.ScriptRunner.PreDrain(scriptTarget); err != nil {
					r.DrainManager.DrainErrors <- errors.Errorf("PreDrain failed: instanceID - %v, %v", instanceID, err.Error())
				}

				// Issue drain concurrently - set lastDrainTime
				if node := kubeprovider.SelectNodeByInstanceID(instanceID, r.Cloud.ClusterNodes); !reflect.DeepEqual(node, corev1.Node{}) {
					r.Info("draining the node", "instance", instanceID, "node name", node.Name, "name", r.RollingUpgrade.NamespacedName())

					// Turns onto NodeRotationDrain
					r.NodeStep(inProcessingNodes, nodeSteps, r.RollingUpgrade.Spec.AsgName, nodeName, v1alpha1.NodeRotationDrain)

					if err := r.Auth.DrainNode(&node, time.Duration(r.RollingUpgrade.PostDrainDelaySeconds()), r.RollingUpgrade.DrainTimeout(), r.Auth.Kubernetes); err != nil {
						r.DrainManager.DrainErrors <- errors.Errorf("DrainNode failed: instanceID - %v, %v", instanceID, err.Error())
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
		r.UpdateStatistics(nodeSteps)
		r.UpdateLastBatchNodes(inProcessingNodes)

		r.Error(err, "failed to rotate the node", "name", r.RollingUpgrade.NamespacedName())
		return false, err

	case <-done:
		// goroutines completed, terminate and requeue
		r.RollingUpgrade.SetLastNodeDrainTime(metav1.Time{Time: time.Now()})
		r.Info("instances drained successfully, terminating", "name", r.RollingUpgrade.NamespacedName())
		for _, target := range batch {
			var (
				instanceID   = aws.StringValue(target.InstanceId)
				node         = kubeprovider.SelectNodeByInstanceID(instanceID, r.Cloud.ClusterNodes)
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
				return true, nil
			}
			r.RollingUpgrade.SetLastNodeTerminationTime(metav1.Time{Time: time.Now()})

			// Turns onto NodeRotationTerminate
			r.NodeStep(inProcessingNodes, nodeSteps, r.RollingUpgrade.Spec.AsgName, nodeName, v1alpha1.NodeRotationPostTerminate)

			// Post Terminate Script
			if err := r.ScriptRunner.PostTerminate(scriptTarget); err != nil {
				return false, err
			}

			// Turns onto NodeRotationCompleted
			r.NodeStep(inProcessingNodes, nodeSteps, r.RollingUpgrade.Spec.AsgName, nodeName, v1alpha1.NodeRotationCompleted)
		}

		r.UpdateStatistics(nodeSteps)
		r.UpdateLastBatchNodes(inProcessingNodes)

	case <-time.After(DefaultWaitGroupTimeout):
		// goroutines timed out - requeue

		r.UpdateStatistics(nodeSteps)
		r.UpdateLastBatchNodes(inProcessingNodes)

		r.Info("instances are still draining", "name", r.RollingUpgrade.NamespacedName())
		return true, nil
	}
	return true, nil
}

func (r *RollingUpgradeContext) SelectTargets(scalingGroup *autoscaling.Group) []*autoscaling.Instance {
	var (
		batchSize  = r.RollingUpgrade.MaxUnavailable()
		totalNodes = len(scalingGroup.Instances)
		targets    = make([]*autoscaling.Instance, 0)
	)

	var unavailableInt int
	if batchSize.Type == intstr.String {
		if strings.Contains(batchSize.StrVal, "%") {
			unavailableInt, _ = intstr.GetValueFromIntOrPercent(&batchSize, totalNodes, true)
		}
		unavailableInt, _ = strconv.Atoi(batchSize.StrVal)
	} else {
		unavailableInt = batchSize.IntValue()
	}

	// first process all in progress instances
	r.Info("selecting batch for rotation", "batch size", batchSize, "name", r.RollingUpgrade.NamespacedName())
	for _, instance := range r.Cloud.InProgressInstances {
		if selectedInstance := awsprovider.SelectScalingGroupInstance(instance, scalingGroup); !reflect.DeepEqual(selectedInstance, &autoscaling.Instance{}) {
			targets = append(targets, selectedInstance)
		}
	}

	if len(targets) > 0 {
		r.Info("found in-progress instances", "instances", awsprovider.GetInstanceIDs(targets))
	}

	// select via strategy if there are no in-progress instances
	if r.RollingUpgrade.UpdateStrategyType() == v1alpha1.RandomUpdateStrategy {
		for _, instance := range scalingGroup.Instances {
			if r.IsInstanceDrifted(instance) && !common.ContainsEqualFold(awsprovider.GetInstanceIDs(targets), aws.StringValue(instance.InstanceId)) {
				targets = append(targets, instance)
			}
		}
		if unavailableInt > len(targets) {
			unavailableInt = len(targets)
		}
		return targets[:unavailableInt]

	} else if r.RollingUpgrade.UpdateStrategyType() == v1alpha1.UniformAcrossAzUpdateStrategy {
		for _, instance := range scalingGroup.Instances {
			if r.IsInstanceDrifted(instance) && !common.ContainsEqualFold(awsprovider.GetInstanceIDs(targets), aws.StringValue(instance.InstanceId)) {
				targets = append(targets, instance)
			}
		}

		var AZtargets = make([]*autoscaling.Instance, 0)
		AZs := awsprovider.GetScalingAZs(targets)
		if len(AZs) == 0 {
			return AZtargets
		}
		for _, target := range targets {
			AZ := aws.StringValue(target.AvailabilityZone)
			if strings.EqualFold(AZ, AZs[0]) {
				AZtargets = append(AZtargets, target)
			}
		}
		if unavailableInt > len(AZtargets) {
			unavailableInt = len(AZtargets)
		}
		return AZtargets[:unavailableInt]
	}
	return targets
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
		var (
			node                = kubeprovider.SelectNodeByInstanceID(instanceID, r.Cloud.ClusterNodes)
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
			r.Info("launch configuration name differs", "instance", instanceID, "name", r.RollingUpgrade.NamespacedName())
			return true
		}
		launchConfigName := aws.StringValue(scalingGroup.LaunchConfigurationName)
		instanceConfigName := aws.StringValue(instance.LaunchConfigurationName)
		if !strings.EqualFold(launchConfigName, instanceConfigName) {
			r.Info("launch configuration name differs", "instance", instanceID, "name", r.RollingUpgrade.NamespacedName())
			return true
		}
	} else if scalingGroup.LaunchTemplate != nil {
		r.Info("launchTemplates", "instance", instanceID, "instanceLT", aws.StringValue(instance.LaunchTemplate.LaunchTemplateName), "scalingGroupLT", aws.StringValue(scalingGroup.LaunchTemplate.LaunchTemplateName), "name", r.RollingUpgrade.NamespacedName())
		if instance.LaunchTemplate == nil {
			r.Info("launch template name differs", "instance", instanceID, "name", r.RollingUpgrade.NamespacedName())
			return true
		}

		var (
			launchTemplateName      = aws.StringValue(scalingGroup.LaunchTemplate.LaunchTemplateName)
			instanceTemplateName    = aws.StringValue(instance.LaunchTemplate.LaunchTemplateName)
			instanceTemplateVersion = aws.StringValue(instance.LaunchTemplate.Version)
			templateVersion         = awsprovider.GetTemplateLatestVersion(r.Cloud.LaunchTemplates, launchTemplateName)
		)

		if !strings.EqualFold(launchTemplateName, instanceTemplateName) {
			r.Info("launch template name differs", "instance", instanceID, "name", r.RollingUpgrade.NamespacedName())
			return true
		} else if !strings.EqualFold(instanceTemplateVersion, templateVersion) {
			r.Info("launch template version differs", "instance", instanceID, "name", r.RollingUpgrade.NamespacedName())
			return true
		}

	} else if scalingGroup.MixedInstancesPolicy != nil {
		if instance.LaunchTemplate == nil {
			r.Info("launch template name differs", "instance", instanceID, "name", r.RollingUpgrade.NamespacedName())
			return true
		}

		var (
			launchTemplateName      = aws.StringValue(scalingGroup.MixedInstancesPolicy.LaunchTemplate.LaunchTemplateSpecification.LaunchTemplateName)
			instanceTemplateName    = aws.StringValue(instance.LaunchTemplate.LaunchTemplateName)
			instanceTemplateVersion = aws.StringValue(instance.LaunchTemplate.Version)
			templateVersion         = awsprovider.GetTemplateLatestVersion(r.Cloud.LaunchTemplates, launchTemplateName)
		)

		if !strings.EqualFold(launchTemplateName, instanceTemplateName) {
			r.Info("launch template name differs", "instance", instanceID, "name", r.RollingUpgrade.NamespacedName())
			return true
		} else if !strings.EqualFold(instanceTemplateVersion, templateVersion) {
			r.Info("launch template version differs", "instance", instanceID, "name", r.RollingUpgrade.NamespacedName())
			return true
		}
	}

	r.Info("node refresh not required", "name", r.RollingUpgrade.NamespacedName(), "instance", instanceID)
	return false
}

func (r *RollingUpgradeContext) IsScalingGroupDrifted() bool {
	r.Info("checking if rolling upgrade is completed", "name", r.RollingUpgrade.NamespacedName())

	scalingGroup := awsprovider.SelectScalingGroup(r.RollingUpgrade.ScalingGroupName(), r.Cloud.ScalingGroups)
	for _, instance := range scalingGroup.Instances {
		if r.IsInstanceDrifted(instance) {
			r.Info("launch definition differs", "instance", aws.StringValue(instance.InstanceId), "name", r.RollingUpgrade.NamespacedName())
			return true
		}
	}
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
		for _, node := range r.Cloud.ClusterNodes.Items {
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
