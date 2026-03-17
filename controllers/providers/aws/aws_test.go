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
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// Simple mocks that focus on testing our business logic, not AWS SDK behavior
type mockAutoScalingClient struct {
	pages           []autoscaling.DescribeAutoScalingGroupsOutput
	calls           []autoscaling.DescribeAutoScalingGroupsInput
	shouldError     bool
	errorMsg        string
	terminateInputs []*autoscaling.TerminateInstanceInAutoScalingGroupInput
	standbyInputs   []*autoscaling.EnterStandbyInput
}

func (m *mockAutoScalingClient) DescribeAutoScalingGroups(ctx context.Context, params *autoscaling.DescribeAutoScalingGroupsInput, optFns ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	if m.shouldError {
		return nil, fmt.Errorf("%s", m.errorMsg)
	}

	// Track the calls to verify our pagination logic
	m.calls = append(m.calls, *params)

	pageIndex := len(m.calls) - 1
	if pageIndex >= len(m.pages) {
		return &autoscaling.DescribeAutoScalingGroupsOutput{}, nil
	}

	return &m.pages[pageIndex], nil
}

func (m *mockAutoScalingClient) TerminateInstanceInAutoScalingGroup(ctx context.Context, params *autoscaling.TerminateInstanceInAutoScalingGroupInput, optFns ...func(*autoscaling.Options)) (*autoscaling.TerminateInstanceInAutoScalingGroupOutput, error) {
	if m.shouldError {
		return nil, fmt.Errorf("%s", m.errorMsg)
	}
	m.terminateInputs = append(m.terminateInputs, params)
	return &autoscaling.TerminateInstanceInAutoScalingGroupOutput{}, nil
}

func (m *mockAutoScalingClient) EnterStandby(ctx context.Context, params *autoscaling.EnterStandbyInput, optFns ...func(*autoscaling.Options)) (*autoscaling.EnterStandbyOutput, error) {
	if m.shouldError {
		return nil, fmt.Errorf("%s", m.errorMsg)
	}
	m.standbyInputs = append(m.standbyInputs, params)
	return &autoscaling.EnterStandbyOutput{}, nil
}

type mockEC2Client struct {
	pages       []ec2.DescribeInstancesOutput
	calls       []ec2.DescribeInstancesInput
	shouldError bool
	errorMsg    string
	ltPages     []ec2.DescribeLaunchTemplatesOutput
	ltCalls     []ec2.DescribeLaunchTemplatesInput
}

func (m *mockEC2Client) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	if m.shouldError {
		return nil, fmt.Errorf("%s", m.errorMsg)
	}

	// Track the calls to verify our filter construction and pagination logic
	m.calls = append(m.calls, *params)

	pageIndex := len(m.calls) - 1
	if pageIndex >= len(m.pages) {
		return &ec2.DescribeInstancesOutput{}, nil
	}

	return &m.pages[pageIndex], nil
}

func (m *mockEC2Client) DescribeLaunchTemplates(ctx context.Context, params *ec2.DescribeLaunchTemplatesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplatesOutput, error) {
	if m.shouldError {
		return nil, fmt.Errorf("%s", m.errorMsg)
	}
	if len(m.ltPages) == 0 {
		return &ec2.DescribeLaunchTemplatesOutput{}, nil
	}
	m.ltCalls = append(m.ltCalls, *params)
	pageIndex := len(m.ltCalls) - 1
	if pageIndex >= len(m.ltPages) {
		return &ec2.DescribeLaunchTemplatesOutput{}, nil
	}
	return &m.ltPages[pageIndex], nil
}

func (m *mockEC2Client) CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	if m.shouldError {
		return nil, fmt.Errorf("%s", m.errorMsg)
	}
	return &ec2.CreateTagsOutput{}, nil
}

func (m *mockEC2Client) DeleteTags(ctx context.Context, params *ec2.DeleteTagsInput, optFns ...func(*ec2.Options)) (*ec2.DeleteTagsOutput, error) {
	if m.shouldError {
		return nil, fmt.Errorf("%s", m.errorMsg)
	}
	return &ec2.DeleteTagsOutput{}, nil
}

