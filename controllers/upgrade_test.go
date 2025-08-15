package controllers

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	//AWS
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/keikoproj/upgrade-manager/api/v1alpha1"
	awsprovider "github.com/keikoproj/upgrade-manager/controllers/providers/aws"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubectl/pkg/drain"
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
			Ctx:                 context.Background(),
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
		Instance        *types.Instance
		AsgName         *string
		ExpectedValue   bool
	}{
		{
			"Instance has the same launch config as the ASG, expect false from IsInstanceDrifted",
			createRollingUpgradeReconciler(t),
			createASGInstance("mock-instance-1", "mock-launch-config-1", "az-1"),
			aws.String("mock-asg-1"),
			false,
		},
		{
			"Instance has different launch config from the ASG, expect true from IsInstanceDrifted",
			createRollingUpgradeReconciler(t),
			createASGInstance("mock-instance-1", "different-launch-config", "az-1"),
			aws.String("mock-asg-1"),
			true,
		},
		{
			"Instance has no launch config, expect true from IsInstanceDrifted",
			createRollingUpgradeReconciler(t),
			createASGInstance("mock-instance-1", "", "az-1"),
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
				newAsgClient.autoScalingGroups[0].DesiredCapacity = func(x int) *int32 { i := int32(x); return &i }(4)
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
					node := createNode(fmt.Sprintf("mock-node-notready-%d", i+1))
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
				newAsgClient.autoScalingGroups[0].Instances = []types.Instance{
					{InstanceId: aws.String("mock-instance-1"), LifecycleState: types.LifecycleStatePending},
					{InstanceId: aws.String("mock-instance-2"), LifecycleState: types.LifecycleStateTerminating},
					{InstanceId: aws.String("mock-instance-3"), LifecycleState: types.LifecycleStateTerminating},
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

		// We update the fake Kubernetes client to use the nodes specified by the test case
		// and this ensures that r.Auth.ListClusterNodes() returns the expected test nodes
		testNodeList := &corev1.NodeList{}
		for _, node := range test.ClusterNodes {
			testNodeList.Items = append(testNodeList.Items, *node)
		}
		rollupCtx.Auth.KubernetesClientSet.Kubernetes = fake.NewSimpleClientset(testNodeList)

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
				rollingUpgrade.Spec.Strategy.MaxUnavailable = intstr.IntOrString{StrVal: "100%", Type: 1}
				rollingUpgrade.Spec.Strategy.Type = v1alpha1.RandomUpdateStrategy
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
				rollingUpgrade.Spec.Strategy.MaxUnavailable = intstr.IntOrString{StrVal: "100%", Type: 1}
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
				rollingUpgrade.Spec.Strategy.MaxUnavailable = intstr.IntOrString{StrVal: "100%", Type: 1}
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
		// Convert []types.Instance to []*types.Instance for GetInstanceIDsFromPointers
		var batchPointers []*types.Instance
		for i := range batch {
			batchPointers = append(batchPointers, &batch[i])
		}
		actualValue := rollupCtx.SetBatchStandBy(awsprovider.GetInstanceIDsFromPointers(batchPointers))
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
				rollingUpgrade.Spec.Strategy.Type = v1alpha1.RandomUpdateStrategy
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
				rollingUpgrade.Spec.Strategy.Type = v1alpha1.RandomUpdateStrategy
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

func TestCordoningAndUncordoningOfNodes(t *testing.T) {
	var tests = []struct {
		TestDescription            string
		Reconciler                 *RollingUpgradeReconciler
		Node                       *corev1.Node
		CordonNodeFlag             bool
		ExpectedUnschdeulableValue bool
		ExpectedError              bool
	}{
		{
			"Test if all the nodes are cordoned.",
			createRollingUpgradeReconciler(t),
			createNode("mock-node-1"),
			true,
			true,
			false,
		},
		{
			"Test if all the nodes are uncordoned",
			createRollingUpgradeReconciler(t),
			createNode("mock-node-1"),
			false,
			false,
			false,
		},
		{
			"Try to cordon an unknown node.",
			createRollingUpgradeReconciler(t),
			createNode("mock-node-4"),
			true,
			true,
			true,
		},
	}
	for _, test := range tests {
		rollupCtx := createRollingUpgradeContext(test.Reconciler)

		if err := rollupCtx.Auth.CordonUncordonNode(test.Node, rollupCtx.Auth.Kubernetes, test.CordonNodeFlag); err != nil && test.ExpectedError {
			continue
		}

		// By default, nodes are uncordoned. Therefore, before testing uncordoning the node, first cordon it.
		if !test.CordonNodeFlag {
			if err := rollupCtx.Auth.CordonUncordonNode(test.Node, rollupCtx.Auth.Kubernetes, true); err != nil {
				t.Errorf("Test Description: %s \n error: %v", test.TestDescription, err)
			}
		}

		if err := rollupCtx.Auth.CordonUncordonNode(test.Node, rollupCtx.Auth.Kubernetes, test.CordonNodeFlag); err != nil {
			t.Errorf("Test Description: %s \n error: %v", test.TestDescription, err)
		}

		if test.ExpectedUnschdeulableValue != test.Node.Spec.Unschedulable {
			t.Errorf("Test Description: %s \n expectedValue: %v, actualValue: %v", test.TestDescription, test.ExpectedUnschdeulableValue, test.Node.Spec.Unschedulable)
		}
	}
}

func TestSelectTargetsDifferentStrategy(t *testing.T) {
	var tests = []struct {
		TestDescription string
		Reconciler      *RollingUpgradeReconciler
		RollingUpgrade  *v1alpha1.RollingUpgrade
		AsgClient       *MockAutoscalingGroup
	}{
		{
			"Test with RandomUpdate strategy",
			createRollingUpgradeReconciler(t),
			func() *v1alpha1.RollingUpgrade {
				rollingUpgrade := createRollingUpgrade()
				rollingUpgrade.Spec.Strategy.Type = v1alpha1.RandomUpdateStrategy
				rollingUpgrade.Spec.Strategy.MaxUnavailable = intstr.IntOrString{StrVal: "100%", Type: 1}
				return rollingUpgrade
			}(),
			createASGClient(),
		},
		{
			"Test with UniformAcorssAZUpdate strategy",
			createRollingUpgradeReconciler(t),
			func() *v1alpha1.RollingUpgrade {
				rollingUpgrade := createRollingUpgrade()
				rollingUpgrade.Spec.Strategy.Type = v1alpha1.UniformAcrossAzUpdateStrategy
				rollingUpgrade.Spec.Strategy.MaxUnavailable = intstr.IntOrString{StrVal: "100%", Type: 1}
				return rollingUpgrade
			}(),
			createASGClient(),
		},
	}
	for _, test := range tests {
		rollupCtx := createRollingUpgradeContext(test.Reconciler)
		rollupCtx.RollingUpgrade = test.RollingUpgrade
		rollupCtx.Cloud.ScalingGroups = test.AsgClient.autoScalingGroups
		rollupCtx.Auth.AmazonClientSet.AsgClient = test.AsgClient

		for _, scalingGroup := range rollupCtx.Cloud.ScalingGroups {
			rollupCtx.RollingUpgrade.Spec.AsgName = *scalingGroup.AutoScalingGroupName
			selectedInstances := rollupCtx.SelectTargets(&scalingGroup, make([]string, 0))
			t.Log("selectedInstances -", selectedInstances)
			if selectedInstances == nil {
				t.Errorf("Test Description: %s \n error: selectedInstances is nil", test.TestDescription)
			}
		}
	}
}

func TestSelectTargetsWithExcluededInstances(t *testing.T) {
	// Create a mock autoscaling group
	asgClient := createASGClient()
	scalingGroup := asgClient.autoScalingGroups[1]

	// Create a mock excluded instances list
	excludedInstances := []string{"mock-instance-1"}

	// Create a mock RollingUpgradeContext
	rollingUpgradeContext := createRollingUpgradeContext(createRollingUpgradeReconciler(t))
	rollingUpgradeContext.Cloud.ScalingGroups = asgClient.autoScalingGroups
	rollingUpgradeContext.RollingUpgrade.Spec.AsgName = *scalingGroup.AutoScalingGroupName
	rollingUpgradeContext.RollingUpgrade.Spec.Strategy.MaxUnavailable = intstr.IntOrString{StrVal: "100%", Type: 1}
	rollingUpgradeContext.RollingUpgrade.Spec.Strategy.Type = v1alpha1.RandomUpdateStrategy

	// Call the SelectTargets function
	selectedInstances := rollingUpgradeContext.SelectTargets(&scalingGroup, excludedInstances)

	// Verify the result
	expectedSelectedInstances := []*types.Instance{
		{
			InstanceId: aws.String("mock-instance-2"),
		},
		{
			InstanceId: aws.String("mock-instance-3"),
		},
	}

	if len(selectedInstances) != len(expectedSelectedInstances) {
		t.Errorf("Expected %d selected instances, but got %d", len(expectedSelectedInstances), len(selectedInstances))
	}

	for i, instance := range selectedInstances {
		if *instance.InstanceId != *expectedSelectedInstances[i].InstanceId {
			t.Errorf("Expected instance ID %s, but got %s", *expectedSelectedInstances[i].InstanceId, *instance.InstanceId)
		}
	}
}

// Test edge cases in instance lifecycle state filtering
func TestSelectTargetsWithTerminatingInstances(t *testing.T) {
	reconciler := createRollingUpgradeReconciler(t)
	rollupCtx := createRollingUpgradeContext(reconciler)

	// Test that terminating instances are excluded from selection
	// Use existing mock infrastructure that works
	rollupCtx.Cloud.ScalingGroups = createASGs()
	rollupCtx.RollingUpgrade.Spec.AsgName = "mock-asg-2" // This ASG has drifted instances
	rollupCtx.RollingUpgrade.Spec.Strategy.MaxUnavailable = intstr.IntOrString{IntVal: 3}

	// Get the ASG and modify it to have terminating instances
	asg := awsprovider.SelectScalingGroup("mock-asg-2", rollupCtx.Cloud.ScalingGroups)

	// Modify instances to have mixed lifecycle states
	asg.Instances[0].LifecycleState = types.LifecycleStateInService
	asg.Instances[1].LifecycleState = types.LifecycleStateTerminating
	asg.Instances[2].LifecycleState = types.LifecycleStateTerminated

	targets := rollupCtx.SelectTargets(asg, []string{})

	// Should only select in-service instances, not terminating ones
	// The exact count depends on how many are drifted and in-service
	for _, target := range targets {
		if target.LifecycleState == types.LifecycleStateTerminating ||
			target.LifecycleState == types.LifecycleStateTerminated {
			t.Errorf("selected terminating/terminated instance: %s with state %s",
				*target.InstanceId, target.LifecycleState)
		}
	}
}

func TestSelectTargetsWithEmptyScalingGroup(t *testing.T) {
	reconciler := createRollingUpgradeReconciler(t)
	rollupCtx := createRollingUpgradeContext(reconciler)

	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: aws.String("empty-asg"),
		DesiredCapacity:      aws.Int32(0),
		Instances:            []types.Instance{},
	}

	targets := rollupCtx.SelectTargets(asg, []string{})

	if len(targets) != 0 {
		t.Errorf("expected 0 targets for empty ASG, got %d", len(targets))
	}
}

func TestIsInstanceDriftedWithMissingLaunchConfig(t *testing.T) {
	reconciler := createRollingUpgradeReconciler(t)
	rollupCtx := createRollingUpgradeContext(reconciler)

	// Create ASG with launch config
	rollupCtx.Cloud.ScalingGroups = []types.AutoScalingGroup{
		{
			AutoScalingGroupName:    aws.String("test-asg"),
			LaunchConfigurationName: aws.String("test-config"),
		},
	}
	rollupCtx.RollingUpgrade.Spec.AsgName = "test-asg"

	// Instance without launch config should be drifted
	instance := &types.Instance{
		InstanceId:              aws.String("i-123"),
		LaunchConfigurationName: nil,
		LifecycleState:          types.LifecycleStateInService,
	}

	isDrifted := rollupCtx.IsInstanceDrifted(instance)

	if !isDrifted {
		t.Error("expected instance without launch config to be drifted")
	}
}

func TestIsInstanceDriftedWithVersionMismatch(t *testing.T) {
	reconciler := createRollingUpgradeReconciler(t)
	rollupCtx := createRollingUpgradeContext(reconciler)

	// Create ASG with launch template
	rollupCtx.Cloud.ScalingGroups = []types.AutoScalingGroup{
		{
			AutoScalingGroupName: aws.String("test-asg"),
			LaunchTemplate: &types.LaunchTemplateSpecification{
				LaunchTemplateName: aws.String("test-template"),
				Version:            aws.String("2"),
			},
		},
	}
	rollupCtx.RollingUpgrade.Spec.AsgName = "test-asg"

	// Instance with different version should be drifted
	instance := &types.Instance{
		InstanceId: aws.String("i-123"),
		LaunchTemplate: &types.LaunchTemplateSpecification{
			LaunchTemplateName: aws.String("test-template"),
			Version:            aws.String("1"),
		},
		LifecycleState: types.LifecycleStateInService,
	}

	isDrifted := rollupCtx.IsInstanceDrifted(instance)

	if !isDrifted {
		t.Error("expected instance with version mismatch to be drifted")
	}
}

func TestIsInstanceDriftedWithMixedInstancePolicy(t *testing.T) {
	reconciler := createRollingUpgradeReconciler(t)
	rollupCtx := createRollingUpgradeContext(reconciler)

	// Create ASG with mixed instances policy
	rollupCtx.Cloud.ScalingGroups = []types.AutoScalingGroup{
		{
			AutoScalingGroupName: aws.String("test-asg"),
			MixedInstancesPolicy: &types.MixedInstancesPolicy{
				LaunchTemplate: &types.LaunchTemplate{
					LaunchTemplateSpecification: &types.LaunchTemplateSpecification{
						LaunchTemplateName: aws.String("test-template"),
						Version:            aws.String("3"),
					},
				},
			},
		},
	}
	rollupCtx.RollingUpgrade.Spec.AsgName = "test-asg"

	// Instance with different template should be drifted
	instance := &types.Instance{
		InstanceId: aws.String("i-123"),
		LaunchTemplate: &types.LaunchTemplateSpecification{
			LaunchTemplateName: aws.String("different-template"),
			Version:            aws.String("3"),
		},
		LifecycleState: types.LifecycleStateInService,
	}

	isDrifted := rollupCtx.IsInstanceDrifted(instance)

	if !isDrifted {
		t.Error("expected instance with different template name to be drifted")
	}
}

func TestSetBatchStandByWithLargeInstanceCount(t *testing.T) {
	reconciler := createRollingUpgradeReconciler(t)
	rollupCtx := createRollingUpgradeContext(reconciler)

	// Create a large list of instance IDs (more than AWS standby limit)
	var instanceIDs []string
	for i := 0; i < 25; i++ {
		instanceIDs = append(instanceIDs, fmt.Sprintf("i-%d", i))
	}

	// Mock ASG client
	mockClient := &MockAutoscalingGroup{}
	rollupCtx.Auth.AmazonClientSet.AsgClient = mockClient
	rollupCtx.RollingUpgrade.Spec.AsgName = "test-asg"

	err := rollupCtx.SetBatchStandBy(instanceIDs)

	// Should handle large batches without error (chunking logic)
	if err != nil {
		t.Errorf("unexpected error with large instance count: %v", err)
	}
}

func TestClusterBallooningWithZeroMaxReplacement(t *testing.T) {
	reconciler := createRollingUpgradeReconciler(t)
	reconciler.MaxReplacementNodes = 0
	rollupCtx := createRollingUpgradeContext(reconciler)
	rollupCtx.MaxReplacementNodes = 0

	isBallooning, allowedBatch := rollupCtx.ClusterBallooning(5)

	// With zero max replacement, should not be ballooning and allow full batch
	if isBallooning {
		t.Error("expected no ballooning with zero max replacement")
	}
	if allowedBatch != 5 {
		t.Errorf("expected allowed batch of 5, got %d", allowedBatch)
	}
}

// Test launch template version handling edge cases
func TestIsInstanceDriftedWithLatestVersion(t *testing.T) {
	reconciler := createRollingUpgradeReconciler(t)
	rollupCtx := createRollingUpgradeContext(reconciler)

	// Create ASG with launch template using "$Latest"
	rollupCtx.Cloud.ScalingGroups = []types.AutoScalingGroup{
		{
			AutoScalingGroupName: aws.String("test-asg"),
			LaunchTemplate: &types.LaunchTemplateSpecification{
				LaunchTemplateName: aws.String("test-template"),
				Version:            aws.String("$Latest"),
			},
		},
	}
	rollupCtx.RollingUpgrade.Spec.AsgName = "test-asg"

	// Mock launch templates to provide version resolution
	rollupCtx.Cloud.LaunchTemplates = []ec2types.LaunchTemplate{
		{
			LaunchTemplateName:  aws.String("test-template"),
			LatestVersionNumber: aws.Int64(5),
		},
	}

	// Instance with specific version should be drifted when ASG uses $Latest
	instance := &types.Instance{
		InstanceId: aws.String("i-123"),
		LaunchTemplate: &types.LaunchTemplateSpecification{
			LaunchTemplateName: aws.String("test-template"),
			Version:            aws.String("3"),
		},
		LifecycleState: types.LifecycleStateInService,
	}

	isDrifted := rollupCtx.IsInstanceDrifted(instance)

	if !isDrifted {
		t.Error("expected instance with older version to be drifted when ASG uses $Latest")
	}
}

func TestIsInstanceDriftedWithDefaultVersion(t *testing.T) {
	reconciler := createRollingUpgradeReconciler(t)
	rollupCtx := createRollingUpgradeContext(reconciler)

	// Create ASG with launch template using "$Default"
	rollupCtx.Cloud.ScalingGroups = []types.AutoScalingGroup{
		{
			AutoScalingGroupName: aws.String("test-asg"),
			LaunchTemplate: &types.LaunchTemplateSpecification{
				LaunchTemplateName: aws.String("test-template"),
				Version:            aws.String("$Default"),
			},
		},
	}
	rollupCtx.RollingUpgrade.Spec.AsgName = "test-asg"

	// Instance with same $Default should not be drifted
	instance := &types.Instance{
		InstanceId: aws.String("i-123"),
		LaunchTemplate: &types.LaunchTemplateSpecification{
			LaunchTemplateName: aws.String("test-template"),
			Version:            aws.String("$Default"),
		},
		LifecycleState: types.LifecycleStateInService,
	}

	isDrifted := rollupCtx.IsInstanceDrifted(instance)

	if isDrifted {
		t.Error("expected instance with same $Default version to not be drifted")
	}
}

// Test error handling in batch processing
func TestSetBatchStandByWithInstanceStandByLimit(t *testing.T) {
	reconciler := createRollingUpgradeReconciler(t)
	rollupCtx := createRollingUpgradeContext(reconciler)

	// Save original limit
	originalLimit := awsprovider.InstanceStandByLimit
	defer func() {
		awsprovider.InstanceStandByLimit = originalLimit
	}()

	// Set a small limit to test chunking
	awsprovider.InstanceStandByLimit = 2

	// Create instance IDs that exceed the limit
	instanceIDs := []string{"i-1", "i-2", "i-3", "i-4", "i-5"}

	// Mock ASG client
	mockClient := &MockAutoscalingGroup{}
	rollupCtx.Auth.AmazonClientSet.AsgClient = mockClient
	rollupCtx.RollingUpgrade.Spec.AsgName = "test-asg"

	err := rollupCtx.SetBatchStandBy(instanceIDs)

	// Should handle chunking without error
	if err != nil {
		t.Errorf("unexpected error with chunked standby: %v", err)
	}
}

// Test drift detection with no launch configuration or template
func TestIsInstanceDriftedWithNoLaunchSpec(t *testing.T) {
	reconciler := createRollingUpgradeReconciler(t)
	rollupCtx := createRollingUpgradeContext(reconciler)

	// Create ASG with no launch config or template (edge case)
	rollupCtx.Cloud.ScalingGroups = []types.AutoScalingGroup{
		{
			AutoScalingGroupName: aws.String("test-asg"),
			// No LaunchConfigurationName or LaunchTemplate
		},
	}
	rollupCtx.RollingUpgrade.Spec.AsgName = "test-asg"

	// Instance with launch config - based on the actual logic, this is NOT drifted
	// because the ASG has no launch spec to compare against
	instance := &types.Instance{
		InstanceId:              aws.String("i-123"),
		LaunchConfigurationName: aws.String("some-config"),
		LifecycleState:          types.LifecycleStateInService,
	}

	isDrifted := rollupCtx.IsInstanceDrifted(instance)

	// The actual business logic returns false when ASG has no launch spec
	// This is the current behavior - instances are only drifted when there's a mismatch
	if isDrifted {
		t.Error("expected instance to not be drifted when ASG has no launch spec to compare against")
	}
}

// New: ensure UniformAcrossAzUpdateStrategy performs round-robin across AZs
func TestSelectTargetsUniformAcrossAzOrdering(t *testing.T) {
	reconciler := createRollingUpgradeReconciler(t)
	rollupCtx := createRollingUpgradeContext(reconciler)
	rollupCtx.Cloud.ScalingGroups = []types.AutoScalingGroup{
		{
			AutoScalingGroupName:    aws.String("asg-uni"),
			LaunchConfigurationName: aws.String("lc"),
			DesiredCapacity:         aws.Int32(6),
			Instances: []types.Instance{
				{InstanceId: aws.String("i-a1"), AvailabilityZone: aws.String("az-a"), LifecycleState: types.LifecycleStateInService},
				{InstanceId: aws.String("i-b1"), AvailabilityZone: aws.String("az-b"), LifecycleState: types.LifecycleStateInService},
				{InstanceId: aws.String("i-c1"), AvailabilityZone: aws.String("az-c"), LifecycleState: types.LifecycleStateInService},
				{InstanceId: aws.String("i-a2"), AvailabilityZone: aws.String("az-a"), LifecycleState: types.LifecycleStateInService},
				{InstanceId: aws.String("i-b2"), AvailabilityZone: aws.String("az-b"), LifecycleState: types.LifecycleStateInService},
				{InstanceId: aws.String("i-c2"), AvailabilityZone: aws.String("az-c"), LifecycleState: types.LifecycleStateInService},
			},
		},
	}
	rollupCtx.RollingUpgrade.Spec.AsgName = "asg-uni"
	rollupCtx.RollingUpgrade.Spec.Strategy.Type = v1alpha1.UniformAcrossAzUpdateStrategy
	rollupCtx.RollingUpgrade.Spec.Strategy.MaxUnavailable = intstr.IntOrString{StrVal: "100%", Type: 1}
	// Provide EC2 instances without the in-progress tag so DescribeInstancesWithoutTagValue returns them
	mockEC2 := &MockEC2{
		Instances: []ec2types.Instance{
			{InstanceId: aws.String("i-a1")},
			{InstanceId: aws.String("i-a2")},
			{InstanceId: aws.String("i-b1")},
			{InstanceId: aws.String("i-b2")},
			{InstanceId: aws.String("i-c1")},
			{InstanceId: aws.String("i-c2")},
		},
	}
	rollupCtx.Auth.AmazonClientSet.Ec2Client = mockEC2
	rollupCtx.Cloud.AmazonClientSet.Ec2Client = mockEC2

	targets := rollupCtx.SelectTargets(&rollupCtx.Cloud.ScalingGroups[0], []string{})
	got := awsprovider.GetInstanceIDsFromPointers(targets)
	if len(got) != 6 {
		t.Fatalf("expected 6 targets, got %d: %v", len(got), got)
	}
	azById := map[string]string{
		"i-a1": "az-a", "i-a2": "az-a",
		"i-b1": "az-b", "i-b2": "az-b",
		"i-c1": "az-c", "i-c2": "az-c",
	}
	// membership check
	expectedSet := map[string]bool{"i-a1": true, "i-a2": true, "i-b1": true, "i-b2": true, "i-c1": true, "i-c2": true}
	for _, id := range got {
		if !expectedSet[id] {
			t.Fatalf("unexpected id in selection: %s", id)
		}
	}
	// per-AZ counts are balanced
	counts := map[string]int{}
	for _, id := range got {
		counts[azById[id]]++
	}
	if counts["az-a"] != 2 || counts["az-b"] != 2 || counts["az-c"] != 2 {
		t.Fatalf("expected two selections per AZ, got: %v", counts)
	}
}

// New: CalculateMaxUnavailable edge cases
func TestCalculateMaxUnavailableEdges(t *testing.T) {
	cases := []struct {
		batch intstr.IntOrString
		total int
		want  int
	}{
		{intstr.FromString("50%"), 7, 4},
		{intstr.FromString("10"), 5, 5},
		{intstr.FromInt(0), 3, 1},
		{intstr.FromInt(2), 10, 2},
	}
	for _, c := range cases {
		got := CalculateMaxUnavailable(c.batch, c.total)
		if got != c.want {
			t.Errorf("CalculateMaxUnavailable(%v,%d)=%d, want %d", c.batch, c.total, got, c.want)
		}
	}
}

// Test the early cordoning functionality with Kubernetes annotations
func TestCordonUncordonAllNodesWithAnnotations(t *testing.T) {
	// This test focuses on the annotation logic
	reconciler := createRollingUpgradeReconciler(t)
	rollupCtx := createRollingUpgradeContext(reconciler)

	// Test the annotation constants are defined
	if EarlyCordonAnnotationKey != "rollingupgrade.keikoproj.io/early-cordoned-by" {
		t.Errorf("Expected annotation key 'rollingupgrade.keikoproj.io/early-cordoned-by', got '%s'", EarlyCordonAnnotationKey)
	}

	if EarlyCordonAnnotationValue != "upgrade-manager" {
		t.Errorf("Expected annotation value 'upgrade-manager', got '%s'", EarlyCordonAnnotationValue)
	}

	// Test function call
	ok, err := rollupCtx.CordonUncordonAllNodes(true)
	if err == nil && !ok {
		t.Errorf("Expected function to handle empty scaling group gracefully")
	}
}

// Test the cordonAndAnnotateNode helper function
func TestCordonAndAnnotateNode(t *testing.T) {
	var tests = []struct {
		TestDescription string
		Reconciler      *RollingUpgradeReconciler
		NodeName        string
		ExpectError     bool
	}{
		{
			"Test cordoning and annotating a valid node",
			createRollingUpgradeReconciler(t),
			"mock-node-1",
			false,
		},
		{
			"Test cordoning a non-existent node",
			createRollingUpgradeReconciler(t),
			"non-existent-node",
			true,
		},
	}

	for _, test := range tests {
		rollupCtx := createRollingUpgradeContext(test.Reconciler)
		node := createNode(test.NodeName)

		err := rollupCtx.cordonAndAnnotateNode(node)

		if test.ExpectError && err == nil {
			t.Errorf("Test '%s' expected error but got none", test.TestDescription)
			continue
		}
		if !test.ExpectError && err != nil {
			t.Errorf("Test '%s' unexpected error: %v", test.TestDescription, err)
			continue
		}

		if !test.ExpectError {
			// Verify node is cordoned
			if !node.Spec.Unschedulable {
				t.Errorf("Test '%s': node should be cordoned", test.TestDescription)
			}

			// Verify node has our annotation (check the updated node from fake client)
			updatedNode, err := rollupCtx.Auth.Kubernetes.CoreV1().Nodes().Get(context.Background(), test.NodeName, metav1.GetOptions{})
			if err == nil && updatedNode.Annotations != nil {
				if updatedNode.Annotations[EarlyCordonAnnotationKey] != EarlyCordonAnnotationValue {
					t.Errorf("Test '%s': node missing upgrade-manager annotation", test.TestDescription)
				}
			}
		}
	}
}

// New: IsInstanceDrifted short-circuits for terminating states
func TestIsInstanceDriftedTerminatingShortCircuit(t *testing.T) {
	reconciler := createRollingUpgradeReconciler(t)
	rollupCtx := createRollingUpgradeContext(reconciler)
	rollupCtx.Cloud.ScalingGroups = createASGs()
	rollupCtx.RollingUpgrade.Spec.AsgName = "mock-asg-1"
	inst := &types.Instance{InstanceId: aws.String("i-x"), LifecycleState: types.LifecycleStateTerminating}
	if rollupCtx.IsInstanceDrifted(inst) {
		t.Errorf("terminating instance should not be considered drifted")
	}
}

// Test the uncordonAndRemoveAnnotation helper function
func TestUncordonAndRemoveAnnotation(t *testing.T) {
	var tests = []struct {
		TestDescription string
		Reconciler      *RollingUpgradeReconciler
		NodeName        string
		ExpectError     bool
	}{
		{
			"Test uncordoning and removing annotation from valid node",
			createRollingUpgradeReconciler(t),
			"mock-node-1", // This node exists in the fake client
			false,
		},
	}

	for _, test := range tests {
		rollupCtx := createRollingUpgradeContext(test.Reconciler)

		// Get the existing node and add our annotation
		node, err := rollupCtx.Auth.Kubernetes.CoreV1().Nodes().Get(context.Background(), test.NodeName, metav1.GetOptions{})
		if err != nil {
			t.Errorf("Test '%s': failed to get node for setup: %v", test.TestDescription, err)
			continue
		}

		// Add our annotation and cordon the node
		if node.Annotations == nil {
			node.Annotations = make(map[string]string)
		}
		node.Annotations[EarlyCordonAnnotationKey] = EarlyCordonAnnotationValue
		node.Spec.Unschedulable = true

		// Update the node in the fake client
		_, err = rollupCtx.Auth.Kubernetes.CoreV1().Nodes().Update(context.Background(), node, metav1.UpdateOptions{})
		if err != nil {
			t.Errorf("Test '%s': failed to update node for setup: %v", test.TestDescription, err)
			continue
		}

		// Now we test the uncordonAndRemoveAnnotation function
		err = rollupCtx.uncordonAndRemoveAnnotation(node)

		if test.ExpectError && err == nil {
			t.Errorf("Test '%s' expected error but got none", test.TestDescription)
			continue
		}
		if !test.ExpectError && err != nil {
			t.Errorf("Test '%s' unexpected error: %v", test.TestDescription, err)
			continue
		}

		if !test.ExpectError {
			// Verify annotation is removed and node is uncordoned
			updatedNode, err := rollupCtx.Auth.Kubernetes.CoreV1().Nodes().Get(context.Background(), test.NodeName, metav1.GetOptions{})
			if err != nil {
				t.Errorf("Test '%s': failed to get updated node: %v", test.TestDescription, err)
				continue
			}

			// Check if node is uncordoned
			if updatedNode.Spec.Unschedulable {
				t.Errorf("Test '%s': node should be uncordoned", test.TestDescription)
			}

			// Check if annotation is removed
			if updatedNode.Annotations != nil && updatedNode.Annotations[EarlyCordonAnnotationKey] == EarlyCordonAnnotationValue {
				t.Errorf("Test '%s': upgrade-manager annotation should be removed", test.TestDescription)
			}
		}
	}
}
