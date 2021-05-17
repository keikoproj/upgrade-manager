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
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/autoscaling"
)

var (
	TerminatingInstanceStates = []string{
		autoscaling.LifecycleStateTerminating,
		autoscaling.LifecycleStateTerminatingWait,
		autoscaling.LifecycleStateTerminatingProceed,
	}
)

func (a *AmazonClientSet) DescribeScalingGroups() ([]*autoscaling.Group, error) {
	scalingGroups := []*autoscaling.Group{}
	err := a.AsgClient.DescribeAutoScalingGroupsPages(&autoscaling.DescribeAutoScalingGroupsInput{}, func(page *autoscaling.DescribeAutoScalingGroupsOutput, lastPage bool) bool {
		scalingGroups = append(scalingGroups, page.AutoScalingGroups...)
		return page.NextToken != nil
	})
	if err != nil {
		return scalingGroups, err
	}
	return scalingGroups, nil
}

func (a *AmazonClientSet) TerminateInstance(instance *autoscaling.Instance) error {
	instanceID := aws.StringValue(instance.InstanceId)
	input := &autoscaling.TerminateInstanceInAutoScalingGroupInput{
		InstanceId:                     aws.String(instanceID),
		ShouldDecrementDesiredCapacity: aws.Bool(false),
	}

	if _, err := a.AsgClient.TerminateInstanceInAutoScalingGroup(input); err != nil {
		return err
	}
	return nil
}

func (a *AmazonClientSet) SetInstanceStandBy(instances []*autoscaling.Instance, scalingGroupName string) (int, error) {
	var instanceIDs []string
	for _, instance := range instances {
		if aws.StringValue(instance.LifecycleState) == autoscaling.LifecycleStateInService {
			instanceIDs = append(instanceIDs, aws.StringValue(instance.InstanceId))
		}
	}
	if len(instanceIDs) > 0 {
		input := &autoscaling.EnterStandbyInput{
			AutoScalingGroupName:           aws.String(scalingGroupName),
			InstanceIds:                    aws.StringSlice(instanceIDs),
			ShouldDecrementDesiredCapacity: aws.Bool(false),
		}
		if _, err := a.AsgClient.EnterStandby(input); err != nil {
			return len(instanceIDs), err
		}
	}

	return len(instanceIDs), nil
}