// Test our pagination logic - this is OUR code, not AWS SDK code
func TestDescribeScalingGroupsPagination(t *testing.T) {
	mockClient := &mockAutoScalingClient{
		pages: []autoscaling.DescribeAutoScalingGroupsOutput{
			{
				AutoScalingGroups: []types.AutoScalingGroup{
					{AutoScalingGroupName: aws.String("asg-1")},
				},
				NextToken: aws.String("token-1"),
			},
			{
				AutoScalingGroups: []types.AutoScalingGroup{
					{AutoScalingGroupName: aws.String("asg-2")},
				},
				NextToken: nil, // Last page
			},
		},
	}

	clientSet := &AmazonClientSet{AsgClient: mockClient}
	asgs, err := clientSet.DescribeScalingGroups()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify our pagination logic collected all results
	if len(asgs) != 2 {
		t.Errorf("expected 2 ASGs, got %d", len(asgs))
	}

	// Verify our pagination logic made the right calls
	if len(mockClient.calls) != 2 {
		t.Errorf("expected 2 API calls, got %d", len(mockClient.calls))
	}

	// Verify our pagination logic passed the token correctly
	if mockClient.calls[0].NextToken != nil {
		t.Error("first call should not have NextToken")
	}
	if mockClient.calls[1].NextToken == nil || *mockClient.calls[1].NextToken != "token-1" {
		t.Error("second call should have NextToken from first response")
	}
}

// Test our filter construction logic - this is OUR code
func TestDescribeTaggedInstanceIDsFilters(t *testing.T) {
	mockClient := &mockEC2Client{
		pages: []ec2.DescribeInstancesOutput{
			{
				Reservations: []ec2types.Reservation{
					{
						Instances: []ec2types.Instance{
							{InstanceId: aws.String("i-1234567890abcdef0")},
							{InstanceId: aws.String("i-1234567890abcdef1")},
						},
					},
				},
			},
		},
	}

	clientSet := &AmazonClientSet{Ec2Client: mockClient}
	instances, err := clientSet.DescribeTaggedInstanceIDs("Environment", "production")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify our data extraction logic
	if len(instances) != 2 {
		t.Errorf("expected 2 instances, got %d", len(instances))
	}

	// Verify our filter construction logic
	if len(mockClient.calls) != 1 {
		t.Fatalf("expected 1 API call, got %d", len(mockClient.calls))
	}

	filters := mockClient.calls[0].Filters
	if len(filters) != 2 {
		t.Errorf("expected 2 filters, got %d", len(filters))
	}

	// Verify our tag filter construction
	tagFilter := filters[0]
	if *tagFilter.Name != "tag:Environment" {
		t.Errorf("expected tag filter name 'tag:Environment', got %s", *tagFilter.Name)
	}
	if len(tagFilter.Values) != 1 || tagFilter.Values[0] != "production" {
		t.Errorf("expected tag filter value 'production', got %v", tagFilter.Values)
	}

	// Verify our instance state filter construction
	stateFilter := filters[1]
	if *stateFilter.Name != "instance-state-name" {
		t.Errorf("expected state filter name 'instance-state-name', got %s", *stateFilter.Name)
	}
	expectedStates := []string{"pending", "running"}
	if len(stateFilter.Values) != 2 || stateFilter.Values[0] != expectedStates[0] || stateFilter.Values[1] != expectedStates[1] {
		t.Errorf("expected state filter values %v, got %v", expectedStates, stateFilter.Values)
	}
}

// Test our parameter mapping logic - this is OUR business rule
func TestTerminateInstanceParameters(t *testing.T) {
	mockClient := &mockAutoScalingClient{}
	clientSet := &AmazonClientSet{AsgClient: mockClient}

	instance := &types.Instance{
		InstanceId: aws.String("i-1234567890abcdef0"),
	}

	err := clientSet.TerminateInstance(instance)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mockClient.terminateInputs) != 1 {
		t.Fatalf("expected 1 terminate call, got %d", len(mockClient.terminateInputs))
	}
	got := mockClient.terminateInputs[0]
	if got.InstanceId == nil || *got.InstanceId != "i-1234567890abcdef0" {
		t.Errorf("unexpected instance id: %v", got.InstanceId)
	}
	if got.ShouldDecrementDesiredCapacity == nil || *got.ShouldDecrementDesiredCapacity != false {
		t.Errorf("ShouldDecrementDesiredCapacity should be false")
	}
}

