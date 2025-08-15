package controllers

import (
	"sync"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubectl/pkg/scheme"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/keikoproj/upgrade-manager/api/v1alpha1"
	awsprovider "github.com/keikoproj/upgrade-manager/controllers/providers/aws"
	kubeprovider "github.com/keikoproj/upgrade-manager/controllers/providers/kubernetes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	controllerRuntimeClientFake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	//AWS
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// K8s
func createRollingUpgradeReconciler(t *testing.T, objects ...runtime.Object) *RollingUpgradeReconciler {
	// add v1alpha1 scheme
	s := scheme.Scheme
	err := v1alpha1.AddToScheme(s)
	if err != nil {
		t.Errorf("Test Description: %s \n error: %v", "failed to add v1alpha1 scheme", err)
	}

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
		Client:      controllerRuntimeClientFake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objects...).Build(),
		Scheme:      s,
		Logger:      logger,
		Auth:        auth,
		EventWriter: kubeprovider.NewEventWriter(kubeClient, logger),
		ScriptRunner: ScriptRunner{
			Logger: logger,
		},
		DrainGroupMapper:    &sync.Map{},
		DrainErrorMapper:    &sync.Map{},
		ReplacementNodesMap: &sync.Map{},
		ReconcileMap:        &sync.Map{},
		AdmissionMap:        sync.Map{},

		ClusterNodesMap: &sync.Map{},
	}

	// set up fake admission map
	reconciler.AdmissionMap.Store("default/2", "mock-asg-2")
	reconciler.AdmissionMap.Store("default/3", "mock-asg-3")
	reconciler.AdmissionMap.Store("default/4", "mock-asg-4")
	reconciler.AdmissionMap.Store("default/5", "mock-asg-5")

	return reconciler

}

func createRollingUpgradeContext(r *RollingUpgradeReconciler) *RollingUpgradeContext {
	rollingUpgrade := createRollingUpgrade()
	drainGroup, _ := r.DrainGroupMapper.LoadOrStore(rollingUpgrade.NamespacedName(), &sync.WaitGroup{})
	drainErrs, _ := r.DrainErrorMapper.LoadOrStore(rollingUpgrade.NamespacedName(), make(chan error))

	return &RollingUpgradeContext{
		Logger:       r.Logger,
		Auth:         r.Auth,
		ScriptRunner: r.ScriptRunner,
		Cloud:        NewDiscoveredState(r.Auth, r.Logger),
		DrainManager: &DrainManager{
			DrainErrors: drainErrs.(chan error),
			DrainGroup:  drainGroup.(*sync.WaitGroup),
		},
		RollingUpgrade:      rollingUpgrade,
		metricsMutex:        &sync.Mutex{},
		ReplacementNodesMap: r.ReplacementNodesMap,
		MaxReplacementNodes: r.MaxReplacementNodes,
	}

}

func createRollingUpgrade() *v1alpha1.RollingUpgrade {
	return &v1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-upgrade",
			Namespace: "default",
		},
		Spec: v1alpha1.RollingUpgradeSpec{
			AsgName:  "mock-asg-1",
			Strategy: v1alpha1.UpdateStrategy{},
		},
		Status: v1alpha1.RollingUpgradeStatus{
			CurrentStatus: v1alpha1.StatusInit,
		},
	}
}

func createNodeList() *corev1.NodeList {
	return &corev1.NodeList{
		Items: []corev1.Node{
			corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "mock-node-1"},
				Spec:       corev1.NodeSpec{ProviderID: "foo-bar/mock-instance-1"},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
					},
				},
			},
			corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "mock-node-2"},
				Spec:       corev1.NodeSpec{ProviderID: "foo-bar/mock-instance-2"},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
					},
				},
			},
			corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "mock-node-3"},
				Spec:       corev1.NodeSpec{ProviderID: "foo-bar/mock-instance-3"},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
					},
				},
			},
		},
	}
}

func createNodeSlice() []*corev1.Node {
	return []*corev1.Node{
		createNode("mock-node-1"),
		createNode("mock-node-2"),
		createNode("mock-node-3"),
	}
}

func createNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       corev1.NodeSpec{ProviderID: "foo-bar/mock-instance-1"},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

