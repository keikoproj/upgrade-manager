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
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"github.com/keikoproj/upgrade-manager/controllers/common"
	awsprovider "github.com/keikoproj/upgrade-manager/controllers/providers/aws"
	kubeprovider "github.com/keikoproj/upgrade-manager/controllers/providers/kubernetes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// TODO: main node rotation logic
func (r *RollingUpgradeReconciler) RotateNodes(rollingUpgrade *v1alpha1.RollingUpgrade) error {
	var (
		lastTerminationTime = rollingUpgrade.LastNodeTerminationTime()
		nodeInterval        = rollingUpgrade.NodeIntervalSeconds()
		lastDrainTime       = rollingUpgrade.LastNodeDrainTime()
		drainInterval       = rollingUpgrade.PostDrainDelaySeconds()
	)
	rollingUpgrade.SetCurrentStatus(v1alpha1.StatusRunning)

	// set status start time
	if rollingUpgrade.StartTime() == "" {
		rollingUpgrade.SetStartTime(time.Now().Format(time.RFC3339))
	}

	if !lastTerminationTime.IsZero() || !lastDrainTime.IsZero() {

		// Check if we are still waiting on a termination delay
		if time.Since(lastTerminationTime.Time).Seconds() < float64(nodeInterval) {
			r.Info("reconcile requeue due to termination interval wait", "name", rollingUpgrade.NamespacedName())
			return nil
		}

		// Check if we are still waiting on a drain delay
		if time.Since(lastDrainTime.Time).Seconds() < float64(drainInterval) {
			r.Info("reconcile requeue due to drain interval wait", "name", rollingUpgrade.NamespacedName())
			return nil
		}
	}

	var (
		scalingGroup = awsprovider.SelectScalingGroup(rollingUpgrade.ScalingGroupName(), r.Cloud.ScalingGroups)
	)

	rollingUpgrade.SetTotalNodes(len(scalingGroup.Instances))

	// check if all instances are rotated.
	if !r.IsScalingGroupDrifted(rollingUpgrade) {
		rollingUpgrade.SetCurrentStatus(v1alpha1.StatusComplete)
		return nil
	}

	rotationTargets := r.SelectTargets(rollingUpgrade, scalingGroup)
	if ok, err := r.ReplaceNodeBatch(rollingUpgrade, rotationTargets); !ok {
		return err
	}

	return nil
}

func (r *RollingUpgradeReconciler) ReplaceNodeBatch(rollingUpgrade *v1alpha1.RollingUpgrade, batch []*autoscaling.Instance) (bool, error) {
	var (
		mode = rollingUpgrade.StrategyMode()
	)

	r.Info("rotating batch", "instances", awsprovider.GetInstanceIDs(batch), "name", rollingUpgrade.NamespacedName())

	switch mode {
	case v1alpha1.UpdateStrategyModeEager:

		// TODO: THE BELOW LOOP IS A TEMPORARY PLACEHOLDER FOR TESTING PURPOSES
		// WE SHOULD SWITCH TO PARALLEL PROCESSING OF TARGETS IN A BATCH VIA GOROUTINES

		for _, target := range batch {

			var (
				instanceID   = aws.StringValue(target.InstanceId)
				node         = kubeprovider.SelectNodeByInstanceID(instanceID, r.Cloud.ClusterNodes)
				nodeName     = node.GetName()
				scriptTarget = ScriptTarget{
					InstanceID:    instanceID,
					NodeName:      nodeName,
					UpgradeObject: rollingUpgrade,
				}
			)

			// Add in-progress tag
			if err := r.Auth.TagEC2instance(instanceID, instanceStateTagKey, inProgressTagValue); err != nil {
				r.Error(err, "failed to set instance tag", "name", rollingUpgrade.NamespacedName(), "instance", instanceID)
				return false, err
			}

			// Standby
			if aws.StringValue(target.LifecycleState) == autoscaling.LifecycleStateInService {
				r.Info("setting instance to stand-by", "name", rollingUpgrade.NamespacedName(), "instance", instanceID)
				if err := r.Auth.SetInstanceStandBy(target, rollingUpgrade.Spec.AsgName); err != nil {
					r.Error(err, "couldn't set the instance to stand-by", "name", rollingUpgrade.NamespacedName(), "instance", instanceID)
					return false, err
				}
			}

			// Wait for desired nodes
			if !r.DesiredNodesReady(rollingUpgrade) {
				return true, nil
			}

			// Predrain script
			if err := r.ScriptRunner.PreDrain(scriptTarget); err != nil {
				return false, err
			}

			// Issue drain concurrently - set lastDrainTime
			if node := kubeprovider.SelectNodeByInstanceID(instanceID, r.Cloud.ClusterNodes); !reflect.DeepEqual(node, corev1.Node{}) {
				r.Info("draining the node", "name", rollingUpgrade.NamespacedName(), "instance", instanceID, "node name", node.Name)
				if err := r.Auth.DrainNode(&node, time.Duration(rollingUpgrade.PostDrainDelaySeconds()), rollingUpgrade.DrainTimeout(), r.Auth.Kubernetes); err != nil {
					r.Error(err, "failed to drain node", "name", rollingUpgrade.NamespacedName(), "instance", instanceID, "node name", node.Name)
					return false, err
				}
			}
			rollingUpgrade.SetLastNodeDrainTime(metav1.Time{Time: time.Now()})

			// post drain script
			if err := r.ScriptRunner.PostDrain(scriptTarget); err != nil {
				return false, err
			}

			// Post Wait Script
			if err := r.ScriptRunner.PostWait(scriptTarget); err != nil {
				return false, err
			}

			// Terminate - set lastTerminateTime
			r.Info("terminating instance", "name", rollingUpgrade.NamespacedName(), "instance", instanceID)
			if err := r.Auth.TerminateInstance(target); err != nil {
				r.Info("failed to terminate instance", "name", rollingUpgrade.NamespacedName(), "instance", instanceID, "message", err)
				return true, nil
			}
			rollingUpgrade.SetLastNodeTerminationTime(metav1.Time{Time: time.Now()})

			// Post Terminate Script
			if err := r.ScriptRunner.PostTerminate(scriptTarget); err != nil {
				return false, err
			}
		}
	case v1alpha1.UpdateStrategyModeLazy:
		for _, target := range batch {
			_ = target
			// Add in-progress tag

			// Issue drain/scripts concurrently - set lastDrainTime

			// Is drained?

			// Terminate - set lastTerminateTime
		}
	}
	return true, nil
}

