package controllers

import (
	"testing"

	v1alpha1 "github.com/keikoproj/upgrade-manager/api/v1alpha1"
	awsprovider "github.com/keikoproj/upgrade-manager/controllers/providers/aws"
	kubeprovider "github.com/keikoproj/upgrade-manager/controllers/providers/kubernetes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	ctrl "sigs.k8s.io/controller-runtime"
)

func createRollingUpgradeReconciler(t *testing.T) *RollingUpgradeReconciler {
	// amazon client
	amazonClient := createAmazonClient(t)

	// k8s client (fake client)
	kubeClient := &kubeprovider.KubernetesClientSet{
		Kubernetes: fake.NewSimpleClientset(createNodeList()),
	}

	// logger
	logger := ctrl.Log.WithName("controllers").WithName("RollingUpgrade")

	// authenticator
	auth := &RollingUpgradeAuthenticator{
		KubernetesClientSet: kubeClient,
		AmazonClientSet:     amazonClient,
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
	return reconciler

}

func createAmazonClient(t *testing.T) *awsprovider.AmazonClientSet {
	return &awsprovider.AmazonClientSet{}
}

func createRollingUpgrade() *v1alpha1.RollingUpgrade {
	return &v1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "mock-rollup", Namespace: "default"},
		Spec: v1alpha1.RollingUpgradeSpec{
			PostDrainDelaySeconds: 30,
			Strategy: v1alpha1.UpdateStrategy{
				Type:         v1alpha1.RandomUpdateStrategy,
				DrainTimeout: 30,
			},
		},
	}
}

func createNodeList() *corev1.NodeList {
	return &corev1.NodeList{
		Items: []corev1.Node{
			corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "mock-node-1"},
				Spec:       corev1.NodeSpec{ProviderID: "foo-bar/9213851"},
			},
			corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "mock-node-2"},
				Spec:       corev1.NodeSpec{ProviderID: "foo-bar/9213852"},
			},
			corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "mock-node-3"},
				Spec:       corev1.NodeSpec{ProviderID: "foo-bar/9213853"},
			},
		},
	}
}

func createNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "mock-node-1", Namespace: "default"},
		Spec:       corev1.NodeSpec{ProviderID: "foo-bar/9213851"},
	}
}