// AWS SDK Go v2 Mock implementations following the official testing guide
// https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/unit-testing.html

type MockAutoscalingGroup struct {
	autoScalingGroups []types.AutoScalingGroup
	errorFlag         bool
	errorInstanceId   string
}

type MockEC2 struct {
	LaunchTemplates []ec2types.LaunchTemplate
	Instances       []ec2types.Instance
}

func createASGInstance(instanceID string, launchConfigName string, az string) *types.Instance {
	return &types.Instance{
		InstanceId:              aws.String(instanceID),
		LaunchConfigurationName: aws.String(launchConfigName),
		AvailabilityZone:        aws.String(az),
		LifecycleState:          types.LifecycleStateInService,
	}
}

func createDriftedASGInstance(instanceID string, az string) *types.Instance {
	return &types.Instance{
		InstanceId:       aws.String(instanceID),
		AvailabilityZone: aws.String(az),
		LifecycleState:   types.LifecycleStateInService,
	}
}

func createASGInstanceWithLaunchTemplate(instanceID string, launchTemplateName string) *types.Instance {
	return &types.Instance{
		InstanceId: aws.String(instanceID),
		LaunchTemplate: &types.LaunchTemplateSpecification{
			LaunchTemplateName: aws.String(launchTemplateName),
		},
		AvailabilityZone: aws.String("az-1"),
		LifecycleState:   types.LifecycleStateInService,
	}
}

func createEc2Instances() []ec2types.Instance {
	return []ec2types.Instance{
		{
			InstanceId: aws.String("mock-instance-1"),
		},
		{
			InstanceId: aws.String("mock-instance-2"),
		},
		{
			InstanceId: aws.String("mock-instance-3"),
		},
	}
}

func createASG(asgName string, launchConfigName string) *types.AutoScalingGroup {
	return &types.AutoScalingGroup{
		AutoScalingGroupName:    aws.String(asgName),
		LaunchConfigurationName: aws.String(launchConfigName),
		Instances: []types.Instance{
			*createASGInstance("mock-instance-1", launchConfigName, "az-1"),
			*createASGInstance("mock-instance-2", launchConfigName, "az-2"),
			*createASGInstance("mock-instance-3", launchConfigName, "az-3"),
		},
		DesiredCapacity: func(x int) *int32 { i := int32(x); return &i }(3),
	}
}

func createASGWithLaunchTemplate(asgName string, launchTemplate string) *types.AutoScalingGroup {
	return &types.AutoScalingGroup{
		AutoScalingGroupName: aws.String(asgName),
		LaunchTemplate: &types.LaunchTemplateSpecification{
			LaunchTemplateName: aws.String(asgName),
		},
		Instances: []types.Instance{
			*createASGInstance("mock-instance-1", launchTemplate, "az-1"),
			*createASGInstance("mock-instance-2", launchTemplate, "az-2"),
			*createASGInstance("mock-instance-3", launchTemplate, "az-3"),
		},
		DesiredCapacity: func(x int) *int32 { i := int32(x); return &i }(3),
	}
}

func createASGWithMixedInstanceLaunchTemplate(asgName string, launchTemplate string) *types.AutoScalingGroup {
	return &types.AutoScalingGroup{
		AutoScalingGroupName: aws.String(asgName),
		MixedInstancesPolicy: &types.MixedInstancesPolicy{
			LaunchTemplate: &types.LaunchTemplate{
				LaunchTemplateSpecification: &types.LaunchTemplateSpecification{
					LaunchTemplateName: aws.String(asgName),
				},
			},
		},
		Instances: []types.Instance{
			*createASGInstance("mock-instance-1", launchTemplate, "az-1"),
			*createASGInstance("mock-instance-2", launchTemplate, "az-2"),
			*createASGInstance("mock-instance-3", launchTemplate, "az-3"),
		},
		DesiredCapacity: func(x int) *int32 { i := int32(x); return &i }(3),
	}
}

