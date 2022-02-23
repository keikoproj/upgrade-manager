package controllers

import (
	"testing"

	"github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimelog "sigs.k8s.io/controller-runtime/pkg/log"
)

func TestScriptSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	r := &ScriptRunner{Logger: runtimelog.NullLogger{}}
	target := ScriptTarget{
		InstanceID: "instance",
		NodeName:   "node",
		UpgradeObject: &v1alpha1.RollingUpgrade{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "foo",
				Namespace: "default",
			},
		},
	}

	out, err := r.runScript("echo hello", target)
	g.Expect(err).To(gomega.BeNil())
	g.Expect(out).To(gomega.Equal("hello\n"))
}

func TestScriptFailure(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	r := &ScriptRunner{Logger: runtimelog.NullLogger{}}
	target := ScriptTarget{
		InstanceID: "instance",
		NodeName:   "node",
		UpgradeObject: &v1alpha1.RollingUpgrade{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "foo",
				Namespace: "default",
			},
		},
	}

	out, err := r.runScript("echo this will fail; exit 1", target)
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
	g.Expect(out).To(gomega.Not(gomega.Equal("")))
}
