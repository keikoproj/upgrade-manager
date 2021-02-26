package common

import (
	"github.com/keikoproj/upgrade-manager/controllers/common/log"
	"github.com/onsi/gomega"
	"testing"
)

func TestAddRollingUpgradeStepDuration(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	log.Warnf("test %v", stepSummaries)

	g.Expect(stepSummaries["test-asg"]).To(gomega.BeNil())
	AddRollingUpgradeStepDuration("test-asg", "kickoff", 1)

	g.Expect(stepSummaries["test-asg"]).NotTo(gomega.BeNil())
	g.Expect(stepSummaries["test-asg"]["kickoff"]).NotTo(gomega.BeNil())

	//Test duplicate
	AddRollingUpgradeStepDuration("test-asg", "kickoff", 1)
	g.Expect(stepSummaries["test-asg"]["kickoff"]).NotTo(gomega.BeNil())
}
