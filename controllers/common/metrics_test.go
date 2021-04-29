package common

import (
	"testing"

	"github.com/onsi/gomega"
)

func TestAddRollingUpgradeStepDuration(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	g.Expect(stepSummaries["test-asg"]).To(gomega.BeNil())
	AddStepDuration("test-asg", "kickoff", 1)

	g.Expect(stepSummaries["test-asg"]).NotTo(gomega.BeNil())
	g.Expect(stepSummaries["test-asg"]["kickoff"]).NotTo(gomega.BeNil())

	//Test duplicate
	AddStepDuration("test-asg", "kickoff", 1)
	g.Expect(stepSummaries["test-asg"]["kickoff"]).NotTo(gomega.BeNil())

	//Test duplicate
	delete(stepSummaries["test-asg"], "kickoff")
	AddStepDuration("test-asg", "kickoff", 1)
	g.Expect(stepSummaries["test-asg"]["kickoff"]).NotTo(gomega.BeNil())

	//Test total
	AddStepDuration("test-asg", "total", 1)
	g.Expect(stepSummaries["test-asg"]["kickoff"]).NotTo(gomega.BeNil())
}

func TestCRStatusCompleted(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	SetMetricRollupInitOrRunning("cr_test_1")
	gauage, err := CRStatus.GetMetricWithLabelValues("cr_test_1")
	g.Expect(err).To(gomega.BeNil())
	g.Expect(gauage).ToNot(gomega.BeNil())

	SetMetricRollupCompleted("cr_test_2")
	gauage, err = CRStatus.GetMetricWithLabelValues("cr_test_2")
	g.Expect(err).To(gomega.BeNil())
	g.Expect(gauage).ToNot(gomega.BeNil())

	SetMetricRollupFailed("cr_test_3")
	gauage, err = CRStatus.GetMetricWithLabelValues("cr_test_3")
	g.Expect(err).To(gomega.BeNil())
	g.Expect(gauage).ToNot(gomega.BeNil())
}
