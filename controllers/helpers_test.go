package controllers

import (
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	upgrademgrv1alpha1 "github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"github.com/onsi/gomega"
	"testing"
)

func TestGetMaxUnavailableWithPercentageValue(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	jsonString := "{ \"type\": \"randomUpdate\", \"maxUnavailable\": \"75%\", \"drainTimeout\": 15 }"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := json.Unmarshal([]byte(jsonString), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy object, error: %s", err.Error())
	}

	g.Expect(getMaxUnavailable(strategy, 200)).To(gomega.Equal(150))
}

func TestGetMaxUnavailableWithPercentageValue33(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	jsonString := "{ \"type\": \"randomUpdate\", \"maxUnavailable\": \"67%\", \"drainTimeout\": 15 }"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := json.Unmarshal([]byte(jsonString), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy object, error: %s", err.Error())
	}

	g.Expect(getMaxUnavailable(strategy, 3)).To(gomega.Equal(2))
}

func TestGetMaxUnavailableWithPercentageAndSingleInstance(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	totalNodes := 1
	jsonString := "{ \"type\": \"randomUpdate\", \"maxUnavailable\": \"67%\", \"drainTimeout\": 15 }"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := json.Unmarshal([]byte(jsonString), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy object, error: %s", err.Error())
	}

	g.Expect(getMaxUnavailable(strategy, totalNodes)).To(gomega.Equal(1))
}

func TestGetMaxUnavailableWithPercentageNonIntResult(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	jsonString := "{ \"type\": \"randomUpdate\", \"maxUnavailable\": \"37%\", \"drainTimeout\": 15 }"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := json.Unmarshal([]byte(jsonString), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy object, error: %s", err.Error())
	}

	g.Expect(getMaxUnavailable(strategy, 50)).To(gomega.Equal(18))
}

func TestGetMaxUnavailableWithIntValue(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	jsonString := "{ \"type\": \"randomUpdate\", \"maxUnavailable\": 75, \"drainTimeout\": 15 }"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := json.Unmarshal([]byte(jsonString), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy object, error: %s", err.Error())
	}

	g.Expect(getMaxUnavailable(strategy, 200)).To(gomega.Equal(75))
}

func TestGetNextAvailableInstance(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	mockInstanceName1 := "foo1"
	mockInstanceName2 := "bar1"
	az := "az-1"
	instance1 := autoscaling.Instance{InstanceId: &mockInstanceName1, AvailabilityZone: &az}
	instance2 := autoscaling.Instance{InstanceId: &mockInstanceName2, AvailabilityZone: &az}

	instancesList := []*autoscaling.Instance{}
	instancesList = append(instancesList, &instance1, &instance2)
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: clusterState}
	rcRollingUpgrade.ClusterState.initializeAsg(mockAsgName, instancesList)
	available := getNextAvailableInstances(mockAsgName, 1, instancesList, rcRollingUpgrade.ClusterState)

	g.Expect(1).Should(gomega.Equal(len(available)))
	g.Expect(rcRollingUpgrade.ClusterState.deleteEntryOfAsg(mockAsgName)).To(gomega.BeTrue())

}

func TestGetNextAvailableInstanceNoInstanceFound(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	mockInstanceName1 := "foo1"
	mockInstanceName2 := "bar1"
	az := "az-1"
	instance1 := autoscaling.Instance{InstanceId: &mockInstanceName1, AvailabilityZone: &az}
	instance2 := autoscaling.Instance{InstanceId: &mockInstanceName2, AvailabilityZone: &az}

	instancesList := []*autoscaling.Instance{}
	instancesList = append(instancesList, &instance1, &instance2)
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: clusterState}
	rcRollingUpgrade.ClusterState.initializeAsg(mockAsgName, instancesList)
	available := getNextAvailableInstances("asg2", 1, instancesList, rcRollingUpgrade.ClusterState)

	g.Expect(0).Should(gomega.Equal(len(available)))
	g.Expect(rcRollingUpgrade.ClusterState.deleteEntryOfAsg(mockAsgName)).To(gomega.BeTrue())

}

func TestGetNextAvailableInstanceInAz(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	mockInstanceName1 := "foo1"
	mockInstanceName2 := "bar1"
	az := "az-1"
	az2 := "az-2"
	instance1 := autoscaling.Instance{InstanceId: &mockInstanceName1, AvailabilityZone: &az}
	instance2 := autoscaling.Instance{InstanceId: &mockInstanceName2, AvailabilityZone: &az2}

	instancesList := []*autoscaling.Instance{}
	instancesList = append(instancesList, &instance1, &instance2)
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: clusterState}
	rcRollingUpgrade.ClusterState.initializeAsg(mockAsgName, instancesList)

	instances := getNextSetOfAvailableInstancesInAz(mockAsgName, az, 1, instancesList, rcRollingUpgrade.ClusterState)
	g.Expect(1).Should(gomega.Equal(len(instances)))
	g.Expect(mockInstanceName1).Should(gomega.Equal(*instances[0].InstanceId))

	instances = getNextSetOfAvailableInstancesInAz(mockAsgName, az2, 1, instancesList, rcRollingUpgrade.ClusterState)
	g.Expect(1).Should(gomega.Equal(len(instances)))
	g.Expect(mockInstanceName2).Should(gomega.Equal(*instances[0].InstanceId))

	instances = getNextSetOfAvailableInstancesInAz(mockAsgName, "az3", 1, instancesList, rcRollingUpgrade.ClusterState)
	g.Expect(0).Should(gomega.Equal(len(instances)))

	g.Expect(rcRollingUpgrade.ClusterState.deleteEntryOfAsg(mockAsgName)).To(gomega.BeTrue())

}

func TestGetNextAvailableInstanceInAzGetMultipleInstances(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	mockInstanceName1 := "foo1"
	mockInstanceName2 := "bar1"
	az := "az-1"
	instance1 := autoscaling.Instance{InstanceId: &mockInstanceName1, AvailabilityZone: &az}
	instance2 := autoscaling.Instance{InstanceId: &mockInstanceName2, AvailabilityZone: &az}

	instancesList := []*autoscaling.Instance{}
	instancesList = append(instancesList, &instance1, &instance2)
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: clusterState}
	rcRollingUpgrade.ClusterState.initializeAsg(mockAsgName, instancesList)

	instances := getNextSetOfAvailableInstancesInAz(mockAsgName, az, 3, instancesList, rcRollingUpgrade.ClusterState)

	// Even though the request is for 3 instances, only 2 should be returned as there are only 2 nodes in the ASG
	g.Expect(2).Should(gomega.Equal(len(instances)))
	instanceIds := []string{*instances[0].InstanceId, *instances[1].InstanceId}
	g.Expect(instanceIds).Should(gomega.ConsistOf(mockInstanceName1, mockInstanceName2))

	g.Expect(rcRollingUpgrade.ClusterState.deleteEntryOfAsg(mockAsgName)).To(gomega.BeTrue())
}
