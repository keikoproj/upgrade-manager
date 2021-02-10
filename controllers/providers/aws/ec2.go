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

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
)

func (a *AmazonClientSet) DescribeLaunchTemplates() ([]*ec2.LaunchTemplate, error) {
	launchTemplates := []*ec2.LaunchTemplate{}
	err := a.Ec2Client.DescribeLaunchTemplatesPages(&ec2.DescribeLaunchTemplatesInput{}, func(page *ec2.DescribeLaunchTemplatesOutput, lastPage bool) bool {
		launchTemplates = append(launchTemplates, page.LaunchTemplates...)
		return page.NextToken != nil
	})
	if err != nil {
		return launchTemplates, err
	}
	return launchTemplates, nil
}

func (a *AmazonClientSet) DescribeTaggedInstanceIDs(tagKey, tagValue string) ([]string, error) {
	instances := []string{}
	key := fmt.Sprintf("tag:%v", tagKey)
	input := &ec2.DescribeInstancesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String(key),
				Values: aws.StringSlice([]string{tagValue}),
			},
			{
				Name:   aws.String("instance-state-name"),
				Values: aws.StringSlice([]string{"pending", "running"}),
			},
		},
	}

	err := a.Ec2Client.DescribeInstancesPages(input, func(page *ec2.DescribeInstancesOutput, lastPage bool) bool {
		for _, res := range page.Reservations {
			for _, instance := range res.Instances {
				instances = append(instances, aws.StringValue(instance.InstanceId))
			}
		}
		return page.NextToken != nil
	})
	return instances, err
}

func (a *AmazonClientSet) TagEC2instance(instanceID, tagKey, tagValue string) error {
	input := &ec2.CreateTagsInput{
		Resources: aws.StringSlice([]string{instanceID}),
		Tags: []*ec2.Tag{
			{
				Key:   aws.String(tagKey),
				Value: aws.String(tagValue),
			},
		},
	}
	_, err := a.Ec2Client.CreateTags(input)
	return err
}
