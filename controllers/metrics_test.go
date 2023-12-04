package controllers

import (
	"testing"

	"github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"github.com/onsi/gomega"
)

// Test
func TestNodeTurnsOntoStep(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	reconciler := createRollingUpgradeReconciler(t)
	r := createRollingUpgradeContext(reconciler)

	//A map to retain the steps for multiple nodes
	nodeSteps := make(map[string][]v1alpha1.NodeStepDuration)
	inProcessingNodes := make(map[string]*v1alpha1.NodeInProcessing)

	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-1", v1alpha1.NodeRotationKickoff)

	g.Expect(inProcessingNodes).NotTo(gomega.BeNil())
	g.Expect(nodeSteps["node-1"]).To(gomega.BeNil())

	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-1", v1alpha1.NodeRotationDesiredNodeReady)

	g.Expect(len(nodeSteps["node-1"])).To(gomega.Equal(1))
	g.Expect(nodeSteps["node-1"][0].StepName).To(gomega.Equal(v1alpha1.NodeRotationKickoff))

	//Retry desired_node_ready
	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-1", v1alpha1.NodeRotationDesiredNodeReady)
	g.Expect(len(nodeSteps["node-1"])).To(gomega.Equal(1))
	g.Expect(nodeSteps["node-1"][0].StepName).To(gomega.Equal(v1alpha1.NodeRotationKickoff))

	//Retry desired_node_ready again
	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-1", v1alpha1.NodeRotationDesiredNodeReady)
	g.Expect(len(nodeSteps["node-1"])).To(gomega.Equal(1))
	g.Expect(nodeSteps["node-1"][0].StepName).To(gomega.Equal(v1alpha1.NodeRotationKickoff))

	//Completed
	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-1", v1alpha1.NodeRotationCompleted)
	g.Expect(len(nodeSteps["node-1"])).To(gomega.Equal(3))
	g.Expect(nodeSteps["node-1"][1].StepName).To(gomega.Equal(v1alpha1.NodeRotationDesiredNodeReady))
	g.Expect(nodeSteps["node-1"][2].StepName).To(gomega.Equal(v1alpha1.NodeRotationTotal))

	//Second node
	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-2", v1alpha1.NodeRotationKickoff)
	g.Expect(len(nodeSteps["node-1"])).To(gomega.Equal(3))

	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-2", v1alpha1.NodeRotationDesiredNodeReady)
	g.Expect(len(nodeSteps["node-1"])).To(gomega.Equal(3))
}
