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
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
)

// TODO: Resource discovery for AWS & Kubernetes

type DiscoveredState struct {
	*RollingUpgradeAuthenticator
	logr.Logger
	ClusterNodes        []corev1.NodeList
	LaunchTemplates     []*ec2.LaunchTemplate
	ScalingGroups       []*autoscaling.Group
	InProgressInstances []*ec2.Instance
}

func NewDiscoveredState(auth *RollingUpgradeAuthenticator, logger logr.Logger) *DiscoveredState {
	return &DiscoveredState{
		RollingUpgradeAuthenticator: auth,
		Logger:                      logger,
	}
}

func (d *DiscoveredState) Discover() error {

	// DescribeLaunchTemplatesPages

	// DescribeAutoScalingGroupsPages

	// DescribeInstancesPages with filter upgrademgr.keikoproj.io/state=in-progress

	// List Nodes

	return nil
}

func (d *DiscoveredState) IsConfigurationDrift() bool {

	// Check if launch template / launch config mismatch from scaling group

	return false
}
