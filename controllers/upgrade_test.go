package controllers

import (
	"fmt"
	"os"
	"testing"

	"github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes/fake"
	drain "k8s.io/kubectl/pkg/drain"
	ctrl "sigs.k8s.io/controller-runtime"

	"reflect"
	"time"

	"github.com/keikoproj/upgrade-manager/api/v1alpha1"
	awsprovider "github.com/keikoproj/upgrade-manager/controllers/providers/aws"
	kubeprovider "github.com/keikoproj/upgrade-manager/controllers/providers/kubernetes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestListClusterNodes(t *testing.T) {
	var err error
	g := gomega.NewGomegaWithT(t)

	r := createRollingUpgradeReconciler(t)

	actual, err := r.Auth.ListClusterNodes()
	expected := createNodeList()
	g.Expect(err).To(gomega.BeNil())
	g.Expect(reflect.DeepEqual(actual, expected)).To(gomega.BeTrue())

	var tests = []struct {
		TestDescription string
		Reconciler      *RollingUpgradeReconciler
		RollingUpgrade  *v1alpha1.RollingUpgrade
		Node            *corev1.Node
		ExpectError     bool
	}{
		{
			"List cluster should succeed",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			createNode(),
			false,
		},
	}

	for _, test := range tests {
		actual, err := test.Reconciler.Auth.ListClusterNodes()
		expected := createNodeList()
		if err != nil || !reflect.DeepEqual(actual, expected) {
			t.Errorf("ListClusterNodes fail %v", err)
		}
	}
}

// This test checks implementation of our DrainNode which does both cordon + drain
func TestDrainNode(t *testing.T) {
	var tests = []struct {
		TestDescription string
		Reconciler      *RollingUpgradeReconciler
		RollingUpgrade  *v1alpha1.RollingUpgrade
		Node            *corev1.Node
		ExpectError     bool
	}{
		{
			"Drain should succeed as node is registered with fakeClient",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			createNode(),
			false,
		},
		{
			"Drain should fail as node is not registered with fakeClient",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			&corev1.Node{},
			true,
		},
	}

	for _, test := range tests {
		err := test.Reconciler.Auth.DrainNode(
			test.Node,
			time.Duration(test.RollingUpgrade.PostDrainDelaySeconds()),
			test.RollingUpgrade.DrainTimeout(),
			test.Reconciler.Auth.Kubernetes,
		)
		if (test.ExpectError && err == nil) || (!test.ExpectError && err != nil) {
			t.Errorf("Test Description: %s \n expected error(bool): %v, Actual err: %v", test.TestDescription, test.ExpectError, err)
		}
	}

}

// This test checks implementation of the package provided Cordon/Uncordon function
func TestRunCordonOrUncordon(t *testing.T) {
	var tests = []struct {
		TestDescription string
		Reconciler      *RollingUpgradeReconciler
		RollingUpgrade  *v1alpha1.RollingUpgrade
		Node            *corev1.Node
		Cordon          bool
		ExpectError     bool
	}{
		{
			"Cordon should succeed as node is registered with fakeClient",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			createNode(),
			true,
			false,
		},
		{
			"Cordon should fail as node is not registered with fakeClient",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			&corev1.Node{},
			true,
			true,
		},
		{
			"Uncordon should succeed as node is registered with fakeClient",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			func() *corev1.Node {
				node := createNode()
				node.Spec.Unschedulable = true
				return node
			}(),
			false,
			false,
		},
		{
			"Uncordon should fail as node is not registered with fakeClient",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			func() *corev1.Node {
				node := &corev1.Node{}
				node.Spec.Unschedulable = true
				return node
			}(),
			false,
			true,
		},
	}

	for _, test := range tests {
		helper := &drain.Helper{
			Client:              test.Reconciler.Auth.Kubernetes,
			Force:               true,
			GracePeriodSeconds:  -1,
			IgnoreAllDaemonSets: true,
			Out:                 os.Stdout,
			ErrOut:              os.Stdout,
			DeleteEmptyDirData:  true,
			Timeout:             time.Duration(test.RollingUpgrade.Spec.Strategy.DrainTimeout) * time.Second,
		}
		err := drain.RunCordonOrUncordon(helper, test.Node, test.Cordon)
		if (test.ExpectError && err == nil) || (!test.ExpectError && err != nil) {
			fmt.Println(test.Node)
			t.Errorf("Test Description: %s \n expected error(bool): %v, Actual err: %v", test.TestDescription, test.ExpectError, err)
		}
		//check if the node is actually cordoned/uncordoned.
		if test.Cordon && test.Node != nil && !test.Node.Spec.Unschedulable {
			fmt.Println(test.Node)
			t.Errorf("Test Description: %s \n expected the node to be cordoned but it is uncordoned", test.TestDescription)
		}
		if !test.Cordon && test.Node != nil && test.Node.Spec.Unschedulable {
			fmt.Println(test.Node)
			t.Errorf("Test Description: %s \n expected the node to be uncordoned but it is cordoned", test.TestDescription)
		}

	}

}

// This test checks implementation of the package provided Drain function
func TestRunDrainNode(t *testing.T) {
	var tests = []struct {
		TestDescription string
		Reconciler      *RollingUpgradeReconciler
		RollingUpgrade  *v1alpha1.RollingUpgrade
		Node            *corev1.Node
		ExpectError     bool
	}{
		{
			"Drain should succeed as node is registered with fakeClient",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			createNode(),
			false,
		},
		// This test should fail, create an upstream ticket.
		// https://github.com/kubernetes/kubectl/blob/d5b32e7f3c0260abb5b1cd5a62d4eb1de287bc93/pkg/drain/default.go#L33
		{
			"Drain should fail as node is not registered with fakeClient",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			&corev1.Node{},
			true,
		},
	}

	for _, test := range tests {
		helper := &drain.Helper{
			Client:              test.Reconciler.Auth.Kubernetes,
			Force:               true,
			GracePeriodSeconds:  -1,
			IgnoreAllDaemonSets: true,
			Out:                 os.Stdout,
			ErrOut:              os.Stdout,
			DeleteEmptyDirData:  true,
			Timeout:             time.Duration(test.RollingUpgrade.Spec.Strategy.DrainTimeout) * time.Second,
		}
		err := drain.RunNodeDrain(helper, test.Node.Name)
		if (test.ExpectError && err == nil) || (!test.ExpectError && err != nil) {
			fmt.Println(test.Node)
			t.Errorf("Test Description: %s \n expected error(bool): %v, Actual err: %v", test.TestDescription, test.ExpectError, err)
		}
	}

}

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
