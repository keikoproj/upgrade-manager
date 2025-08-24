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
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"

	"github.com/pkg/errors"
)

var (
	instanceStateTagKey = "upgrademgr.keikoproj.io/state"
	inProgressTagValue  = "in-progress"
	failedDrainTagValue = "failed-drain"
)

type DiscoveredState struct {
	*RollingUpgradeAuthenticator
	logr.Logger
	ClusterNodes        []*corev1.Node
	LaunchTemplates     []ec2types.LaunchTemplate
	ScalingGroups       []types.AutoScalingGroup
	InProgressInstances []string
}

func NewDiscoveredState(auth *RollingUpgradeAuthenticator, logger logr.Logger) *DiscoveredState {
	return &DiscoveredState{
		RollingUpgradeAuthenticator: auth,
		Logger:                      logger,
	}
}

func (d *DiscoveredState) Discover() error {

	launchTemplates, err := d.AmazonClientSet.DescribeLaunchTemplates()
	if err != nil {
		return errors.Wrap(err, "failed to discover launch templates")
	}
	d.LaunchTemplates = launchTemplates

	scalingGroups, err := d.AmazonClientSet.DescribeScalingGroups()
	if err != nil {
		return errors.Wrap(err, "failed to discover scaling groups")
	}
	d.ScalingGroups = scalingGroups

	inProgressInstances, err := d.AmazonClientSet.DescribeTaggedInstanceIDs(instanceStateTagKey, inProgressTagValue)
	if err != nil {
		return errors.Wrap(err, "failed to discover ec2 instances")
	}
	d.InProgressInstances = inProgressInstances

	return nil
}
