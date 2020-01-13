package controllers

import (
	"github.com/aws/aws-sdk-go/service/autoscaling"
)

// launchDefinition describes how instances are launched in ASG.
// Supports LaunchConfiguration and LaunchTemplate.
type launchDefinition struct {
	// launchConfigurationName is name of LaunchConfiguration used by ASG.
	// +optional
	launchConfigurationName *string
	// launchTemplate is Launch template definition used for ASG.
	// +optional
	launchTemplate *autoscaling.LaunchTemplateSpecification
}

func NewLaunchDefinition(asg *autoscaling.Group) *launchDefinition {
	return &launchDefinition{
		launchConfigurationName: asg.LaunchConfigurationName,
		launchTemplate:          asg.LaunchTemplate,
	}
}
