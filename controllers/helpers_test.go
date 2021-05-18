package controllers

import (
	"sync"
	"testing"

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
		RollingUpgrade: rollingUpgrade,
	}

}

func createRollingUpgrade() *v1alpha1.RollingUpgrade {
	return &v1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "0", Namespace: "default"},
		Spec: v1alpha1.RollingUpgradeSpec{
			AsgName:               "mock-asg-1",
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

func createNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "mock-node-1", Namespace: "default"},
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
	errorFlag         bool
	awsErr            awserr.Error
	errorInstanceId   string
	autoScalingGroups []*autoscaling.Group
}

type MockEC2 struct {
	ec2iface.EC2API
	awsErr       awserr.Error
	reservations []*ec2.Reservation
}

func createASGInstance(instanceID string, launchConfigName string) *autoscaling.Instance {
	return &autoscaling.Instance{
		InstanceId:              &instanceID,
		LaunchConfigurationName: &launchConfigName,
		AvailabilityZone:        aws.String("az-1"),
		LifecycleState:          aws.String("InService"),
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

func createASGs() []*autoscaling.Group {
	return []*autoscaling.Group{
		createASG("mock-asg-1", "mock-launch-config-1"),
		createASG("mock-asg-2", "mock-launch-config-2"),
		createASG("mock-asg-3", "mock-launch-config-3"),
	}
}

func createASGClient() *MockAutoscalingGroup {
	return &MockAutoscalingGroup{
		autoScalingGroups: createASGs(),
	}
}

func createEc2Client() MockEC2 {
	return MockEC2{}
}

func createAmazonClient(t *testing.T) *awsprovider.AmazonClientSet {
	return &awsprovider.AmazonClientSet{
		AsgClient: createASGClient(),
		Ec2Client: createEc2Client(),
	}
}
