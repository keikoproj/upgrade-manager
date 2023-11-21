package controllers

import (
	"context"
	"os"
	"testing"

	"k8s.io/apimachinery/pkg/util/intstr"
	drain "k8s.io/kubectl/pkg/drain"

	"time"

	awsprovider "github.com/keikoproj/upgrade-manager/controllers/providers/aws"
	corev1 "k8s.io/api/core/v1"

	//AWS
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// This test checks implementation of our DrainNode which does both cordon + drain
func TestDrainNode(t *testing.T) {
	var tests = []struct {
		TestDescription string
		Reconciler      *RollingUpgradeReconciler
		Node            *corev1.Node
		ExpectError     bool
	}{
		{
			"Drain should succeed as node is registered with fakeClient",
			createRollingUpgradeReconciler(t),
			createNode("mock-node-1"),
			false,
		},
		{
			"Drain should fail as node is not registered with fakeClient",
			createRollingUpgradeReconciler(t),
			&corev1.Node{},
			true,
		},
	}

	for _, test := range tests {
		rollupCtx := createRollingUpgradeContext(test.Reconciler)
		err := rollupCtx.Auth.DrainNode(
			test.Node,
			time.Duration(rollupCtx.RollingUpgrade.PostDrainDelaySeconds()),
			900,
			rollupCtx.Auth.Kubernetes,
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
		Node            *corev1.Node
		Cordon          bool
		ExpectError     bool
	}{
		{
			"Cordon should succeed as node is registered with fakeClient",
			createRollingUpgradeReconciler(t),
			createNode("mock-node-1"),
			true,
			false,
		},
		{
			"Cordon should fail as node is not registered with fakeClient",
			createRollingUpgradeReconciler(t),
			&corev1.Node{},
			true,
			true,
		},
		{
			"Uncordon should succeed as node is registered with fakeClient",
			createRollingUpgradeReconciler(t),
			func() *corev1.Node {
				node := createNode("mock-node-1")
				node.Spec.Unschedulable = true
				return node
			}(),
			false,
			false,
		},
		{
			"Uncordon should fail as node is not registered with fakeClient",
			createRollingUpgradeReconciler(t),
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
		rollupCtx := createRollingUpgradeContext(test.Reconciler)
		helper := &drain.Helper{
			Client:              rollupCtx.Auth.Kubernetes,
			Force:               true,
			GracePeriodSeconds:  -1,
			IgnoreAllDaemonSets: true,
			Out:                 os.Stdout,
			ErrOut:              os.Stdout,
			DeleteEmptyDirData:  true,
			Timeout:             900,
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
		Node            *corev1.Node
		ExpectError     bool
	}{
		{
			"Drain should succeed as node is registered with fakeClient",
			createRollingUpgradeReconciler(t),
			createNode("mock-node-1"),
			false,
		},
		// This test should fail, create an upstream ticket.
		// https://github.com/kubernetes/kubectl/blob/d5b32e7f3c0260abb5b1cd5a62d4eb1de287bc93/pkg/drain/default.go#L33
		// {
		// 	"Drain should fail as node is not registered with fakeClient",
		// 	createRollingUpgradeReconciler(t),
		// 	&corev1.Node{},
		// 	true,
		// },
	}
	for _, test := range tests {
		rollupCtx := createRollingUpgradeContext(test.Reconciler)
		helper := &drain.Helper{
			Client:              rollupCtx.Auth.Kubernetes,
			Force:               true,
			GracePeriodSeconds:  -1,
			IgnoreAllDaemonSets: true,
			Out:                 os.Stdout,
			ErrOut:              os.Stdout,
			DeleteEmptyDirData:  true,
			Timeout:             900,
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
		Instance        *autoscaling.Instance
		AsgName         *string
		ExpectedValue   bool
	}{
		{
			"Instance has the same launch config as the ASG, expect false from IsInstanceDrifted",
			createRollingUpgradeReconciler(t),
			createASGInstance("mock-instance-1", "mock-launch-config-1"),
			aws.String("mock-asg-1"),
			false,
		},
		{
			"Instance has different launch config from the ASG, expect true from IsInstanceDrifted",
			createRollingUpgradeReconciler(t),
			createASGInstance("mock-instance-1", "different-launch-config"),
			aws.String("mock-asg-1"),
			true,
		},
		{
			"Instance has no launch config, expect true from IsInstanceDrifted",
			createRollingUpgradeReconciler(t),
			createASGInstance("mock-instance-1", ""),
			aws.String("mock-asg-1"),
			true,
		},
		{
			"Instance has launch template, expect true from IsInstanceDrifted",
			createRollingUpgradeReconciler(t),
			createASGInstanceWithLaunchTemplate("mock-instance-1", "mock-launch-template-4"),
			aws.String("mock-asg-4"),
			true,
		},
		{
			"Instance has mixed instances launch template, expect true from IsInstanceDrifted",
			createRollingUpgradeReconciler(t),
			createASGInstanceWithLaunchTemplate("mock-instance-1", "mock-launch-template-5"),
			aws.String("mock-asg-5"),
			true,
		},
	}
	for _, test := range tests {
		rollupCtx := createRollingUpgradeContext(test.Reconciler)
		rollupCtx.Cloud.ScalingGroups = createASGs()
		rollupCtx.RollingUpgrade.Spec.AsgName = *test.AsgName
		actualValue := rollupCtx.IsInstanceDrifted(test.Instance)
		if actualValue != test.ExpectedValue {
			t.Errorf("Test Description: %s \n expected value: %v, actual value: %v", test.TestDescription, test.ExpectedValue, actualValue)
		}
	}
}

func TestIsScalingGroupDrifted(t *testing.T) {
	var tests = []struct {
		TestDescription string
		Reconciler      *RollingUpgradeReconciler
		AsgClient       *MockAutoscalingGroup
		ExpectedValue   bool
	}{
		{
			"All instances have the same launch config as the ASG, expect false from IsScalingGroupDrifted",
			createRollingUpgradeReconciler(t),
			createASGClient(),
			false,
		},
		{
			"All instances have different launch config as the ASG, expect true from IsScalingGroupDrifted",
			createRollingUpgradeReconciler(t),
			func() *MockAutoscalingGroup {
				newAsgClient := createASGClient()
				newAsgClient.autoScalingGroups[0].LaunchConfigurationName = aws.String("different-launch-config")
				return newAsgClient
			}(),
			true,
		},
	}
	for _, test := range tests {
		rollupCtx := createRollingUpgradeContext(test.Reconciler)
		rollupCtx.Cloud.ScalingGroups = test.AsgClient.autoScalingGroups
		rollupCtx.Auth.AmazonClientSet.AsgClient = test.AsgClient

		actualValue := rollupCtx.IsScalingGroupDrifted()
		if actualValue != test.ExpectedValue {
			t.Errorf("Test Description: %s \n expected value: %v, actual value: %v", test.TestDescription, test.ExpectedValue, actualValue)
		}
	}

}

func TestRotateNodes(t *testing.T) {
	var tests = []struct {
		TestDescription       string
		Reconciler            *RollingUpgradeReconciler
		AsgClient             *MockAutoscalingGroup
		RollingUpgradeContext *RollingUpgradeContext
		ExpectedValue         bool
		ExpectedStatusValue   string
	}{
		{
			"All instances have different launch config as the ASG, RotateNodes() should not mark CR complete",
			createRollingUpgradeReconciler(t),
			createASGClient(),
			func() *RollingUpgradeContext {
				newRollingUpgradeContext := createRollingUpgradeContext(createRollingUpgradeReconciler(t))
				newRollingUpgradeContext.RollingUpgrade.Spec.AsgName = "mock-asg-2" // The instances in mock-asg are drifted
				return newRollingUpgradeContext
			}(),
			true,
			v1alpha1.StatusRunning,
		},
		{
			"All instances have same launch config as the ASG, RotateNodes() should mark CR complete",
			createRollingUpgradeReconciler(t),
			createASGClient(),
			createRollingUpgradeContext(createRollingUpgradeReconciler(t)),
			false,
			v1alpha1.StatusComplete,
		},
	}
	for _, test := range tests {
		rollupCtx := test.RollingUpgradeContext
		rollupCtx.Cloud.ScalingGroups = test.AsgClient.autoScalingGroups
		rollupCtx.Auth.AmazonClientSet.AsgClient = test.AsgClient
		rollupCtx.EarlyCordonNodes = true

		err := rollupCtx.RotateNodes()
		if err != nil {
			t.Errorf("Test Description: %s \n error: %v", test.TestDescription, err)
		}
		if rollupCtx.RollingUpgrade.CurrentStatus() != test.ExpectedStatusValue {
			t.Errorf("Test Description: %s \n expected value: %s, actual value: %s", test.TestDescription, test.ExpectedStatusValue, rollupCtx.RollingUpgrade.CurrentStatus())
		}
	}

}

func TestDesiredNodesReady(t *testing.T) {
	var tests = []struct {
		TestDescription string
		Reconciler      *RollingUpgradeReconciler
		AsgClient       *MockAutoscalingGroup
		ClusterNodes    []*corev1.Node
		ExpectedValue   bool
	}{
		{
			"Desired nodes are ready",
			createRollingUpgradeReconciler(t),
			createASGClient(),
			createNodeSlice(),
			true,
		},
		{
			"Desired instances are not ready (desiredCount != inServiceCount)",
			createRollingUpgradeReconciler(t),
			func() *MockAutoscalingGroup {
				newAsgClient := createASGClient()
				newAsgClient.autoScalingGroups[0].DesiredCapacity = func(x int) *int64 { i := int64(x); return &i }(4)
				return newAsgClient
			}(),
			createNodeSlice(),
			false,
		},
		{
			"None of the nodes are ready (desiredCount != readyCount)",
			createRollingUpgradeReconciler(t),
			createASGClient(),
			func() []*corev1.Node {
				var nodeSlice []*corev1.Node
				for i := 0; i < 3; i++ {
					node := createNode("mock-node-1")
					node.Status.Conditions = []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
					}
					nodeSlice = append(nodeSlice, node)
				}
				return nodeSlice
			}(),
			false,
		},
		{
			"None of the instances are InService (desiredCount != inServiceCount)",
			createRollingUpgradeReconciler(t),
			func() *MockAutoscalingGroup {
				newAsgClient := createASGClient()
				newAsgClient.autoScalingGroups[0].Instances = []*autoscaling.Instance{
					&autoscaling.Instance{InstanceId: aws.String("mock-instance-1"), LifecycleState: aws.String("Pending")},
					&autoscaling.Instance{InstanceId: aws.String("mock-instance-2"), LifecycleState: aws.String("Terminating")},
					&autoscaling.Instance{InstanceId: aws.String("mock-instance-3"), LifecycleState: aws.String("Terminating")},
				}
				return newAsgClient
			}(),
			createNodeSlice(),
			false,
		},
	}
	for _, test := range tests {
		rollupCtx := createRollingUpgradeContext(test.Reconciler)
		rollupCtx.Cloud.ScalingGroups = test.AsgClient.autoScalingGroups
		rollupCtx.Cloud.ClusterNodes = test.ClusterNodes
		rollupCtx.Auth.AmazonClientSet.AsgClient = test.AsgClient

		actualValue := rollupCtx.DesiredNodesReady()
		if actualValue != test.ExpectedValue {
			t.Errorf("Test Description: %s \n expected value: %v, actual value: %v", test.TestDescription, test.ExpectedValue, actualValue)
		}
	}
}

func TestSetBatchStandBy(t *testing.T) {
	var tests = []struct {
		TestDescription      string
		Reconciler           *RollingUpgradeReconciler
		RollingUpgrade       *v1alpha1.RollingUpgrade
		AsgClient            *MockAutoscalingGroup
		ClusterNodes         []*corev1.Node
		ExpectedValue        error
		InstanceStandByLimit int
	}{
		{
			"Single Batch",
			createRollingUpgradeReconciler(t),
			func() *v1alpha1.RollingUpgrade {
				rollingUpgrade := createRollingUpgrade()
				rollingUpgrade.Spec.Strategy.MaxUnavailable = intstr.IntOrString{StrVal: "100%"}
				return rollingUpgrade
			}(),
			createASGClient(),
			createNodeSlice(),
			nil,
			3,
		},
		{
			"Multiple Batches",
			createRollingUpgradeReconciler(t),
			func() *v1alpha1.RollingUpgrade {
				rollingUpgrade := createRollingUpgrade()
				rollingUpgrade.Spec.Strategy.MaxUnavailable = intstr.IntOrString{StrVal: "100%"}
				return rollingUpgrade
			}(),
			createASGClient(),
			createNodeSlice(),
			nil,
			1,
		},
		{
			"Multiple Batches with some overflow",
			createRollingUpgradeReconciler(t),
			func() *v1alpha1.RollingUpgrade {
				rollingUpgrade := createRollingUpgrade()
				rollingUpgrade.Spec.Strategy.MaxUnavailable = intstr.IntOrString{StrVal: "100%"}
				return rollingUpgrade
			}(),
			createASGClient(),
			createNodeSlice(),
			nil,
			2,
		},
	}
	for _, test := range tests {
		awsprovider.InstanceStandByLimit = test.InstanceStandByLimit
		rollupCtx := createRollingUpgradeContext(test.Reconciler)
		rollupCtx.RollingUpgrade = test.RollingUpgrade
		rollupCtx.Cloud.ScalingGroups = test.AsgClient.autoScalingGroups
		rollupCtx.Cloud.ClusterNodes = test.ClusterNodes
		rollupCtx.Auth.AmazonClientSet.AsgClient = test.AsgClient

		batch := test.AsgClient.autoScalingGroups[0].Instances
		actualValue := rollupCtx.SetBatchStandBy(awsprovider.GetInstanceIDs(batch))
		if actualValue != test.ExpectedValue {
			t.Errorf("Test Description: %s \n expected value: %v, actual value: %v", test.TestDescription, test.ExpectedValue, actualValue)
		}
	}
}

func TestIgnoreDrainFailuresAndDrainTimeout(t *testing.T) {
	var tests = []struct {
		TestDescription     string
		Reconciler          *RollingUpgradeReconciler
		RollingUpgrade      *v1alpha1.RollingUpgrade
		AsgClient           *MockAutoscalingGroup
		ClusterNodes        []*corev1.Node
		ExpectedStatusValue string
	}{
		{
			"CR spec has IgnoreDrainFailures as nil, so default false should be considered",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			createASGClient(),
			createNodeSlice(),
			v1alpha1.StatusComplete,
		},
		{
			"CR spec has IgnoreDrainFailures as true, so default false should not be considered",
			createRollingUpgradeReconciler(t),
			func() *v1alpha1.RollingUpgrade {
				rollingUpgrade := createRollingUpgrade()
				ignoreDrainFailuresValue := true
				rollingUpgrade.Spec.IgnoreDrainFailures = &ignoreDrainFailuresValue
				return rollingUpgrade
			}(),
			createASGClient(),
			createNodeSlice(),
			v1alpha1.StatusComplete,
		},
		{
			"CR spec has DrainTimeout as nil, so default value of 900 should be considered",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			createASGClient(),
			createNodeSlice(),
			v1alpha1.StatusComplete,
		},
		{
			"CR spec has DrainTimeout as 1800, so default value of 900 should not be considered",
			createRollingUpgradeReconciler(t),
			func() *v1alpha1.RollingUpgrade {
				rollingUpgrade := createRollingUpgrade()
				drainTimeoutValue := 1800
				rollingUpgrade.Spec.Strategy.DrainTimeout = &drainTimeoutValue
				return rollingUpgrade
			}(),
			createASGClient(),
			createNodeSlice(),
			v1alpha1.StatusComplete,
		},
	}
	for _, test := range tests {
		rollupCtx := createRollingUpgradeContext(test.Reconciler)
		rollupCtx.RollingUpgrade = test.RollingUpgrade
		rollupCtx.Cloud.ScalingGroups = test.AsgClient.autoScalingGroups
		rollupCtx.Cloud.ClusterNodes = test.ClusterNodes
		rollupCtx.Auth.AmazonClientSet.AsgClient = test.AsgClient
		rollupCtx.EarlyCordonNodes = true

		err := rollupCtx.RotateNodes()
		if err != nil {
			t.Errorf("Test Description: %s \n error: %v", test.TestDescription, err)
		}
		if rollupCtx.RollingUpgrade.CurrentStatus() != test.ExpectedStatusValue {
			t.Errorf("Test Description: %s \n expected value: %s, actual value: %s", test.TestDescription, test.ExpectedStatusValue, rollupCtx.RollingUpgrade.CurrentStatus())
		}
	}
}

func TestClusterBallooning(t *testing.T) {
	var tests = []struct {
		TestDescription     string
		Reconciler          *RollingUpgradeReconciler
		RollingUpgrade      *v1alpha1.RollingUpgrade
		AsgClient           *MockAutoscalingGroup
		ClusterNodes        []*corev1.Node
		BatchSize           int
		IsClusterBallooning bool
		AllowedBatchSize    int
	}{
		{
			"ClusterBallooning - maxReplacementNodes is not set, expect no clusterBallooning",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			createASGClient(),
			createNodeSlice(),
			3,
			false,
			3, // because mock-asg-1 has 3 instances
		},
		{
			"ClusterBallooning - cluster has hit maxReplacementNodes capacity, expect clusterBallooning to be true",
			func() *RollingUpgradeReconciler {
				reconciler := createRollingUpgradeReconciler(t)
				reconciler.MaxReplacementNodes = 500
				reconciler.ReplacementNodesMap.Store("ReplacementNodes", 500)
				reconciler.EarlyCordonNodes = true
				return reconciler
			}(),
			createRollingUpgrade(),
			createASGClient(),
			createNodeSlice(),
			3,
			true,
			0,
		},
		{
			"ClusterBallooning - cluster is below maxReplacementNodes capacity, expect no clusterBallooning",
			func() *RollingUpgradeReconciler {
				reconciler := createRollingUpgradeReconciler(t)
				reconciler.MaxReplacementNodes = 500
				reconciler.ReplacementNodesMap.Store("ReplacementNodes", 100)
				return reconciler
			}(),
			createRollingUpgrade(),
			createASGClient(),
			createNodeSlice(),
			400,
			false,
			400,
		},
		{
			"ClusterBallooning - cluster is about to hit maxReplacementNodes capacity, expect reduced batchSize",
			func() *RollingUpgradeReconciler {
				reconciler := createRollingUpgradeReconciler(t)
				reconciler.MaxReplacementNodes = 100
				reconciler.ReplacementNodesMap.Store("ReplacementNodes", 97)
				return reconciler
			}(),
			createRollingUpgrade(),
			createASGClient(),
			createNodeSlice(),
			100,
			false,
			3,
		},
		{
			"ClusterBallooning - cluster is about to hit maxReplacementNodes capacity, expect reduced batchSize",
			func() *RollingUpgradeReconciler {
				reconciler := createRollingUpgradeReconciler(t)
				reconciler.MaxReplacementNodes = 5
				reconciler.ReplacementNodesMap.Store("ReplacementNodes", 3)
				return reconciler
			}(),
			createRollingUpgrade(),
			createASGClient(),
			createNodeSlice(),
			3,
			false,
			2,
		},
	}
	for _, test := range tests {
		rollupCtx := createRollingUpgradeContext(test.Reconciler)
		rollupCtx.RollingUpgrade = test.RollingUpgrade
		rollupCtx.Cloud.ScalingGroups = test.AsgClient.autoScalingGroups
		rollupCtx.Cloud.ClusterNodes = test.ClusterNodes
		rollupCtx.Auth.AmazonClientSet.AsgClient = test.AsgClient

		isClusterBallooning, allowedBatchSize := rollupCtx.ClusterBallooning(test.BatchSize)
		if isClusterBallooning != test.IsClusterBallooning {
			t.Errorf("Test Description: %s \n isClusterBallooning, expected: %v \n actual: %v", test.TestDescription, test.IsClusterBallooning, isClusterBallooning)
		}
		if allowedBatchSize != test.AllowedBatchSize {
			t.Errorf("Test Description: %s \n allowedBatchSize expected: %v \n actual: %v", test.TestDescription, test.AllowedBatchSize, allowedBatchSize)
		}

	}
}

func TestEarlyCordonFunction(t *testing.T) {
	var tests = []struct {
		TestDescription            string
		Reconciler                 *RollingUpgradeReconciler
		RollingUpgrade             *v1alpha1.RollingUpgrade
		AsgClient                  *MockAutoscalingGroup
		ClusterNodes               []*corev1.Node
		ExpectedUnschdeulableValue bool
	}{
		{
			"Test if all the nodes are cordoned by default.",
			createRollingUpgradeReconciler(t),
			createRollingUpgrade(),
			createASGClient(),
			createNodeSlice(),
			true,
		},
	}
	for _, test := range tests {
		rollupCtx := createRollingUpgradeContext(test.Reconciler)
		rollupCtx.RollingUpgrade = test.RollingUpgrade
		rollupCtx.Cloud.ScalingGroups = test.AsgClient.autoScalingGroups
		rollupCtx.Cloud.ClusterNodes = test.ClusterNodes
		rollupCtx.Auth.AmazonClientSet.AsgClient = test.AsgClient

		_, err := rollupCtx.EarlyCordonAllNodes()
		if err != nil {
			t.Errorf("Test Description: %s \n error: %v", test.TestDescription, err)
		}
		for _, node := range rollupCtx.Cloud.ClusterNodes {
			if test.ExpectedUnschdeulableValue != node.Spec.Unschedulable {
				t.Errorf("Test Description: %s \n expectedValue: %v, actualValue: %v", test.TestDescription, test.ExpectedUnschdeulableValue, node.Spec.Unschedulable)
			}
		}
	}
}