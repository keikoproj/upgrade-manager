package common

import (
	"github.com/onsi/gomega"
	"testing"
)

func TestAddRollingUpgradeStepDuration(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	g.Expect(stepSummaries["test-asg"]).To(gomega.BeNil())
	AddRollingUpgradeStepDuration("test-asg", "kickoff", 1)

	g.Expect(stepSummaries["test-asg"]).NotTo(gomega.BeNil())
	g.Expect(stepSummaries["test-asg"]["kickoff"]).NotTo(gomega.BeNil())

	//Test duplicate
	AddRollingUpgradeStepDuration("test-asg", "kickoff", 1)
	g.Expect(stepSummaries["test-asg"]["kickoff"]).NotTo(gomega.BeNil())

	//Test duplicate
	delete(stepSummaries["test-asg"], "kickoff")
	AddRollingUpgradeStepDuration("test-asg", "kickoff", 1)
	g.Expect(stepSummaries["test-asg"]["kickoff"]).NotTo(gomega.BeNil())

	//Test total
	AddRollingUpgradeStepDuration("test-asg", "total", 1)
	g.Expect(stepSummaries["test-asg"]["kickoff"]).NotTo(gomega.BeNil())
}