func createDriftedASG(asgName string, launchConfigName string) *types.AutoScalingGroup {
	return &types.AutoScalingGroup{
		AutoScalingGroupName:    aws.String(asgName),
		LaunchConfigurationName: aws.String(launchConfigName),
		Instances: []types.Instance{
			*createDriftedASGInstance("mock-instance-1", "az-1"),
			*createDriftedASGInstance("mock-instance-2", "az-2"),
			*createDriftedASGInstance("mock-instance-3", "az-3"),
		},
		DesiredCapacity: func(x int) *int32 { i := int32(x); return &i }(3),
	}
}

func createASGs() []types.AutoScalingGroup {
	return []types.AutoScalingGroup{
		*createASG("mock-asg-1", "mock-launch-config-1"),
		*createDriftedASG("mock-asg-2", "mock-launch-config-2"),
		*createASG("mock-asg-3", "mock-launch-config-3"),
		*createASGWithLaunchTemplate("mock-asg-4", "mock-launch-template-4"),
		*createASGWithMixedInstanceLaunchTemplate("mock-asg-5", "mock-launch-template-5"),
	}
}

func createASGClient() *MockAutoscalingGroup {
	return &MockAutoscalingGroup{
		autoScalingGroups: createASGs(),
	}
}

func createEc2Client() *MockEC2 {
	return &MockEC2{}
}

func createAmazonClient(t *testing.T) *awsprovider.AmazonClientSet {
	// Create mock clients that implement our interfaces
	return &awsprovider.AmazonClientSet{
		AsgClient: &MockAutoscalingGroup{
			autoScalingGroups: createASGs(),
		},
		Ec2Client: &MockEC2{
			LaunchTemplates: []ec2types.LaunchTemplate{},
			Instances:       createEc2Instances(),
		},
	}
}

/******************************* AWS MOCKS *******************************/
// AWS SDK Go v2 Mock implementations following the official testing guide

// MockAutoscalingGroup implements awsprovider.AutoScalingAPI interface
func (m *MockAutoscalingGroup) DescribeAutoScalingGroups(ctx context.Context, params *autoscaling.DescribeAutoScalingGroupsInput, optFns ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	return &autoscaling.DescribeAutoScalingGroupsOutput{
		AutoScalingGroups: m.autoScalingGroups,
	}, nil
}

func (m *MockAutoscalingGroup) TerminateInstanceInAutoScalingGroup(ctx context.Context, params *autoscaling.TerminateInstanceInAutoScalingGroupInput, optFns ...func(*autoscaling.Options)) (*autoscaling.TerminateInstanceInAutoScalingGroupOutput, error) {
	if m.errorFlag && (len(m.errorInstanceId) == 0 || m.errorInstanceId == aws.ToString(params.InstanceId)) {
		return nil, fmt.Errorf("mock error for instance %s", aws.ToString(params.InstanceId))
	}

	return &autoscaling.TerminateInstanceInAutoScalingGroupOutput{
		Activity: &types.Activity{
			ActivityId:           aws.String("mock-activity-id"),
			AutoScalingGroupName: aws.String("mock-asg"),
			StatusCode:           types.ScalingActivityStatusCodeSuccessful,
		},
	}, nil
}

func (m *MockAutoscalingGroup) EnterStandby(ctx context.Context, params *autoscaling.EnterStandbyInput, optFns ...func(*autoscaling.Options)) (*autoscaling.EnterStandbyOutput, error) {
	return &autoscaling.EnterStandbyOutput{}, nil
}

// MockEC2 implements awsprovider.EC2API interface
func (m *MockEC2) DescribeLaunchTemplates(ctx context.Context, params *ec2.DescribeLaunchTemplatesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplatesOutput, error) {
	return &ec2.DescribeLaunchTemplatesOutput{
		LaunchTemplates: m.LaunchTemplates,
	}, nil
}

func (m *MockEC2) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	reservations := []ec2types.Reservation{
		{
			Instances: m.Instances,
		},
	}
	return &ec2.DescribeInstancesOutput{
		Reservations: reservations,
	}, nil
}

func (m *MockEC2) CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	return &ec2.CreateTagsOutput{}, nil
}

func (m *MockEC2) DeleteTags(ctx context.Context, params *ec2.DeleteTagsInput, optFns ...func(*ec2.Options)) (*ec2.DeleteTagsOutput, error) {
	return &ec2.DeleteTagsOutput{}, nil
}
