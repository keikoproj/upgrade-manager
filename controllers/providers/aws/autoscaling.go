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

package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
)

func (a *AmazonClientSet) DescribeScalingGroups() ([]types.AutoScalingGroup, error) {
	ctx := context.TODO()
	var scalingGroups []types.AutoScalingGroup

	// For concrete clients, we need to handle pagination differently
	// This method will work with both real clients and mocks
	input := &autoscaling.DescribeAutoScalingGroupsInput{}

	for {
		output, err := a.AsgClient.DescribeAutoScalingGroups(ctx, input)
		if err != nil {
			return scalingGroups, err
		}

		scalingGroups = append(scalingGroups, output.AutoScalingGroups...)

		if output.NextToken == nil {
			break
		}
		input.NextToken = output.NextToken
	}

	return scalingGroups, nil
}

func (a *AmazonClientSet) TerminateInstance(instance *types.Instance) error {
	ctx := context.TODO()
	instanceID := aws.ToString(instance.InstanceId)
	input := &autoscaling.TerminateInstanceInAutoScalingGroupInput{
		InstanceId:                     aws.String(instanceID),
		ShouldDecrementDesiredCapacity: aws.Bool(false),
	}

	if _, err := a.AsgClient.TerminateInstanceInAutoScalingGroup(ctx, input); err != nil {
		return err
	}
	return nil
}

func (a *AmazonClientSet) SetInstancesStandBy(instanceIDs []string, scalingGroupName string) error {
	ctx := context.TODO()
	input := &autoscaling.EnterStandbyInput{
		AutoScalingGroupName:           aws.String(scalingGroupName),
		InstanceIds:                    instanceIDs,
		ShouldDecrementDesiredCapacity: aws.Bool(false),
	}
	_, err := a.AsgClient.EnterStandby(ctx, input)
	return err
}
