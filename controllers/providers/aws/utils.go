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
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

var (
	TerminatingInstanceStates = []string{
		string(types.LifecycleStateTerminating),
		string(types.LifecycleStateTerminatingWait),
		string(types.LifecycleStateTerminatingProceed),
		string(types.LifecycleStateTerminated),
		string(types.LifecycleStateWarmedTerminating),
		string(types.LifecycleStateWarmedTerminatingWait),
		string(types.LifecycleStateWarmedTerminatingProceed),
		string(types.LifecycleStateWarmedTerminated),
	}
	// Instance standBy limit is enforced by AWS EnterStandBy API
	InstanceStandByLimit = 19
)

type AmazonClientSet struct {
	AsgClient AutoScalingAPI
	Ec2Client EC2API
}

func DeriveRegion() (string, error) {
	if region := os.Getenv("AWS_REGION"); region != "" {
		return region, nil
	}

	ctx := context.TODO()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := imds.NewFromConfig(cfg)
	result, err := client.GetRegion(ctx, &imds.GetRegionInput{})
	if err != nil {
		return "", fmt.Errorf("cannot reach ec2metadata, if running locally export AWS_REGION: %w", err)
	}
	return result.Region, nil
}

func SelectScalingGroup(name string, groups []types.AutoScalingGroup) *types.AutoScalingGroup {
	for _, group := range groups {
		groupName := aws.ToString(group.AutoScalingGroupName)
		if strings.EqualFold(groupName, name) {
			return &group
		}
	}
	return &types.AutoScalingGroup{}
}

func SelectScalingGroupInstance(instanceID string, group *types.AutoScalingGroup) *types.Instance {
	for _, instance := range group.Instances {
		selectedID := aws.ToString(instance.InstanceId)
		if strings.EqualFold(instanceID, selectedID) {
			return &instance
		}
	}
	return &types.Instance{}
}

func GetScalingAZs(instances []types.Instance) []string {
	AZs := make([]string, 0)
	for _, instance := range instances {
		AZ := aws.ToString(instance.AvailabilityZone)
		AZs = append(AZs, AZ)
	}
	sort.Strings(AZs)
	return AZs
}

func GetInstanceIDs(instances []*types.Instance) []string {
	IDs := make([]string, 0)
	for _, instance := range instances {
		ID := aws.ToString(instance.InstanceId)
		IDs = append(IDs, ID)
	}
	sort.Strings(IDs)
	return IDs
}

func GetInstanceIDsFromPointers(instances []*types.Instance) []string {
	IDs := make([]string, 0)
	for _, instance := range instances {
		ID := aws.ToString(instance.InstanceId)
		IDs = append(IDs, ID)
	}
	sort.Strings(IDs)
	return IDs
}

func GetInServiceInstanceIDsFromPointers(instances []*types.Instance) []string {
	var inServiceInstanceIDs []string
	for _, instance := range instances {
		if instance.LifecycleState == types.LifecycleStateInService {
			inServiceInstanceIDs = append(inServiceInstanceIDs, aws.ToString(instance.InstanceId))
		}
	}
	return inServiceInstanceIDs
}

func GetTemplateLatestVersion(templates []ec2types.LaunchTemplate, templateName string) string {
	for _, template := range templates {
		name := aws.ToString(template.LaunchTemplateName)
		if strings.EqualFold(name, templateName) {
			versionInt := aws.ToInt64(template.LatestVersionNumber)
			return strconv.FormatInt(versionInt, 10)
		}
	}
	return "0"
}

func GetInServiceInstanceIDs(instances []types.Instance) []string {
	var inServiceInstanceIDs []string
	for _, instance := range instances {
		if instance.LifecycleState == types.LifecycleStateInService {
			inServiceInstanceIDs = append(inServiceInstanceIDs, aws.ToString(instance.InstanceId))
		}
	}
	return inServiceInstanceIDs
}
