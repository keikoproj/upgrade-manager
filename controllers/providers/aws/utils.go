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

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/autoscaling/autoscalingiface"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
)

type AmazonClientSet struct {
	AsgClient autoscalingiface.AutoScalingAPI
	Ec2Client ec2iface.EC2API
}

func DeriveRegion() (string, error) {

	if region := os.Getenv("AWS_REGION"); region != "" {
		return region, nil
	}

	var config aws.Config
	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
		Config:            config,
	}))
	c := ec2metadata.New(sess)
	region, err := c.Region()
	if err != nil {
		return "", fmt.Errorf("cannot reach ec2metadata, if running locally export AWS_REGION: %w", err)
	}
	return region, nil
}

func SelectScalingGroup(name string, groups []*autoscaling.Group) *autoscaling.Group {
	for _, group := range groups {
		groupName := aws.StringValue(group.AutoScalingGroupName)
		if strings.EqualFold(groupName, name) {
			return group
		}
	}
	return &autoscaling.Group{}
}

func SelectScalingGroupInstance(instanceID string, group *autoscaling.Group) *autoscaling.Instance {
	for _, instance := range group.Instances {
		selectedID := aws.StringValue(instance.InstanceId)
		if strings.EqualFold(instanceID, selectedID) {
			return instance
		}
	}
	return &autoscaling.Instance{}
}

func GetScalingAZs(instances []*autoscaling.Instance) []string {
	AZs := make([]string, 0)
	for _, instance := range instances {
		AZ := aws.StringValue(instance.AvailabilityZone)
		AZs = append(AZs, AZ)
	}
	sort.Strings(AZs)
	return AZs
}

// func SelectInstancesByAZ(instances []*autoscaling.Group) *autoscaling.Instance {
// 	for _, instance := range group.Instances {
// 		selectedID := aws.StringValue(instance.InstanceId)
// 		if strings.EqualFold(instanceID, selectedID) {
// 			return instance
// 		}
// 	}
// 	return &autoscaling.Instance{}
// }

// func ListScalingInstanceIDs(group *autoscaling.Group) []string {
// 	instanceIDs := make([]string, 0)
// 	for _, instance := range group.Instances {
// 		instanceID := aws.StringValue(instance.InstanceId)
// 		instanceIDs = append(instanceIDs, instanceID)
// 	}
// 	return instanceIDs
// }

func GetTemplateLatestVersion(templates []*ec2.LaunchTemplate, templateName string) string {
	for _, template := range templates {
		name := aws.StringValue(template.LaunchTemplateName)
		if strings.EqualFold(name, templateName) {
			versionInt := aws.Int64Value(template.LatestVersionNumber)
			return strconv.FormatInt(versionInt, 10)
		}
	}
	return "0"
}

func TagEC2instance(instanceID, tagKey, tagValue string, client ec2iface.EC2API) error {
	input := &ec2.CreateTagsInput{
		Resources: aws.StringSlice([]string{instanceID}),
		Tags: []*ec2.Tag{
			{
				Key:   aws.String(tagKey),
				Value: aws.String(tagValue),
			},
		},
	}
	_, err := client.CreateTags(input)
	return err
}