// Test our parameter mapping logic for standby
func TestSetInstancesStandByParameters(t *testing.T) {
	mockClient := &mockAutoScalingClient{}
	clientSet := &AmazonClientSet{AsgClient: mockClient}

	err := clientSet.SetInstancesStandBy([]string{"i-123", "i-456"}, "test-asg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mockClient.standbyInputs) != 1 {
		t.Fatalf("expected 1 standby call, got %d", len(mockClient.standbyInputs))
	}
	got := mockClient.standbyInputs[0]
	if got.AutoScalingGroupName == nil || *got.AutoScalingGroupName != "test-asg" {
		t.Errorf("unexpected asg name: %v", got.AutoScalingGroupName)
	}
	if len(got.InstanceIds) != 2 || got.InstanceIds[0] != "i-123" || got.InstanceIds[1] != "i-456" {
		t.Errorf("unexpected instance ids: %v", got.InstanceIds)
	}
	if got.ShouldDecrementDesiredCapacity == nil || *got.ShouldDecrementDesiredCapacity != false {
		t.Errorf("ShouldDecrementDesiredCapacity should be false")
	}
}

// Test error handling in AWS provider methods
func TestProviderErrorHandling(t *testing.T) {
	t.Run("DescribeScalingGroups error", func(t *testing.T) {
		mockClient := &mockAutoScalingClient{shouldError: true, errorMsg: "AWS API error"}
		clientSet := &AmazonClientSet{AsgClient: mockClient}
		_, err := clientSet.DescribeScalingGroups()
		if err == nil || !strings.Contains(err.Error(), "AWS API error") {
			t.Fatalf("expected AWS API error, got: %v", err)
		}
	})

	t.Run("DescribeLaunchTemplates error", func(t *testing.T) {
		mockClient := &mockEC2Client{shouldError: true, errorMsg: "EC2 API error"}
		clientSet := &AmazonClientSet{Ec2Client: mockClient}
		_, err := clientSet.DescribeLaunchTemplates()
		if err == nil || !strings.Contains(err.Error(), "EC2 API error") {
			t.Fatalf("expected EC2 API error, got: %v", err)
		}
	})

	t.Run("DescribeTaggedInstanceIDs error", func(t *testing.T) {
		mockClient := &mockEC2Client{shouldError: true, errorMsg: "EC2 API error"}
		clientSet := &AmazonClientSet{Ec2Client: mockClient}
		_, err := clientSet.DescribeTaggedInstanceIDs("Environment", "production")
		if err == nil || !strings.Contains(err.Error(), "EC2 API error") {
			t.Fatalf("expected EC2 API error, got: %v", err)
		}
	})

	t.Run("TerminateInstance error", func(t *testing.T) {
		mockClient := &mockAutoScalingClient{shouldError: true, errorMsg: "Termination failed"}
		clientSet := &AmazonClientSet{AsgClient: mockClient}
		instance := &types.Instance{InstanceId: aws.String("i-1234567890abcdef0")}
		err := clientSet.TerminateInstance(instance)
		if err == nil || !strings.Contains(err.Error(), "Termination failed") {
			t.Fatalf("expected Termination failed, got: %v", err)
		}
	})

	t.Run("SetInstancesStandBy error", func(t *testing.T) {
		mockClient := &mockAutoScalingClient{shouldError: true, errorMsg: "Standby operation failed"}
		clientSet := &AmazonClientSet{AsgClient: mockClient}
		err := clientSet.SetInstancesStandBy([]string{"i-123", "i-456"}, "test-asg")
		if err == nil || !strings.Contains(err.Error(), "Standby operation failed") {
			t.Fatalf("expected Standby operation failed, got: %v", err)
		}
	})

	t.Run("Tag/Untag EC2 error", func(t *testing.T) {
		mockClient := &mockEC2Client{shouldError: true, errorMsg: "Tagging failed"}
		clientSet := &AmazonClientSet{Ec2Client: mockClient}
		if err := clientSet.TagEC2instances([]string{"i-123"}, "Environment", "test"); err == nil || !strings.Contains(err.Error(), "Tagging failed") {
			t.Fatalf("expected Tagging failed, got: %v", err)
		}
		mockClient.errorMsg = "Untagging failed"
		if err := clientSet.UntagEC2instances([]string{"i-123"}, "Environment", "test"); err == nil || !strings.Contains(err.Error(), "Untagging failed") {
			t.Fatalf("expected Untagging failed, got: %v", err)
		}
	})
}

