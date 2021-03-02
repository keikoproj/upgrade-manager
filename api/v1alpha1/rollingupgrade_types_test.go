package v1alpha1

import (
	"github.com/onsi/gomega"
	"testing"
)

// Test
func TestNodeTurnsOntoStep(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	r := &RollingUpgradeStatus{}

	r.NodeStep("test-asg", "node-1", NodeRotationKickoff)

	g.Expect(r.InProcessingNodes).NotTo(gomega.BeNil())
	g.Expect(r.Statistics).To(gomega.BeNil())

	r.NodeStep("test-asg", "node-1", NodeRotationDesiredNodeReady)

	g.Expect(r.Statistics).NotTo(gomega.BeNil())
	g.Expect(len(r.Statistics)).To(gomega.Equal(1))
	g.Expect(r.Statistics[0].StepName).To(gomega.Equal(NodeRotationKickoff))

	//Retry desired_node_ready
	r.NodeStep("test-asg", "node-1", NodeRotationDesiredNodeReady)
	g.Expect(len(r.Statistics)).To(gomega.Equal(1))
	g.Expect(r.Statistics[0].StepName).To(gomega.Equal(NodeRotationKickoff))

	//Retry desired_node_ready again
	r.NodeStep("test-asg", "node-1", NodeRotationDesiredNodeReady)
	g.Expect(len(r.Statistics)).To(gomega.Equal(1))
	g.Expect(r.Statistics[0].StepName).To(gomega.Equal(NodeRotationKickoff))

	//Completed
	r.NodeStep("test-asg", "node-1", NodeRotationCompleted)
	g.Expect(len(r.Statistics)).To(gomega.Equal(3))
	g.Expect(r.Statistics[1].StepName).To(gomega.Equal(NodeRotationDesiredNodeReady))
	g.Expect(r.Statistics[2].StepName).To(gomega.Equal(NodeRotationTotal))

	//Second node
	r.NodeStep("test-asg", "node-2", NodeRotationKickoff)
	g.Expect(len(r.Statistics)).To(gomega.Equal(3))

	r.NodeStep("test-asg", "node-2", NodeRotationDesiredNodeReady)
	g.Expect(len(r.Statistics)).To(gomega.Equal(3))
}
