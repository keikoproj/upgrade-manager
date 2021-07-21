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
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/keikoproj/upgrade-manager/api/v1alpha1"
	awsprovider "github.com/keikoproj/upgrade-manager/controllers/providers/aws"
	kubeprovider "github.com/keikoproj/upgrade-manager/controllers/providers/kubernetes"
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
	rotationTargets := r.SelectTargets(rollingUpgrade, scalingGroup)
	r.ReplaceNodeBatch(rollingUpgrade, rotationTargets)

	return nil
}

func (r *RollingUpgradeReconciler) ReplaceNodeBatch(rollingUpgrade *v1alpha1.RollingUpgrade, batch []*autoscaling.Instance) (bool, error) {
	var (
		mode = rollingUpgrade.StrategyMode()
	)

	switch mode {
	case v1alpha1.UpdateStrategyModeEager:
		for _, target := range batch {
			// Add in-progress tag

			// Standby

			// Wait for desired nodes

			// Issue drain/scripts concurrently - set lastDrainTime

			// Is drained?

			// Terminate - set lastTerminateTime
		}
	case v1alpha1.UpdateStrategyModeLazy:
		for _, target := range batch {
			// Add in-progress tag

			// Issue drain/scripts concurrently - set lastDrainTime

			// Is drained?

			// Terminate - set lastTerminateTime
		}
	}

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