// Test edge cases for business logic
func TestDescribeInstancesWithoutTagValueComplexScenarios(t *testing.T) {
	tests := []struct {
		name      string
		instances []ec2types.Instance
		tagKey    string
		tagValue  string
		expected  []string
	}{
		{
			name: "instances with no tags",
			instances: []ec2types.Instance{
				{InstanceId: aws.String("i-1"), Tags: []ec2types.Tag{}},
				{InstanceId: aws.String("i-2"), Tags: nil},
			},
			tagKey:   "Environment",
			tagValue: "production",
			expected: []string{"i-1", "i-2"},
		},
		{
			name: "instances with different tag values",
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-1"),
					Tags: []ec2types.Tag{
						{Key: aws.String("Environment"), Value: aws.String("staging")},
					},
				},
				{
					InstanceId: aws.String("i-2"),
					Tags: []ec2types.Tag{
						{Key: aws.String("Environment"), Value: aws.String("production")},
					},
				},
			},
			tagKey:   "Environment",
			tagValue: "production",
			expected: []string{"i-1"},
		},
		{
			name: "instances with multiple tags",
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-1"),
					Tags: []ec2types.Tag{
						{Key: aws.String("Environment"), Value: aws.String("production")},
						{Key: aws.String("Team"), Value: aws.String("backend")},
					},
				},
				{
					InstanceId: aws.String("i-2"),
					Tags: []ec2types.Tag{
						{Key: aws.String("Team"), Value: aws.String("frontend")},
					},
				},
			},
			tagKey:   "Environment",
			tagValue: "production",
			expected: []string{"i-2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockEC2Client{
				pages: []ec2.DescribeInstancesOutput{
					{
						Reservations: []ec2types.Reservation{
							{Instances: tt.instances},
						},
					},
				},
			}
			clientSet := &AmazonClientSet{Ec2Client: mockClient}

			result, err := clientSet.DescribeInstancesWithoutTagValue(tt.tagKey, tt.tagValue)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d instances, got %d", len(tt.expected), len(result))
			}

			for i, expected := range tt.expected {
				if i >= len(result) || result[i] != expected {
					t.Errorf("expected instance %s at index %d, got %s", expected, i, result[i])
				}
			}
		})
	}
}

func TestDescribeLaunchTemplatesPagination(t *testing.T) {
	mockClient := &mockEC2Client{
		ltPages: []ec2.DescribeLaunchTemplatesOutput{
			{
				LaunchTemplates: []ec2types.LaunchTemplate{{LaunchTemplateName: aws.String("lt-1")}},
				NextToken:       aws.String("t1"),
			},
			{
				LaunchTemplates: []ec2types.LaunchTemplate{{LaunchTemplateName: aws.String("lt-2")}},
				NextToken:       nil,
			},
		},
	}
	clientSet := &AmazonClientSet{Ec2Client: mockClient}

	templates, err := clientSet.DescribeLaunchTemplates()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(templates) != 2 {
		t.Errorf("expected 2 templates, got %d", len(templates))
	}
	if len(mockClient.ltCalls) != 2 {
		t.Errorf("expected 2 API calls, got %d", len(mockClient.ltCalls))
	}
	if mockClient.ltCalls[0].NextToken != nil {
		t.Error("first call should not have NextToken")
	}
	if mockClient.ltCalls[1].NextToken == nil || *mockClient.ltCalls[1].NextToken != "t1" {
		t.Error("second call should carry forward NextToken")
	}
}
