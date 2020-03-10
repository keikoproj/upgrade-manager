package controllers

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"

	"log"
	"strconv"
	"strings"

	upgrademgrv1alpha1 "github.com/keikoproj/upgrade-manager/api/v1alpha1"
)

// getMaxUnavailable calculates and returns the maximum unavailable nodes
// takes an update strategy and total number of nodes as input
func getMaxUnavailable(strategy upgrademgrv1alpha1.UpdateStrategy, totalNodes int) int {
	maxUnavailable := 1
	if strategy.MaxUnavailable.Type == 0 {
		maxUnavailable = int(strategy.MaxUnavailable.IntVal)
	} else if strategy.MaxUnavailable.Type == 1 {
		strVallue := strategy.MaxUnavailable.StrVal
		intValue, _ := strconv.Atoi(strings.Trim(strVallue, "%"))
		maxUnavailable = int(float32(intValue) / float32(100) * float32(totalNodes))
	}
	// setting maxUnavailable to total number of nodes when maxUnavailable is greater than total node count
	if totalNodes < maxUnavailable {
		log.Printf("Reducing maxUnavailable count from %d to %d as total nodes count is %d",
			maxUnavailable, totalNodes, totalNodes)
		maxUnavailable = totalNodes
	}
	// maxUnavailable has to be atleast 1 when there are nodes in the ASG
	if totalNodes > 0 && maxUnavailable < 1 {
		maxUnavailable = 1
	}
	return maxUnavailable
}

func isNodeReady(node corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			if condition.Status == corev1.ConditionTrue {
				return true
			}
		}
	}
	return false
}

func getInServiceCount(instances []*autoscaling.Instance) int64 {
	var count int64
	for _, instance := range instances {
		if aws.StringValue(instance.LifecycleState) == autoscaling.LifecycleStateInService {
			count++
		}
	}
	return count
}

func getInServiceIds(instances []*autoscaling.Instance) []string {
	list := []string{}
	for _, instance := range instances {
		if aws.StringValue(instance.LifecycleState) == autoscaling.LifecycleStateInService {
			list = append(list, aws.StringValue(instance.InstanceId))
		}
	}
	return list
}

func getGroupInstanceState(group *autoscaling.Group, id string) (string, error) {
	for _, instance := range group.Instances {
		if aws.StringValue(instance.InstanceId) != id {
			continue
		}
		return aws.StringValue(instance.LifecycleState), nil
	}
	return "", errors.Errorf("could not get instance group state, instance %v not found", id)
}

func isInServiceLifecycleState(state string) bool {
	return state == autoscaling.LifecycleStateInService
}

func tagEC2instance(instanceID, tagKey, tagValue string, client ec2iface.EC2API) error {
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
	if err != nil {
		return err
	}
	return nil
}

func getTaggedInstances(tagKey, tagValue string, client ec2iface.EC2API) ([]string, error) {
	instances := []string{}
	key := fmt.Sprintf("tag:%v", tagKey)
	input := &ec2.DescribeInstancesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String(key),
				Values: aws.StringSlice([]string{tagValue}),
			},
		},
	}

	err := client.DescribeInstancesPages(input, func(page *ec2.DescribeInstancesOutput, lastPage bool) bool {
		for _, res := range page.Reservations {
			for _, instance := range res.Instances {
				instances = append(instances, aws.StringValue(instance.InstanceId))
			}
		}
		return page.NextToken != nil
	})
	if err != nil {
		return instances, err
	}
	return instances, nil
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

// getNextAvailableInstances checks the cluster state store for the instance state
// and returns the next set of instances available for update
func getNextAvailableInstances(
	asgName string,
	numberOfInstances int,
	instances []*autoscaling.Instance,
	state ClusterState) []*autoscaling.Instance {
	return getNextSetOfAvailableInstancesInAz(asgName, "", numberOfInstances, instances, state)
}

// getNextSetOfAvailableInstancesInAz checks the cluster state store for the instance state
// and returns the next set of instances available for update in the given AX
func getNextSetOfAvailableInstancesInAz(
	asgName string,
	azName string,
	numberOfInstances int,
	instances []*autoscaling.Instance,
	state ClusterState,
) []*autoscaling.Instance {

	var instancesForUpdate []*autoscaling.Instance
	for instancesFound := 0; instancesFound < numberOfInstances; {
		instanceId := state.getNextAvailableInstanceIdInAz(asgName, azName)
		if len(instanceId) == 0 {
			// All instances are updated, no more instance to update in this AZ
			break
		}

		// check if the instance picked is part of ASG
		for _, instance := range instances {
			if *instance.InstanceId == instanceId {
				instancesForUpdate = append(instancesForUpdate, instance)
				instancesFound++
			}
		}
	}
	return instancesForUpdate
}
