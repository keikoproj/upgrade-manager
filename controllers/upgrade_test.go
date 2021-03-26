package controllers

import (
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/onsi/gomega"
	drain "k8s.io/kubectl/pkg/drain"
)

func TestDrainNode(t *testing.T) {
	var err error
	g := gomega.NewGomegaWithT(t)

	r := createRollingUpgradeReconciler(t)
	rollingUpgrade := createRollingUpgrade()
	node := createNode()

	err = r.Auth.DrainNode(node, time.Duration(rollingUpgrade.PostDrainDelaySeconds()), rollingUpgrade.DrainTimeout(), r.Auth.Kubernetes)
	g.Expect(err).To(gomega.BeNil())
}

func TestRunCordonOrUncordon(t *testing.T) {
	var err error
	g := gomega.NewGomegaWithT(t)

	r := createRollingUpgradeReconciler(t)
	rollingUpgrade := createRollingUpgrade()
	node := createNode()

	helper := &drain.Helper{
		Client:              r.Auth.Kubernetes,
		Force:               true,
		GracePeriodSeconds:  -1,
		IgnoreAllDaemonSets: true,
		Out:                 os.Stdout,
		ErrOut:              os.Stdout,
		DeleteEmptyDirData:  true,
		Timeout:             time.Duration(rollingUpgrade.Spec.Strategy.DrainTimeout) * time.Second,
	}

	err = drain.RunCordonOrUncordon(helper, node, true)
	g.Expect(err).To(gomega.BeNil())
	g.Expect(node.Spec.Unschedulable).To(gomega.BeTrue())

}

func TestRunNodeDrain(t *testing.T) {
	var err error
	g := gomega.NewGomegaWithT(t)

	r := createRollingUpgradeReconciler(t)
	rollingUpgrade := createRollingUpgrade()
	node := createNode()

	helper := &drain.Helper{
		Client:              r.Auth.Kubernetes,
		Force:               true,
		GracePeriodSeconds:  -1,
		IgnoreAllDaemonSets: true,
		Out:                 os.Stdout,
		ErrOut:              os.Stdout,
		DeleteEmptyDirData:  true,
		Timeout:             time.Duration(rollingUpgrade.Spec.Strategy.DrainTimeout) * time.Second,
	}

	err = drain.RunNodeDrain(helper, node.Name)
	g.Expect(err).To(gomega.BeNil())
}

func TestListClusterNodes(t *testing.T) {
	var err error
	g := gomega.NewGomegaWithT(t)

	r := createRollingUpgradeReconciler(t)

	actual, err := r.Auth.ListClusterNodes()
	expected := createNodeList()
	g.Expect(err).To(gomega.BeNil())
	g.Expect(reflect.DeepEqual(actual, expected)).To(gomega.BeTrue())
}

func TestRotateNodes(t *testing.T) {
	// var err error
	// g := gomega.NewGomegaWithT(t)
}
