package v1alpha1

import (
	"github.com/onsi/gomega"
	"testing"
)

// Test
func TestNodeTurnsOntoStep(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	r := &RollingUpgradeStatus{}
	//A map to retain the steps for multiple nodes
	nodeSteps := make(map[string] []NodeStepDuration)
	inProcessingNodes := make(map[string]* NodeInProcessing)

	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-1", NodeRotationKickoff)

	g.Expect(inProcessingNodes).NotTo(gomega.BeNil())
	g.Expect(r.Statistics).To(gomega.BeNil())

	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-1", NodeRotationDesiredNodeReady)

	g.Expect(r.Statistics).NotTo(gomega.BeNil())
	g.Expect(len(r.Statistics)).To(gomega.Equal(1))
	g.Expect(r.Statistics[0].StepName).To(gomega.Equal(NodeRotationKickoff))

	//Retry desired_node_ready
	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-1", NodeRotationDesiredNodeReady)
	g.Expect(len(r.Statistics)).To(gomega.Equal(1))
	g.Expect(r.Statistics[0].StepName).To(gomega.Equal(NodeRotationKickoff))

	//Retry desired_node_ready again
	r.NodeStep(inProcessingNodes, nodeSteps,"test-asg", "node-1", NodeRotationDesiredNodeReady)
	g.Expect(len(r.Statistics)).To(gomega.Equal(1))
	g.Expect(r.Statistics[0].StepName).To(gomega.Equal(NodeRotationKickoff))

	//Completed
	r.NodeStep(inProcessingNodes, nodeSteps,"test-asg", "node-1", NodeRotationCompleted)
	g.Expect(len(r.Statistics)).To(gomega.Equal(3))
	g.Expect(r.Statistics[1].StepName).To(gomega.Equal(NodeRotationDesiredNodeReady))
	g.Expect(r.Statistics[2].StepName).To(gomega.Equal(NodeRotationTotal))

	//Second node
	r.NodeStep(inProcessingNodes, nodeSteps,"test-asg", "node-2", NodeRotationKickoff)
	g.Expect(len(r.Statistics)).To(gomega.Equal(3))

	r.NodeStep(inProcessingNodes, nodeSteps,"test-asg", "node-2", NodeRotationDesiredNodeReady)
	g.Expect(len(r.Statistics)).To(gomega.Equal(3))
}