func (r *RollingUpgradeReconciler) SelectTargets(rollingUpgrade *v1alpha1.RollingUpgrade, scalingGroup *autoscaling.Group) []*autoscaling.Instance {
	var (
		batchSize  = rollingUpgrade.MaxUnavailable()
		totalNodes = len(scalingGroup.Instances)
		targets    = make([]*autoscaling.Instance, 0)
	)

	var unavailableInt int
	if batchSize.Type == intstr.String {
		unavailableInt, _ = intstr.GetValueFromIntOrPercent(&batchSize, totalNodes, true)
	} else {
		unavailableInt = batchSize.IntValue()
	}

	// first process all in progress instances
	for _, instance := range r.Cloud.InProgressInstances {
		selectedInstance := awsprovider.SelectScalingGroupInstance(instance, scalingGroup)
		targets = append(targets, selectedInstance)
	}

	if len(targets) > 0 {
		if unavailableInt > len(targets) {
			unavailableInt = len(targets)
		}
		return targets[:unavailableInt]
	}

	// select via strategy if there are no in-progress instances
	if rollingUpgrade.UpdateStrategyType() == v1alpha1.RandomUpdateStrategy {
		for _, instance := range scalingGroup.Instances {
			if r.IsInstanceDrifted(rollingUpgrade, instance) {
				targets = append(targets, instance)
			}
		}
		if unavailableInt > len(targets) {
			unavailableInt = len(targets)
		}
		return targets[:unavailableInt]

	} else if rollingUpgrade.UpdateStrategyType() == v1alpha1.UniformAcrossAzUpdateStrategy {
		for _, instance := range scalingGroup.Instances {
			if r.IsInstanceDrifted(rollingUpgrade, instance) {
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

func (r *RollingUpgradeReconciler) IsInstanceDrifted(rollingUpgrade *v1alpha1.RollingUpgrade, instance *autoscaling.Instance) bool {

	var (
		scalingGroupName = rollingUpgrade.ScalingGroupName()
		scalingGroup     = awsprovider.SelectScalingGroup(scalingGroupName, r.Cloud.ScalingGroups)
		instanceID       = aws.StringValue(instance.InstanceId)
	)

	// if an instance is in terminating state, ignore.
	if common.ContainsEqualFold(awsprovider.TerminatingInstanceStates, aws.StringValue(instance.LifecycleState)) {
		return false
	}
	// check if there is atleast one node that meets the force-referesh criteria
	if rollingUpgrade.IsForceRefresh() {
		var (
			node                = kubeprovider.SelectNodeByInstanceID(instanceID, r.Cloud.ClusterNodes)
			nodeCreationTime    = node.CreationTimestamp.Time
			upgradeCreationTime = rollingUpgrade.CreationTimestamp.Time
		)
		if nodeCreationTime.Before(upgradeCreationTime) {
			r.Info("rolling upgrade configured for forced refresh", "name", rollingUpgrade.NamespacedName(), "instance", instanceID)
			return true
		}
	}

	if scalingGroup.LaunchConfigurationName != nil {
		if instance.LaunchConfigurationName == nil {
			r.Info("launch configuration name differs", "name", rollingUpgrade.NamespacedName(), "instance", instanceID)
			return true
		}
		launchConfigName := aws.StringValue(scalingGroup.LaunchConfigurationName)
		instanceConfigName := aws.StringValue(instance.LaunchConfigurationName)
		if !strings.EqualFold(launchConfigName, instanceConfigName) {
			r.Info("launch configuration name differs", "name", rollingUpgrade.NamespacedName(), "instance", instanceID)
			return true
		}
	} else if scalingGroup.LaunchTemplate != nil {
		if instance.LaunchTemplate == nil {
			r.Info("launch template name differs", "name", rollingUpgrade.NamespacedName(), "instance", instanceID)
			return true
		}

		var (
			launchTemplateName      = aws.StringValue(scalingGroup.LaunchTemplate.LaunchTemplateName)
			instanceTemplateName    = aws.StringValue(instance.LaunchTemplate.LaunchTemplateName)
			instanceTemplateVersion = aws.StringValue(instance.LaunchTemplate.Version)
			templateVersion         = awsprovider.GetTemplateLatestVersion(r.Cloud.LaunchTemplates, launchTemplateName)
		)

		if !strings.EqualFold(launchTemplateName, instanceTemplateName) {
			r.Info("launch template name differs", "name", rollingUpgrade.NamespacedName(), "instance", instanceID)
			return true
		} else if !strings.EqualFold(instanceTemplateVersion, templateVersion) {
			r.Info("launch template version differs", "name", rollingUpgrade.NamespacedName(), "instance", instanceID)
			return true
		}

	} else if scalingGroup.MixedInstancesPolicy != nil {
		if instance.LaunchTemplate == nil {
			r.Info("launch template name differs", "name", rollingUpgrade.NamespacedName(), "instance", instanceID)
			return true
		}

		var (
			launchTemplateName      = aws.StringValue(scalingGroup.MixedInstancesPolicy.LaunchTemplate.LaunchTemplateSpecification.LaunchTemplateName)
			instanceTemplateName    = aws.StringValue(instance.LaunchTemplate.LaunchTemplateName)
			instanceTemplateVersion = aws.StringValue(instance.LaunchTemplate.Version)
			templateVersion         = awsprovider.GetTemplateLatestVersion(r.Cloud.LaunchTemplates, launchTemplateName)
		)

		if !strings.EqualFold(launchTemplateName, instanceTemplateName) {
			r.Info("launch template name differs", "name", rollingUpgrade.NamespacedName(), "instance", instanceID)
			return true
		} else if !strings.EqualFold(instanceTemplateVersion, templateVersion) {
			r.Info("launch template version differs", "name", rollingUpgrade.NamespacedName(), "instance", instanceID)
			return true
		}
	}

	r.Info("node refresh not required", "name", rollingUpgrade.NamespacedName(), "instance", instanceID)
	return false
}

func (r *RollingUpgradeReconciler) IsScalingGroupDrifted(rollingUpgrade *v1alpha1.RollingUpgrade) bool {
	r.Info("checking if rolling upgrade is completed", "name", rollingUpgrade.NamespacedName())
	scalingGroup := awsprovider.SelectScalingGroup(rollingUpgrade.ScalingGroupName(), r.Cloud.ScalingGroups)
	for _, instance := range scalingGroup.Instances {
		if r.IsInstanceDrifted(rollingUpgrade, instance) {
			return true
		}
	}
	return false
}

func (r *RollingUpgradeReconciler) DesiredNodesReady(rollingUpgrade *v1alpha1.RollingUpgrade) bool {
	var (
		scalingGroup     = awsprovider.SelectScalingGroup(rollingUpgrade.ScalingGroupName(), r.Cloud.ScalingGroups)
		desiredInstances = aws.Int64Value(scalingGroup.DesiredCapacity)
		readyNodes       = 0
	)

	// wait for desired instances
	inServiceInstances := awsprovider.GetInServiceInstances(scalingGroup)
	if len(inServiceInstances) != int(desiredInstances) {
		r.Info("new instance is yet to join the scaling group")
		return false
	}

	// wait for desired nodes
	for _, node := range r.Cloud.ClusterNodes.Items {
		instanceID := kubeprovider.GetNodeInstanceID(node)
		if common.ContainsEqualFold(inServiceInstances, instanceID) && kubeprovider.IsNodeReady(node) && kubeprovider.IsNodePassesReadinessGates(node, rollingUpgrade.Spec.ReadinessGates) {
			readyNodes++
		}
	}
	if readyNodes != int(desiredInstances) {
		r.Info("new node is yet to join the cluster")
		return false
	}

	return true
}
