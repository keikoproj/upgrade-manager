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

	mockAsgName := "some-asg"
	mockNodeName := "some-node-name"
	clusterState.markUpdateInProgress(mockAsgName, mockNodeName)

	g.Expect(clusterState.instanceUpdateInProgress(mockAsgName, mockNodeName)).To(gomega.BeTrue())
}

func TestMarkUpdateCompleted(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	mockNodeName := "some-node-name"
	clusterState.markUpdateCompleted(mockAsgName, mockNodeName)

	g.Expect(clusterState.instanceUpdateCompleted(mockAsgName, mockNodeName)).To(gomega.BeTrue())
}

func TestMarkUpdateInitialized(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	mockNodeName := "some-node-name"
	clusterState.markUpdateInitialized(mockAsgName, mockNodeName)

	g.Expect(clusterState.instanceUpdateInitialized(mockAsgName, mockNodeName)).To(gomega.BeTrue())
}

func TestInstanceUpdateInitialized(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg-1"
	mockNodeName := "some-node-name-1"

	g.Expect(clusterState.instanceUpdateInitialized(mockAsgName, mockNodeName)).To(gomega.BeFalse())
}

func TestIinstanceUpdateInProgress(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg-2"
	mockNodeName := "some-node-name-2"

	g.Expect(clusterState.instanceUpdateInProgress(mockAsgName, mockNodeName)).To(gomega.BeFalse())
}

func TestIinstanceUpdateCompleted(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg-3"
	mockNodeName := "some-node-name-3"

	g.Expect(clusterState.instanceUpdateCompleted(mockAsgName, mockNodeName)).To(gomega.BeFalse())
}

func TestUpdateInstanceState(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	mockNodeName := "some-node-name"
	mockInstanceState := "to-be-updated"
	clusterState.updateInstanceState(mockAsgName, mockNodeName, mockInstanceState)

	g.Expect(clusterState.getInstanceState(mockAsgName, mockNodeName)).To(gomega.ContainSubstring(mockInstanceState))
}

func TestInitializeAsg(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	mockInstance1 := "mock-instance-1"
	mockInstance2 := "mock-instance-2"
	instances := []*autoscaling.Instance{}
	instances = append(instances, &autoscaling.Instance{InstanceId: &mockInstance1})
	instances = append(instances, &autoscaling.Instance{InstanceId: &mockInstance2})
	clusterState.initializeAsg(mockAsgName, instances)

	for _, instance := range instances {
		g.Expect(clusterState.instanceUpdateInitialized(mockAsgName, *instance.InstanceId)).To(gomega.BeTrue())
	}
}

func TestDeleteEntryOfAsg(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg-1"
	mockNodeName := "some-node-1"
	clusterState.markUpdateInProgress(mockAsgName, mockNodeName)

	g.Expect(clusterState.deleteEntryOfAsg(mockAsgName)).To(gomega.BeTrue())
	g.Expect(clusterState.deleteEntryOfAsg(mockAsgName)).To(gomega.BeFalse())

}

func TestInstanceStateUpdateSequence(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg-2"
	mockNodeName := "some-node-2"
	clusterState.markUpdateInitialized(mockAsgName, mockNodeName)
	g.Expect(clusterState.instanceUpdateInitialized(mockAsgName, mockNodeName)).To(gomega.BeTrue())
	clusterState.markUpdateInProgress(mockAsgName, mockNodeName)
	g.Expect(clusterState.instanceUpdateInProgress(mockAsgName, mockNodeName)).To(gomega.BeTrue())
	clusterState.markUpdateCompleted(mockAsgName, mockNodeName)
	g.Expect(clusterState.instanceUpdateCompleted(mockAsgName, mockNodeName)).To(gomega.BeTrue())

	g.Expect(clusterState.deleteEntryOfAsg(mockAsgName)).To(gomega.BeTrue())
	g.Expect(clusterState.deleteEntryOfAsg(mockAsgName)).To(gomega.BeFalse())
}

func TestGetNextAvailableInstanceId(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg-2"
	mockInstance1 := "mock-instance-1"
	mockInstance2 := "mock-instance-2"
	instances := []*autoscaling.Instance{}
	instances = append(instances, &autoscaling.Instance{InstanceId: &mockInstance1})
	instances = append(instances, &autoscaling.Instance{InstanceId: &mockInstance2})
	clusterState.deleteEntryOfAsg(mockAsgName)

	clusterState.initializeAsg(mockAsgName, instances)
	clusterState.markUpdateInProgress(mockAsgName, mockInstance1)

	g.Expect(clusterState.getNextAvailableInstanceId(mockAsgName)).To(gomega.ContainSubstring(mockInstance2))
}
