/*
Copyright 2019 Intuit, Inc..

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

package controllers

import (
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/onsi/gomega"
	"testing"
)

var clusterState = NewClusterState()

func TestMarkUpdateInProgress(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	populateClusterState()
	mockNodeName := "instance-1"
	clusterState.markUpdateInProgress(mockNodeName)

	g.Expect(clusterState.instanceUpdateInProgress(mockNodeName)).To(gomega.BeTrue())
}

func TestMarkUpdateCompleted(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	populateClusterState()
	mockNodeName := "instance-1"
	clusterState.markUpdateCompleted(mockNodeName)

	g.Expect(clusterState.instanceUpdateCompleted(mockNodeName)).To(gomega.BeTrue())
}

func TestMarkUpdateInitialized(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	populateClusterState()
	mockNodeName := "instance-1"
	clusterState.markUpdateInitialized(mockNodeName)

	g.Expect(clusterState.instanceUpdateInitialized(mockNodeName)).To(gomega.BeTrue())
}

func TestInstanceUpdateInitialized(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockNodeName := "instance-3"
	g.Expect(clusterState.instanceUpdateInitialized(mockNodeName)).To(gomega.BeFalse())
}

func TestInstanceUpdateInProgress(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	populateClusterState()
	mockNodeName := "instance-2"

	g.Expect(clusterState.instanceUpdateInProgress(mockNodeName)).To(gomega.BeFalse())
}

func TestInstanceUpdateCompleted(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	populateClusterState()
	mockNodeName := "instance-1"

	g.Expect(clusterState.instanceUpdateCompleted(mockNodeName)).To(gomega.BeFalse())
}

func TestUpdateInstanceState(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	populateClusterState()
	mockNodeName := "instance-1"
	mockInstanceState := "to-be-updated"
	clusterState.updateInstanceState(mockNodeName, mockInstanceState)

	g.Expect(clusterState.getInstanceState(mockNodeName)).To(gomega.ContainSubstring(mockInstanceState))
}

func TestInitializeAsg(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	populateClusterState()

	instanceIds := []string{"instance-1", "instance-2"}
	for _, instance := range instanceIds {
		g.Expect(clusterState.instanceUpdateInitialized(instance)).To(gomega.BeTrue())
	}
}

func TestDeleteEntryOfAsg(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	populateClusterState()
	mockAsgName := "asg-1"

	g.Expect(clusterState.deleteEntryOfAsg(mockAsgName)).To(gomega.BeTrue())
	g.Expect(clusterState.deleteEntryOfAsg(mockAsgName)).To(gomega.BeFalse())
}

func TestInstanceStateUpdateSequence(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	populateClusterState()
	mockAsgName := "asg-1"
	mockNodeName := "instance-1"
	clusterState.markUpdateInitialized(mockNodeName)
	g.Expect(clusterState.instanceUpdateInitialized(mockNodeName)).To(gomega.BeTrue())
	clusterState.markUpdateInProgress(mockNodeName)
	g.Expect(clusterState.instanceUpdateInProgress(mockNodeName)).To(gomega.BeTrue())
	clusterState.markUpdateCompleted(mockNodeName)
	g.Expect(clusterState.instanceUpdateCompleted(mockNodeName)).To(gomega.BeTrue())

	g.Expect(clusterState.deleteEntryOfAsg(mockAsgName)).To(gomega.BeTrue())
	g.Expect(clusterState.deleteEntryOfAsg(mockAsgName)).To(gomega.BeFalse())
}

func TestGetNextAvailableInstanceIdInAz(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	populateClusterState()
	mockAsgName := "asg-1"
	mockInstance1 := "instance-1"
	mockInstance2 := "instance-2"

	clusterState.markUpdateInProgress(mockInstance1)

	g.Expect(clusterState.getNextAvailableInstanceIdInAz(mockAsgName, "az-1")).To(gomega.ContainSubstring(mockInstance2))

	g.Expect(clusterState.getNextAvailableInstanceIdInAz(mockAsgName, "az-2")).To(gomega.ContainSubstring(""))
}

func populateClusterState() {
	asgName := "asg-1"
	instance1 := "instance-1"
	instance2 := "instance-2"
	az := "az-1"
	instances := []*autoscaling.Instance{}
	instances = append(instances, &autoscaling.Instance{InstanceId: &instance1, AvailabilityZone: &az})
	instances = append(instances, &autoscaling.Instance{InstanceId: &instance2, AvailabilityZone: &az})
	clusterState.initializeAsg(asgName, instances)
}
