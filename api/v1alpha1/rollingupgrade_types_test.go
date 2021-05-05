package v1alpha1

import (
	"github.com/onsi/gomega"
	"sync"
	"testing"
)

// Test
func TestNodeTurnsOntoStep(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	r := &RollingUpgradeStatus{}
	//A map to retain the steps for multiple nodes
	nodeSteps := make(map[string][]NodeStepDuration)
	inProcessingNodes := make(map[string]*NodeInProcessing)
	mutex := &sync.Mutex{}

	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-1", NodeRotationKickoff, mutex)

	g.Expect(inProcessingNodes).NotTo(gomega.BeNil())
	g.Expect(nodeSteps["node-1"]).To(gomega.BeNil())

	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-1", NodeRotationDesiredNodeReady, mutex)

	g.Expect(len(nodeSteps["node-1"])).To(gomega.Equal(1))
	g.Expect(nodeSteps["node-1"][0].StepName).To(gomega.Equal(NodeRotationKickoff))

	//Retry desired_node_ready
	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-1", NodeRotationDesiredNodeReady, mutex)
	g.Expect(len(nodeSteps["node-1"])).To(gomega.Equal(1))
	g.Expect(nodeSteps["node-1"][0].StepName).To(gomega.Equal(NodeRotationKickoff))

	//Retry desired_node_ready again
	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-1", NodeRotationDesiredNodeReady, mutex)
	g.Expect(len(nodeSteps["node-1"])).To(gomega.Equal(1))
	g.Expect(nodeSteps["node-1"][0].StepName).To(gomega.Equal(NodeRotationKickoff))

	//Completed
	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-1", NodeRotationCompleted, mutex)
	g.Expect(len(nodeSteps["node-1"])).To(gomega.Equal(3))
	g.Expect(nodeSteps["node-1"][1].StepName).To(gomega.Equal(NodeRotationDesiredNodeReady))
	g.Expect(nodeSteps["node-1"][2].StepName).To(gomega.Equal(NodeRotationTotal))

	//Second node
	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-2", NodeRotationKickoff, mutex)
	g.Expect(len(nodeSteps["node-1"])).To(gomega.Equal(3))

	r.NodeStep(inProcessingNodes, nodeSteps, "test-asg", "node-2", NodeRotationDesiredNodeReady, mutex)
	g.Expect(len(nodeSteps["node-1"])).To(gomega.Equal(3))
}
