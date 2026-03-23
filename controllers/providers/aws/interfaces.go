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

	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// AutoScalingAPI defines the interface for AutoScaling operations used by upgrade-manager
type AutoScalingAPI interface {
	DescribeAutoScalingGroups(ctx context.Context, params *autoscaling.DescribeAutoScalingGroupsInput, optFns ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error)
	TerminateInstanceInAutoScalingGroup(ctx context.Context, params *autoscaling.TerminateInstanceInAutoScalingGroupInput, optFns ...func(*autoscaling.Options)) (*autoscaling.TerminateInstanceInAutoScalingGroupOutput, error)
	EnterStandby(ctx context.Context, params *autoscaling.EnterStandbyInput, optFns ...func(*autoscaling.Options)) (*autoscaling.EnterStandbyOutput, error)
}

// EC2API defines the interface for EC2 operations used by upgrade-manager
type EC2API interface {
	DescribeLaunchTemplates(ctx context.Context, params *ec2.DescribeLaunchTemplatesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplatesOutput, error)
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error)
	DeleteTags(ctx context.Context, params *ec2.DeleteTagsInput, optFns ...func(*ec2.Options)) (*ec2.DeleteTagsOutput, error)
}

// AutoScalingPaginatorAPI defines the interface for AutoScaling paginators
type AutoScalingPaginatorAPI interface {
	HasMorePages() bool
	NextPage(ctx context.Context, optFns ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error)
}

// EC2PaginatorAPI defines the interface for EC2 paginators
type EC2PaginatorAPI interface {
	HasMorePages() bool
	NextPage(ctx context.Context, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// EC2LaunchTemplatePaginatorAPI defines the interface for EC2 launch template paginators
type EC2LaunchTemplatePaginatorAPI interface {
	HasMorePages() bool
	NextPage(ctx context.Context, optFns ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplatesOutput, error)
}
