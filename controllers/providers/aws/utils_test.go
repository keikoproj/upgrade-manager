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
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestSelectScalingGroupWithNonExistentGroup(t *testing.T) {
	groups := []types.AutoScalingGroup{
		{AutoScalingGroupName: aws.String("asg-1")},
		{AutoScalingGroupName: aws.String("asg-2")},
	}

	result := SelectScalingGroup("non-existent", groups)

	// Should return empty AutoScalingGroup
	if result.AutoScalingGroupName != nil {
		t.Errorf("expected empty AutoScalingGroup, got %v", result)
	}
}

func TestSelectScalingGroupInstance(t *testing.T) {
	group := &types.AutoScalingGroup{
		Instances: []types.Instance{
			{InstanceId: aws.String("i-123")},
			{InstanceId: aws.String("i-456")},
		},
	}

	tests := []struct {
		name       string
		instanceID string
		expectNil  bool
	}{
		{"existing instance", "i-123", false},
		{"non-existent instance", "i-999", true},
		{"empty instance ID", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SelectScalingGroupInstance(tt.instanceID, group)

			if tt.expectNil {
				if result.InstanceId != nil {
					t.Errorf("expected empty Instance, got %v", result)
				}
			} else {
				if result.InstanceId == nil || *result.InstanceId != tt.instanceID {
					t.Errorf("expected instance %s, got %v", tt.instanceID, result)
				}
			}
		})
	}
}

func TestGetInstanceIDsWithEmptySlice(t *testing.T) {
	var instances []*types.Instance
	result := GetInstanceIDsFromPointers(instances)

	if len(result) != 0 {
		t.Errorf("expected empty slice, got %v", result)
	}
}

func TestGetInServiceInstanceIDsWithAllTerminating(t *testing.T) {
	instances := []*types.Instance{
		{
			InstanceId:     aws.String("i-123"),
			LifecycleState: types.LifecycleStateTerminating,
		},
		{
			InstanceId:     aws.String("i-456"),
			LifecycleState: types.LifecycleStateTerminated,
		},
	}

	result := GetInServiceInstanceIDsFromPointers(instances)

	if len(result) != 0 {
		t.Errorf("expected no in-service instances, got %v", result)
	}
}

func TestGetInServiceInstanceIDsWithMixedStates(t *testing.T) {
	instances := []*types.Instance{
		{
			InstanceId:     aws.String("i-123"),
			LifecycleState: types.LifecycleStateInService,
		},
		{
			InstanceId:     aws.String("i-456"),
			LifecycleState: types.LifecycleStateTerminating,
		},
		{
			InstanceId:     aws.String("i-789"),
			LifecycleState: types.LifecycleStateInService,
		},
	}

	result := GetInServiceInstanceIDsFromPointers(instances)

	expected := []string{"i-123", "i-789"}
	if len(result) != len(expected) {
		t.Errorf("expected %d instances, got %d", len(expected), len(result))
	}

	for i, expectedID := range expected {
		if i >= len(result) || result[i] != expectedID {
			t.Errorf("expected instance %s at index %d, got %s", expectedID, i, result[i])
		}
	}
}

func TestGetTemplateLatestVersionWithNonExistentTemplate(t *testing.T) {
	templates := []ec2types.LaunchTemplate{
		{
			LaunchTemplateName:  aws.String("template-1"),
			LatestVersionNumber: aws.Int64(5),
		},
	}

	result := GetTemplateLatestVersion(templates, "non-existent")

	if result != "0" {
		t.Errorf("expected '0' for non-existent template, got %s", result)
	}
}

func TestGetTemplateLatestVersionWithExistingTemplate(t *testing.T) {
	templates := []ec2types.LaunchTemplate{
		{
			LaunchTemplateName:  aws.String("template-1"),
			LatestVersionNumber: aws.Int64(5),
		},
		{
			LaunchTemplateName:  aws.String("template-2"),
			LatestVersionNumber: aws.Int64(12),
		},
	}

	result := GetTemplateLatestVersion(templates, "template-2")

	if result != "12" {
		t.Errorf("expected '12' for template-2, got %s", result)
	}
}

func TestGetScalingAZsWithEmptyInstances(t *testing.T) {
	var instances []types.Instance
	result := GetScalingAZs(instances)

	if len(result) != 0 {
		t.Errorf("expected empty slice, got %v", result)
	}
}

func TestGetScalingAZsWithDuplicates(t *testing.T) {
	instances := []types.Instance{
		{AvailabilityZone: aws.String("us-west-2a")},
		{AvailabilityZone: aws.String("us-west-2b")},
		{AvailabilityZone: aws.String("us-west-2a")},
		{AvailabilityZone: aws.String("us-west-2c")},
	}

	result := GetScalingAZs(instances)

	// Should be sorted and contain duplicates
	expected := []string{"us-west-2a", "us-west-2a", "us-west-2b", "us-west-2c"}
	if len(result) != len(expected) {
		t.Errorf("expected %d AZs, got %d", len(expected), len(result))
	}

	for i, expectedAZ := range expected {
		if i >= len(result) || result[i] != expectedAZ {
			t.Errorf("expected AZ %s at index %d, got %s", expectedAZ, i, result[i])
		}
	}
}
