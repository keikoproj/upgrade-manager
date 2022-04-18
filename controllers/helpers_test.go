package controllers

import (
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/keikoproj/upgrade-manager/api/v1alpha1"
	awsprovider "github.com/keikoproj/upgrade-manager/controllers/providers/aws"
	kubeprovider "github.com/keikoproj/upgrade-manager/controllers/providers/kubernetes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	//AWS
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/autoscaling/autoscalingiface"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
)

// K8s
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
		DrainGroupMapper: &sync.Map{},
		DrainErrorMapper: &sync.Map{},
	}
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
		ObjectMeta: metav1.ObjectMeta{Name: "0", Namespace: "default"},
		Spec: v1alpha1.RollingUpgradeSpec{
			AsgName:               "mock-asg-1",
			PostDrainDelaySeconds: 30,
			Strategy: v1alpha1.UpdateStrategy{
				Type: v1alpha1.RandomUpdateStrategy,
			},
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

// AWS
type MockAutoscalingGroup struct {
	autoscalingiface.AutoScalingAPI
	errorFlag            bool
	awsErr               awserr.Error
	errorInstanceId      string
	autoScalingGroups    []*autoscaling.Group
	Groups               map[string]*autoscaling.Group
	LaunchConfigurations map[string]*autoscaling.LaunchConfiguration
}

type launchTemplateInfo struct {
	name *string
}
type MockEC2 struct {
	ec2iface.EC2API
	LaunchTemplates map[string]*launchTemplateInfo
}

var _ ec2iface.EC2API = &MockEC2{}

func createASGInstance(instanceID string, launchConfigName string) *autoscaling.Instance {
	return &autoscaling.Instance{
		InstanceId:              &instanceID,
		LaunchConfigurationName: &launchConfigName,
		AvailabilityZone:        aws.String("az-1"),
		LifecycleState:          aws.String("InService"),
	}
}

func createASGInstanceWithLaunchTemplate(instanceID string, launchTemplateName string) *autoscaling.Instance {
	return &autoscaling.Instance{
		InstanceId: &instanceID,
		LaunchTemplate: &autoscaling.LaunchTemplateSpecification{
			LaunchTemplateName: &launchTemplateName,
		},
		AvailabilityZone: aws.String("az-1"),
		LifecycleState:   aws.String("InService"),
	}
}

func createEc2Instances() []*ec2.Instance {
	return []*ec2.Instance{
		&ec2.Instance{
			InstanceId: aws.String("mock-instance-1"),
		},
		&ec2.Instance{
			InstanceId: aws.String("mock-instance-2"),
		},
		&ec2.Instance{
			InstanceId: aws.String("mock-instance-3"),
		},
	}
}

func createASG(asgName string, launchConfigName string) *autoscaling.Group {
	return &autoscaling.Group{
		AutoScalingGroupName:    &asgName,
		LaunchConfigurationName: &launchConfigName,
		Instances: []*autoscaling.Instance{
			createASGInstance("mock-instance-1", launchConfigName),
			createASGInstance("mock-instance-2", launchConfigName),
			createASGInstance("mock-instance-3", launchConfigName),
		},
		DesiredCapacity: func(x int) *int64 { i := int64(x); return &i }(3),
	}
}

func createASGWithLaunchTemplate(asgName string, launchTemplate string) *autoscaling.Group {
	return &autoscaling.Group{
		AutoScalingGroupName: &asgName,
		LaunchTemplate: &autoscaling.LaunchTemplateSpecification{
			LaunchTemplateName: &asgName,
		},
		Instances: []*autoscaling.Instance{
			createASGInstance("mock-instance-1", launchTemplate),
			createASGInstance("mock-instance-2", launchTemplate),
			createASGInstance("mock-instance-3", launchTemplate),
		},
		DesiredCapacity: func(x int) *int64 { i := int64(x); return &i }(3),
	}
}

func createASGWithMixedInstanceLaunchTemplate(asgName string, launchTemplate string) *autoscaling.Group {
	return &autoscaling.Group{
		AutoScalingGroupName: &asgName,
		MixedInstancesPolicy: &autoscaling.MixedInstancesPolicy{
			LaunchTemplate: &autoscaling.LaunchTemplate{
				LaunchTemplateSpecification: &autoscaling.LaunchTemplateSpecification{
					LaunchTemplateName: &asgName,
				},
			},
		},
		Instances: []*autoscaling.Instance{
			createASGInstance("mock-instance-1", launchTemplate),
			createASGInstance("mock-instance-2", launchTemplate),
			createASGInstance("mock-instance-3", launchTemplate),
		},
		DesiredCapacity: func(x int) *int64 { i := int64(x); return &i }(3),
	}
}

func createDriftedASG(asgName string, launchConfigName string) *autoscaling.Group {
	return &autoscaling.Group{
		AutoScalingGroupName:    &asgName,
		LaunchConfigurationName: &launchConfigName,
		Instances: []*autoscaling.Instance{
			createASGInstance("mock-instance-1", "different-launch-config"),
			createASGInstance("mock-instance-2", "different-launch-config"),
			createASGInstance("mock-instance-3", "different-launch-config"),
		},
		DesiredCapacity: func(x int) *int64 { i := int64(x); return &i }(3),
	}
}

func createASGs() []*autoscaling.Group {
	return []*autoscaling.Group{
		createASG("mock-asg-1", "mock-launch-config-1"),
		createDriftedASG("mock-asg-2", "mock-launch-config-2"),
		createASG("mock-asg-3", "mock-launch-config-3"),
		createASGWithLaunchTemplate("mock-asg-4", "mock-launch-template-4"),
		createASGWithMixedInstanceLaunchTemplate("mock-asg-5", "mock-launch-template-5"),
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
	return &awsprovider.AmazonClientSet{
		AsgClient: createASGClient(),
		Ec2Client: createEc2Client(),
	}
}

/******************************* AWS MOCKS *******************************/

func (mockAutoscalingGroup MockAutoscalingGroup) TerminateInstanceInAutoScalingGroup(input *autoscaling.TerminateInstanceInAutoScalingGroupInput) (*autoscaling.TerminateInstanceInAutoScalingGroupOutput, error) {
	output := &autoscaling.TerminateInstanceInAutoScalingGroupOutput{}
	if mockAutoscalingGroup.errorFlag {
		if mockAutoscalingGroup.awsErr != nil {
			if len(mockAutoscalingGroup.errorInstanceId) <= 0 ||
				mockAutoscalingGroup.errorInstanceId == *input.InstanceId {
				return output, mockAutoscalingGroup.awsErr
			}
		}
	}
	asgChange := autoscaling.Activity{ActivityId: aws.String("xxx"), AutoScalingGroupName: aws.String("sss"), Cause: aws.String("xxx"), StartTime: aws.Time(time.Now()), StatusCode: aws.String("200"), StatusMessage: aws.String("success")}
	output.Activity = &asgChange
	return output, nil
}

// DescribeLaunchTemplatesPages mocks the describing the launch templates
func (m *MockEC2) DescribeLaunchTemplatesPages(request *ec2.DescribeLaunchTemplatesInput, callback func(*ec2.DescribeLaunchTemplatesOutput, bool) bool) error {
	page, err := m.DescribeLaunchTemplates(request)
	if err != nil {
		return err
	}

	callback(page, false)

	return nil
}

// DescribeLaunchTemplates mocks the describing the launch templates
func (m *MockEC2) DescribeLaunchTemplates(request *ec2.DescribeLaunchTemplatesInput) (*ec2.DescribeLaunchTemplatesOutput, error) {

	o := &ec2.DescribeLaunchTemplatesOutput{}

	if m.LaunchTemplates == nil {
		return o, nil
	}

	for id, ltInfo := range m.LaunchTemplates {
		launchTemplatetName := aws.StringValue(ltInfo.name)

		allFiltersMatch := true
		for _, filter := range request.Filters {
			filterName := aws.StringValue(filter.Name)
			filterValue := aws.StringValue(filter.Values[0])

			filterMatches := false
			if filterName == "tag:Name" && filterValue == launchTemplatetName {
				filterMatches = true
			}
			if strings.HasPrefix(filterName, "tag:kubernetes.io/cluster/") {
				filterMatches = true
			}

			if !filterMatches {
				allFiltersMatch = false
				break
			}
		}

		if allFiltersMatch {
			o.LaunchTemplates = append(o.LaunchTemplates, &ec2.LaunchTemplate{
				LaunchTemplateName: aws.String(launchTemplatetName),
				LaunchTemplateId:   aws.String(id),
			})
		}
	}

	return o, nil
}

func (m *MockAutoscalingGroup) DescribeAutoScalingGroupsPages(request *autoscaling.DescribeAutoScalingGroupsInput, callback func(*autoscaling.DescribeAutoScalingGroupsOutput, bool) bool) error {
	// For the mock, we just send everything in one page
	page, err := m.DescribeAutoScalingGroups(request)
	if err != nil {
		return err
	}

	callback(page, false)

	return nil
}

func (m *MockAutoscalingGroup) DescribeAutoScalingGroups(input *autoscaling.DescribeAutoScalingGroupsInput) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	return &autoscaling.DescribeAutoScalingGroupsOutput{
		AutoScalingGroups: createASGs(),
	}, nil
}

func (m *MockEC2) DescribeInstancesPages(request *ec2.DescribeInstancesInput, callback func(*ec2.DescribeInstancesOutput, bool) bool) error {
	// For the mock, we just send everything in one page
	page, err := m.DescribeInstances(request)
	if err != nil {
		return err
	}

	callback(page, false)

	return nil
}

func (m *MockEC2) DescribeInstances(*ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
	return &ec2.DescribeInstancesOutput{
		Reservations: []*ec2.Reservation{
			&ec2.Reservation{Instances: createEc2Instances()},
		},
	}, nil
}

func (mockAutoscalingGroup MockAutoscalingGroup) EnterStandby(_ *autoscaling.EnterStandbyInput) (*autoscaling.EnterStandbyOutput, error) {
	output := &autoscaling.EnterStandbyOutput{}
	return output, nil
}
