package controllers

import (
	upgrademgrv1alpha1 "github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimelog "sigs.k8s.io/controller-runtime/pkg/log"
	"testing"
)

func TestEchoScript(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	ru := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	r := &ScriptRunner{Log: runtimelog.NullLogger{}}
	out, err := r.runScript("echo hello", false, ru)

	g.Expect(err).To(gomega.BeNil())
	g.Expect(out).To(gomega.Equal("hello\n"))
}

func TestEchoBackgroundScript(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	ru := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	r := &ScriptRunner{Log: runtimelog.NullLogger{}}
	out, err := r.runScript("echo background", true, ru)

	g.Expect(err).To(gomega.BeNil())
	g.Expect(out).To(gomega.Equal(""))
}

func TestRunScriptFailure(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	ru := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	r := &ScriptRunner{Log: runtimelog.NullLogger{}}
	out, err := r.runScript("echo this will fail; exit 1", false, ru)

	g.Expect(err).To(gomega.Not(gomega.BeNil()))
	g.Expect(out).To(gomega.Not(gomega.Equal("")))
}
