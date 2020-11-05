package controllers

import (
	"github.com/keikoproj/aws-sdk-go-cache/cache"
	upgrademgrv1alpha1 "github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	log2 "sigs.k8s.io/controller-runtime/pkg/log"
	"sync"
	"testing"
	"time"
)

func Test_createK8sV1Event(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		ASGClient:       MockAutoscalingGroup{},
		EC2Client:       MockEC2{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		admissionMap:    sync.Map{},
		ruObjNameToASG:  sync.Map{},
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
	}

	event := rcRollingUpgrade.createK8sV1Event(ruObj, EventReasonRUStarted, EventLevelNormal, map[string]string{})
	g.Expect(EventReason(event.Reason)).To(gomega.Equal(EventReasonRUStarted))

	g.Expect(err).To(gomega.BeNil())
}
