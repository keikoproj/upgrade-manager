package controllers

import (
	"os"
	"testing"

	drain "k8s.io/kubectl/pkg/drain"

	"reflect"
	"time"

	"github.com/keikoproj/upgrade-manager/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"

	//AWS
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/autoscaling"
)

func TestListClusterNodes(t *testing.T) {
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
			t.Errorf("Test Description: %s \n expected error(bool): %v, Actual err: %v", test.TestDescription, test.ExpectError, err)
		}
		//check if the node is actually cordoned/uncordoned.
		if test.Cordon && test.Node != nil && !test.Node.Spec.Unschedulable {
			t.Errorf("Test Description: %s \n expected the node to be cordoned but it is uncordoned", test.TestDescription)
		}
		if !test.Cordon && test.Node != nil && test.Node.Spec.Unschedulable {
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
			t.Errorf("Test Description: %s \n expected error(bool): %v, Actual err: %v", test.TestDescription, test.ExpectError, err)
		}
	}

}

func TestIsInstanceDrifted(t *testing.T) {
	var tests = []struct {
		TestDescription string
		Reconciler      *RollingUpgradeReconciler
		RollingUpgrade  *v1alpha1.RollingUpgrade
		Instance        *autoscaling.Instance
		ExpectedValue   bool
	}{
		{
			"Instance has the same launch config as the ASG, expect false from IsInstanceDrifted",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			createASGInstance("mock-instance-1", "mock-launch-config-1"),
			false,
		},
		{
			"Instance has different launch config from the ASG, expect true from IsInstanceDrifted",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			createASGInstance("mock-instance-1", "different-launch-config"),
			true,
		},
		{
			"Instance has no launch config, expect true from IsInstanceDrifted",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			createASGInstance("mock-instance-1", ""),
			true,
		},
	}
	for _, test := range tests {
		test.Reconciler.Cloud.ScalingGroups = createASGs()
		actualValue := test.Reconciler.IsInstanceDrifted(test.RollingUpgrade, test.Instance)
		if actualValue != test.ExpectedValue {
			t.Errorf("Test Description: %s \n expected value: %v, actual value: %v", test.TestDescription, test.ExpectedValue, actualValue)
		}
	}
}

func TestIsScalingGroupDrifted(t *testing.T) {
	var tests = []struct {
		TestDescription string
		Reconciler      *RollingUpgradeReconciler
		RollingUpgrade  *v1alpha1.RollingUpgrade
		AsgClient       *MockAutoscalingGroup
		ExpectedValue   bool
	}{
		{
			"All instances have the same launch config as the ASG, expect false from IsScalingGroupDrifted",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			createASGClient(),
			false,
		},
		{
			"All instances have different launch config as the ASG, expect true from IsScalingGroupDrifted",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			func() *MockAutoscalingGroup {
				newAsgClient := createASGClient()
				newAsgClient.autoScalingGroups[0].LaunchConfigurationName = aws.String("different-launch-config")
				return newAsgClient
			}(),
			true,
		},
	}
	for _, test := range tests {
		test.Reconciler.Cloud.ScalingGroups = test.AsgClient.autoScalingGroups
		test.Reconciler.Auth.AmazonClientSet.AsgClient = test.AsgClient

		actualValue := test.Reconciler.IsScalingGroupDrifted(test.RollingUpgrade)
		if actualValue != test.ExpectedValue {
			t.Errorf("Test Description: %s \n expected value: %v, actual value: %v", test.TestDescription, test.ExpectedValue, actualValue)
		}
	}

}

func TestRotateNodes(t *testing.T) {
	var tests = []struct {
		TestDescription     string
		Reconciler          *RollingUpgradeReconciler
		RollingUpgrade      *v1alpha1.RollingUpgrade
		AsgClient           *MockAutoscalingGroup
		ExpectedValue       bool
		ExpectedStatusValue string
	}{
		{
			"All instances have different launch config as the ASG, expect true from IsScalingGroupDrifted",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			func() *MockAutoscalingGroup {
				newAsgClient := createASGClient()
				newAsgClient.autoScalingGroups[0].LaunchConfigurationName = aws.String("different-launch-config")
				return newAsgClient
			}(),
			true,
			v1alpha1.StatusRunning,
		},
		{
			"All instances have the same launch config as the ASG, expect false from IsScalingGroupDrifted",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			createASGClient(),
			false,
			v1alpha1.StatusComplete,
		},
	}
	for _, test := range tests {
		test.Reconciler.Cloud.ScalingGroups = test.AsgClient.autoScalingGroups
		test.Reconciler.Auth.AmazonClientSet.AsgClient = test.AsgClient

		err := test.Reconciler.RotateNodes(test.RollingUpgrade)
		if err != nil {
			t.Errorf("Test Description: \n expected value: nil, actual value: %v", err)
		}
		if test.RollingUpgrade.CurrentStatus() != test.ExpectedStatusValue {
			t.Errorf("Test Description: %s \n expected value: %s, actual value: %s", test.TestDescription, test.ExpectedStatusValue, test.RollingUpgrade.CurrentStatus())
		}
	}

}

func TestDesiredNodesReady(t *testing.T) {
	var tests = []struct {
		TestDescription string
		Reconciler      *RollingUpgradeReconciler
		RollingUpgrade  *v1alpha1.RollingUpgrade
		AsgClient       *MockAutoscalingGroup
		ClusterNodes    *corev1.NodeList
		ExpectedValue   bool
	}{
		{
			"Desired nodes are ready",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			createASGClient(),
			createNodeList(),
			true,
		},
		{
			"Desired instances are not ready (desiredCount != inServiceCount)",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			func() *MockAutoscalingGroup {
				newAsgClient := createASGClient()
				newAsgClient.autoScalingGroups[0].DesiredCapacity = func(x int) *int64 { i := int64(x); return &i }(4)
				return newAsgClient
			}(),
			createNodeList(),
			false,
		},
		{
			"None of the nodes are ready (desiredCount != readyCount)",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			createASGClient(),
			func() *corev1.NodeList {
				var nodeList = &corev1.NodeList{Items: []corev1.Node{}}
				for i := 0; i < 3; i++ {
					node := createNode()
					node.Status.Conditions = []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
					}
					nodeList.Items = append(nodeList.Items, *node)
				}
				return nodeList
			}(),
			false,
		},
		{
			"None of the instances are InService (desiredCount != inServiceCount)",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			func() *MockAutoscalingGroup {
				newAsgClient := createASGClient()
				newAsgClient.autoScalingGroups[0].Instances = []*autoscaling.Instance{
					&autoscaling.Instance{InstanceId: aws.String("mock-instance-1"), LifecycleState: aws.String("Pending")},
					&autoscaling.Instance{InstanceId: aws.String("mock-instance-2"), LifecycleState: aws.String("Terminating")},
					&autoscaling.Instance{InstanceId: aws.String("mock-instance-3"), LifecycleState: aws.String("Terminating")},
				}
				return newAsgClient
			}(),
			createNodeList(),
			false,
		},
	}
	for _, test := range tests {
		test.Reconciler.Cloud.ScalingGroups = test.AsgClient.autoScalingGroups
		test.Reconciler.Cloud.ClusterNodes = test.ClusterNodes
		test.Reconciler.Auth.AmazonClientSet.AsgClient = test.AsgClient

		actualValue := test.Reconciler.DesiredNodesReady(test.RollingUpgrade)
		if actualValue != test.ExpectedValue {
			t.Errorf("Test Description: %s \n expected value: %v, actual value: %v", test.TestDescription, test.ExpectedValue, actualValue)
		}
	}
}
