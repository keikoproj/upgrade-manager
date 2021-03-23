package controllers

import (
	"fmt"
	"path/filepath"
	"testing"

	v1alpha1 "github.com/keikoproj/upgrade-manager/api/v1alpha1"
	kubeprovider "github.com/keikoproj/upgrade-manager/controllers/providers/kubernetes"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func createRollingUpgradeReconciler(t *testing.T) *RollingUpgradeReconciler {
	// var err error
	// g := gomega.NewGomegaWithT(t)

	// k8s client
	// kubeClient (fake client)
	mockNode := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "mock-node"},
		Spec:       corev1.NodeSpec{ProviderID: "foo-bar/9213851"},
	}
	fmt.Println(&mockNode)
	kubeClient := &kubeprovider.KubernetesClientSet{
		Kubernetes: fake.NewSimpleClientset(&corev1.NodeList{Items: []corev1.Node{mockNode}}),
	}
	// k8sManager, kubeClient := getKubernetesClient(t)

	// logger
	logger := ctrl.Log.WithName("controllers").WithName("RollingUpgrade")

	// authenticator
	auth := &RollingUpgradeAuthenticator{
		KubernetesClientSet: kubeClient,
	}

	// reconciler object
	reconciler := &RollingUpgradeReconciler{
		Logger:      logger,
		Auth:        auth,
		EventWriter: kubeprovider.NewEventWriter(kubeClient, logger),
		ScriptRunner: ScriptRunner{
			Logger: logger,
		},
		Cloud: NewDiscoveredState(auth, logger),
	}

	// register the manager with controller
	// err = reconciler.SetupWithManager(k8sManager)
	// g.Expect(err).ToNot(gomega.HaveOccurred())

	// start the manager
	// go func() {
	// 	err = k8sManager.Start(ctrl.SetupSignalHandler())
	// 	g.Expect(err).ToNot(gomega.HaveOccurred())
	// }()

	return reconciler

}

func getKubernetesClient(t *testing.T) (manager.Manager, *kubeprovider.KubernetesClientSet) {
	var err error
	g := gomega.NewGomegaWithT(t)

	/* kubeClient (from kubebuilder book) */
	var cfg *rest.Config
	var k8sClient client.Client
	var testEnv *envtest.Environment

	// set up test environment
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "config", "crd", "bases")},
	}

	cfg, err = testEnv.Start()
	g.Expect(err).ToNot(gomega.HaveOccurred())
	g.Expect(cfg).ToNot(gomega.BeNil())

	// setup manager
	err = v1alpha1.AddToScheme(scheme.Scheme)
	g.Expect(err).ToNot(gomega.HaveOccurred())

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// k8s client
	k8sClient = k8sManager.GetClient()
	kubeClient := &kubeprovider.KubernetesClientSet{
		Kubernetes: kubernetes.NewForConfigOrDie(k8sManager.GetConfig()),
	}

	// check for k8s client. It shouldn't be nil
	k8sClient = k8sManager.GetClient()
	g.Expect(k8sClient).ToNot(gomega.BeNil())

	// check for k8s interface. It shouldn't be nil
	Kubernetes := kubernetes.NewForConfigOrDie(k8sManager.GetConfig())
	g.Expect(Kubernetes).ToNot(gomega.BeNil())

	return k8sManager, kubeClient
}

func createRollingUpgrade() *v1alpha1.RollingUpgrade {
	return &v1alpha1.RollingUpgrade{}
}
