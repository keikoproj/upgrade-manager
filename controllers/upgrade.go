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

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/keikoproj/upgrade-manager/api/v1alpha1"
	awsprovider "github.com/keikoproj/upgrade-manager/controllers/providers/aws"
	kubeprovider "github.com/keikoproj/upgrade-manager/controllers/providers/kubernetes"
)

// TODO: main node rotation logic
func (r *RollingUpgradeReconciler) RotateNodes(rollingUpgrade *v1alpha1.RollingUpgrade) error {

	return nil
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
