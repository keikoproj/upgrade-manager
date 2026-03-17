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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func (a *AmazonClientSet) DescribeLaunchTemplates() ([]types.LaunchTemplate, error) {
	ctx := context.TODO()
	var launchTemplates []types.LaunchTemplate

	input := &ec2.DescribeLaunchTemplatesInput{}

	for {
		output, err := a.Ec2Client.DescribeLaunchTemplates(ctx, input)
		if err != nil {
			return launchTemplates, err
		}

		launchTemplates = append(launchTemplates, output.LaunchTemplates...)

		if output.NextToken == nil {
			break
		}
		input.NextToken = output.NextToken
	}

	return launchTemplates, nil
}

func (a *AmazonClientSet) DescribeTaggedInstanceIDs(tagKey, tagValue string) ([]string, error) {
	ctx := context.TODO()
	var instances []string
	key := fmt.Sprintf("tag:%v", tagKey)
	input := &ec2.DescribeInstancesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String(key),
				Values: []string{tagValue},
			},
			{
				Name:   aws.String("instance-state-name"),
				Values: []string{"pending", "running"},
			},
		},
	}

	for {
		output, err := a.Ec2Client.DescribeInstances(ctx, input)
		if err != nil {
			return instances, err
		}

		for _, res := range output.Reservations {
			for _, instance := range res.Instances {
				instances = append(instances, aws.ToString(instance.InstanceId))
			}
		}

		if output.NextToken == nil {
			break
		}
		input.NextToken = output.NextToken
	}
	return instances, nil
}

func (a *AmazonClientSet) DescribeInstancesWithoutTagValue(tagKey string, tagValue string) ([]string, error) {
	ctx := context.TODO()
	var instances []string
	input := &ec2.DescribeInstancesInput{}

	for {
		output, err := a.Ec2Client.DescribeInstances(ctx, input)
		if err != nil {
			return instances, err
		}

		for _, res := range output.Reservations {
			for _, instance := range res.Instances {
				tagAndValueIsPresent := false
				for _, t := range instance.Tags {
					if aws.ToString(t.Key) == tagKey && aws.ToString(t.Value) == tagValue {
						tagAndValueIsPresent = true
						break
					}
				}
				if !tagAndValueIsPresent {
					instances = append(instances, aws.ToString(instance.InstanceId))
				}
			}
		}

		if output.NextToken == nil {
			break
		}
		input.NextToken = output.NextToken
	}
	return instances, nil
}

func (a *AmazonClientSet) TagEC2instances(instanceIDs []string, tagKey, tagValue string) error {
	ctx := context.TODO()
	input := &ec2.CreateTagsInput{
		Resources: instanceIDs,
		Tags: []types.Tag{
			{
				Key:   aws.String(tagKey),
				Value: aws.String(tagValue),
			},
		},
	}
	_, err := a.Ec2Client.CreateTags(ctx, input)
	return err
}

func (a *AmazonClientSet) UntagEC2instances(instanceIDs []string, tagKey, tagValue string) error {
	ctx := context.TODO()
	input := &ec2.DeleteTagsInput{
		Resources: instanceIDs,
		Tags: []types.Tag{
			{
				Key:   aws.String(tagKey),
				Value: aws.String(tagValue),
			},
		},
	}
	_, err := a.Ec2Client.DeleteTags(ctx, input)
	return err
}
