package controllers

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/scheme"
	log2 "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/keikoproj/aws-sdk-go-cache/cache"
	"github.com/keikoproj/upgrade-manager/pkg/log"

	"k8s.io/apimachinery/pkg/util/intstr"

	"gopkg.in/yaml.v2"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/autoscaling/autoscalingiface"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/pkg/errors"

	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/onsi/gomega"
	"golang.org/x/net/context"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	upgrademgrv1alpha1 "github.com/keikoproj/upgrade-manager/api/v1alpha1"
)

func TestMain(m *testing.M) {
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "config", "crd", "bases")},
	}

	cfg, _ = testEnv.Start()
	os.Exit(m.Run())
}

func TestErrorStatusMarkJanitor(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	someAsg := "someAsg"
	instance := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec:       upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg},
	}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())
	rcRollingUpgrade := &RollingUpgradeReconciler{Client: mgr.GetClient(),
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		Log:             log2.NullLogger{},
		ClusterState:    NewClusterState(),
		ScriptRunner:    NewScriptRunner(log2.NullLogger{}),
	}

	ctx := context.TODO()
	rcRollingUpgrade.inProcessASGs.Store(someAsg, "processing")
	rcRollingUpgrade.finishExecution(upgrademgrv1alpha1.StatusError, 3, &ctx, instance)
	g.Expect(instance.ObjectMeta.Annotations[JanitorAnnotation]).To(gomega.Equal(ClearErrorFrequency))
	_, exists := rcRollingUpgrade.inProcessASGs.Load(someAsg)
	g.Expect(exists).To(gomega.BeFalse())
}

func TestMarkObjForCleanupCompleted(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Status.CurrentStatus = upgrademgrv1alpha1.StatusComplete

	g.Expect(ruObj.ObjectMeta.Annotations).To(gomega.BeNil())
	MarkObjForCleanup(ruObj)
	g.Expect(ruObj.ObjectMeta.Annotations[JanitorAnnotation]).To(gomega.Equal(ClearCompletedFrequency))
}

func TestMarkObjForCleanupError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Status.CurrentStatus = upgrademgrv1alpha1.StatusError

	g.Expect(ruObj.ObjectMeta.Annotations).To(gomega.BeNil())
	MarkObjForCleanup(ruObj)
	g.Expect(ruObj.ObjectMeta.Annotations[JanitorAnnotation]).To(gomega.Equal(ClearErrorFrequency))
}

func TestMarkObjForCleanupNothingHappens(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Status.CurrentStatus = "some other status"

	g.Expect(ruObj.ObjectMeta.Annotations).To(gomega.BeNil())
	MarkObjForCleanup(ruObj)
	g.Expect(ruObj.ObjectMeta.Annotations).To(gomega.BeEmpty())
}

func TestPreDrainScriptSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	instance := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	instance.Spec.PreDrain.Script = "echo 'Predrain script ran without error'"

	rcRollingUpgrade := createReconciler()
	err := rcRollingUpgrade.preDrainHelper("test-instance-id", "test", instance)
	g.Expect(err).To(gomega.BeNil())
}

func TestPreDrainScriptError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	instance := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	instance.Spec.PreDrain.Script = "exit 1"

	rcRollingUpgrade := createReconciler()
	err := rcRollingUpgrade.preDrainHelper("test-instance-id", "test", instance)
	g.Expect(err.Error()).To(gomega.HavePrefix("Failed to run preDrain script"))
}

func TestPostDrainHelperPostDrainScriptSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostDrain.Script = "echo Hello, postDrainScript!"

	rcRollingUpgrade := createReconciler()
	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall
	err := rcRollingUpgrade.postDrainHelper("test-instance-id", mockNode, ruObj)

	g.Expect(err).To(gomega.BeNil())
}

func TestPostDrainHelperPostDrainScriptError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "k() { echo $@ >> cmdlog.txt ; }; k"
	os.Remove("cmdlog.txt")

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostDrain.Script = "exit 1"

	rcRollingUpgrade := createReconciler()
	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall
	err := rcRollingUpgrade.postDrainHelper("test-instance-id", mockNode, ruObj)

	g.Expect(err).To(gomega.Not(gomega.BeNil()))

	// assert node was uncordoned
	cmdlog, _ := ioutil.ReadFile("cmdlog.txt")
	g.Expect(string(cmdlog)).To(gomega.Equal(fmt.Sprintf("uncordon %s\n", mockNode)))
	os.Remove("cmdlog.txt")
}

func TestPostDrainHelperPostDrainScriptErrorWithIgnoreDrainFailures(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "k() { echo $@ >> cmdlog.txt ; }; k"
	os.Remove("cmdlog.txt")

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec:       upgrademgrv1alpha1.RollingUpgradeSpec{IgnoreDrainFailures: true}}
	ruObj.Spec.PostDrain.Script = "exit 1"

	rcRollingUpgrade := createReconciler()
	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall
	err := rcRollingUpgrade.postDrainHelper("test-instance-id", mockNode, ruObj)

	g.Expect(err).To(gomega.Not(gomega.BeNil()))

	// assert node was not uncordoned
	cmdlog, _ := ioutil.ReadFile("cmdlog.txt")
	g.Expect(string(cmdlog)).To(gomega.Equal(""))
	os.Remove("cmdlog.txt")
}

func TestPostDrainHelperPostDrainWaitScriptSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostDrain.PostWaitScript = "echo Hello, postDrainWaitScript!"
	ruObj.Spec.PostDrainDelaySeconds = 0

	rcRollingUpgrade := createReconciler()
	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall
	err := rcRollingUpgrade.postDrainHelper("test-instance-id", mockNode, ruObj)

	g.Expect(err).To(gomega.BeNil())
}

func TestPostDrainHelperPostDrainWaitScriptError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "k() { echo $@ >> cmdlog.txt ; }; k"
	os.Remove("cmdlog.txt")

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostDrain.PostWaitScript = "exit 1"
	ruObj.Spec.PostDrainDelaySeconds = 0

	rcRollingUpgrade := createReconciler()
	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall
	err := rcRollingUpgrade.postDrainHelper("test-instance-id", mockNode, ruObj)

	g.Expect(err).To(gomega.Not(gomega.BeNil()))

	// assert node was uncordoned
	cmdlog, _ := ioutil.ReadFile("cmdlog.txt")
	g.Expect(string(cmdlog)).To(gomega.Equal(fmt.Sprintf("uncordon %s\n", mockNode)))
	os.Remove("cmdlog.txt")
}

func TestPostDrainHelperPostDrainWaitScriptErrorWithIgnoreDrainFailures(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "k() { echo $@ >> cmdlog.txt ; }; k"
	os.Remove("cmdlog.txt")

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec:       upgrademgrv1alpha1.RollingUpgradeSpec{IgnoreDrainFailures: true}}
	ruObj.Spec.PostDrain.PostWaitScript = "exit 1"
	ruObj.Spec.PostDrainDelaySeconds = 0

	rcRollingUpgrade := createReconciler()
	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall
	err := rcRollingUpgrade.postDrainHelper("test-instance-id", mockNode, ruObj)

	g.Expect(err).To(gomega.Not(gomega.BeNil()))

	// assert node was not uncordoned
	cmdlog, _ := ioutil.ReadFile("cmdlog.txt")
	g.Expect(string(cmdlog)).To(gomega.Equal(""))
	os.Remove("cmdlog.txt")
}

func TestDrainNodeSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	rcRollingUpgrade := createReconciler()
	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall

	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, "test-id", ruObj.Spec.Strategy.DrainTimeout)
	g.Expect(err).To(gomega.BeNil())
}

func TestDrainNodePreDrainError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PreDrain.Script = "exit 1"
	rcRollingUpgrade := createReconciler()
	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall

	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, "test-id", ruObj.Spec.Strategy.DrainTimeout)
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
}

func TestDrainNodePostDrainScriptError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostDrain.Script = "exit 1"
	rcRollingUpgrade := createReconciler()
	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall

	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, "test-id", ruObj.Spec.Strategy.DrainTimeout)
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
}

func TestDrainNodePostDrainWaitScriptError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostDrain.PostWaitScript = "exit 1"
	rcRollingUpgrade := createReconciler()
	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall

	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, "test-id", ruObj.Spec.Strategy.DrainTimeout)
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
}

func TestDrainNodePostDrainFailureToDrainNotFound(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"

	// Force quit from the rest of the command
	mockKubeCtlCall := "echo 'Error from server (NotFound)'; exit 1;"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	rcRollingUpgrade := createReconciler()
	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall

	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, "test-id", ruObj.Spec.Strategy.DrainTimeout)
	g.Expect(err).To(gomega.BeNil())
}

func TestDrainNodePostDrainFailureToDrain(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"

	// Force quit from the rest of the command
	mockKubeCtlCall := "exit 1;"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			Strategy: upgrademgrv1alpha1.UpdateStrategy{DrainTimeout: -1},
		},
	}
	rcRollingUpgrade := createReconciler()
	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall

	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, "test-id", ruObj.Spec.Strategy.DrainTimeout)
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
}

func createReconciler() *RollingUpgradeReconciler {
	return &RollingUpgradeReconciler{
		ClusterState: NewClusterState(),
		Log:          log2.NullLogger{},
		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
	}
}

type MockEC2 struct {
	ec2iface.EC2API
	awsErr       awserr.Error
	reservations []*ec2.Reservation
}

type MockAutoscalingGroup struct {
	autoscalingiface.AutoScalingAPI
	errorFlag         bool
	awsErr            awserr.Error
	errorInstanceId   string
	autoScalingGroups []*autoscaling.Group
}

func (m MockEC2) CreateTags(_ *ec2.CreateTagsInput) (*ec2.CreateTagsOutput, error) {
	if m.awsErr != nil {
		return nil, m.awsErr
	}
	return &ec2.CreateTagsOutput{}, nil
}

func (m MockEC2) DescribeInstances(_ *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
	return &ec2.DescribeInstancesOutput{Reservations: m.reservations}, nil
}

func (m MockEC2) DescribeInstancesPages(input *ec2.DescribeInstancesInput, callback func(*ec2.DescribeInstancesOutput, bool) bool) error {
	page, err := m.DescribeInstances(input)
	if err != nil {
		return err
	}
	callback(page, false)
	return nil
}

func (mockAutoscalingGroup MockAutoscalingGroup) EnterStandby(_ *autoscaling.EnterStandbyInput) (*autoscaling.EnterStandbyOutput, error) {
	output := &autoscaling.EnterStandbyOutput{}
	return output, nil
}

func (mockAutoscalingGroup MockAutoscalingGroup) DescribeAutoScalingGroups(input *autoscaling.DescribeAutoScalingGroupsInput) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	var err error
	output := autoscaling.DescribeAutoScalingGroupsOutput{
		AutoScalingGroups: []*autoscaling.Group{},
	}
	//To support parallel ASG tracking.
	asgA, asgB := "asg-a", "asg-b"

	if mockAutoscalingGroup.errorFlag {
		err = mockAutoscalingGroup.awsErr
	}
	switch *input.AutoScalingGroupNames[0] {
	case asgA:
		output.AutoScalingGroups = []*autoscaling.Group{
			{AutoScalingGroupName: &asgA},
		}
	case asgB:
		output.AutoScalingGroups = []*autoscaling.Group{
			{AutoScalingGroupName: &asgB},
		}
	default:
		output.AutoScalingGroups = mockAutoscalingGroup.autoScalingGroups
	}
	return &output, err
}

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

func TestGetInProgressInstances(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockInstances := []*autoscaling.Instance{
		{
			InstanceId: aws.String("i-0123foo"),
		},
		{
			InstanceId: aws.String("i-0123bar"),
		},
	}
	expectedInstance := &autoscaling.Instance{
		InstanceId: aws.String("i-0123foo"),
	}
	mockReservations := []*ec2.Reservation{
		{
			Instances: []*ec2.Instance{
				{
					InstanceId: aws.String("i-0123foo"),
				},
			},
		},
	}
	reconciler := &RollingUpgradeReconciler{
		ClusterState: NewClusterState(),
		EC2Client:    MockEC2{reservations: mockReservations},
		ASGClient: MockAutoscalingGroup{
			errorFlag: false,
			awsErr:    nil,
		},
		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
	}
	inProgressInstances, err := reconciler.getInProgressInstances(mockInstances)
	g.Expect(err).To(gomega.BeNil())
	g.Expect(inProgressInstances).To(gomega.ContainElement(expectedInstance))
	g.Expect(inProgressInstances).To(gomega.HaveLen(1))
}

func TestTerminateNodeSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		ClusterState: NewClusterState(),
		Log:          log2.NullLogger{},
		EC2Client:    MockEC2{},
		ASGClient: MockAutoscalingGroup{
			errorFlag: false,
			awsErr:    nil,
		},
		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
	}

	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, "")
	g.Expect(err).To(gomega.BeNil())
}

func TestTerminateNodeErrorNotFound(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: true, awsErr: awserr.New("InvalidInstanceID.NotFound",
		"ValidationError: Instance Id not found - No managed instance found for instance ID i-0bba",
		nil)}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		ClusterState: NewClusterState(),
		Log:          log2.NullLogger{},
		ASGClient:    mockAutoscalingGroup,
		EC2Client:    MockEC2{},
		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
	}

	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, "")
	g.Expect(err).To(gomega.BeNil())
}

func init() {
	WaiterMaxDelay = time.Second * 2
	WaiterMinDelay = time.Second * 1
	WaiterMaxAttempts = uint32(2)
}

func TestTerminateNodeErrorScalingActivityInProgressWithRetry(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: true, awsErr: awserr.New(autoscaling.ErrCodeScalingActivityInProgressFault,
		"Scaling activities in progress",
		nil)}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		ClusterState: NewClusterState(),
		Log:          log2.NullLogger{},
		ASGClient:    mockAutoscalingGroup,
		EC2Client:    MockEC2{},
		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
	}
	go func() {
		time.Sleep(WaiterMaxDelay)
		rcRollingUpgrade.ASGClient = MockAutoscalingGroup{
			errorFlag: false,
			awsErr:    nil,
		}
	}()
	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, "")
	g.Expect(err).To(gomega.BeNil())
}

func TestTerminateNodeErrorScalingActivityInProgress(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: true, awsErr: awserr.New(autoscaling.ErrCodeScalingActivityInProgressFault,
		"Scaling activities in progress",
		nil)}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		ClusterState: NewClusterState(),
		Log:          log2.NullLogger{},
		ASGClient:    mockAutoscalingGroup,
		EC2Client:    MockEC2{},
		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
	}
	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, "")
	g.Expect(err.Error()).To(gomega.ContainSubstring("no more retries left"))
}

func TestTerminateNodeErrorResourceContention(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: true, awsErr: awserr.New(autoscaling.ErrCodeResourceContentionFault,
		"Have a pending update on resource",
		nil)}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		ClusterState: NewClusterState(),
		Log:          log2.NullLogger{},
		ASGClient:    mockAutoscalingGroup,
		EC2Client:    MockEC2{},
		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
	}

	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, "")
	g.Expect(err.Error()).To(gomega.ContainSubstring("no more retries left"))
}

func TestTerminateNodeErrorOtherError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: true, awsErr: awserr.New("some-other-aws-error",
		"some message",
		errors.New("some error"))}

	rcRollingUpgrade := &RollingUpgradeReconciler{
		ClusterState: NewClusterState(),
		Log:          log2.NullLogger{},
		ASGClient:    mockAutoscalingGroup,
		EC2Client:    MockEC2{},
		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
	}
	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, "")
	g.Expect(err.Error()).To(gomega.ContainSubstring("some error"))
}

func TestTerminateNodePostTerminateScriptSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostTerminate.Script = "echo hello!"
	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: false, awsErr: nil}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		ClusterState: NewClusterState(),
		Log:          log2.NullLogger{},
		ASGClient:    mockAutoscalingGroup,
		EC2Client:    MockEC2{},
		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
	}
	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, "")
	g.Expect(err).To(gomega.BeNil())
}

func TestTerminateNodePostTerminateScriptErrorNotFoundFromServer(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostTerminate.Script = "echo 'Error from server (NotFound)'; exit 1"
	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: false, awsErr: nil}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		ClusterState: NewClusterState(),
		Log:          log2.NullLogger{},
		ASGClient:    mockAutoscalingGroup,
		EC2Client:    MockEC2{},
		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
	}
	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, "")
	g.Expect(err).To(gomega.BeNil())
}

func TestTerminateNodePostTerminateScriptErrorOtherError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostTerminate.Script = "exit 1"
	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: false, awsErr: nil}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		ClusterState: NewClusterState(),
		Log:          log2.NullLogger{},
		ASGClient:    mockAutoscalingGroup,
		EC2Client:    MockEC2{},
		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
	}
	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, "")
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
	g.Expect(err.Error()).To(gomega.HavePrefix("Failed to run postTerminate script: "))
}

func TestLoadEnvironmentVariables(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	r := &ScriptRunner{}

	mockID := "fake-id-foo"
	mockName := "instance-name-foo"

	env := r.buildEnv(&upgrademgrv1alpha1.RollingUpgrade{
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName: "asg-foo",
		},
	}, mockID, mockName)
	g.Expect(env).To(gomega.HaveLen(3))

}

func TestGetNodeNameFoundNode(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockInstanceID := "123456"
	autoscalingInstance := autoscaling.Instance{InstanceId: &mockInstanceID}

	fooNode1 := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "fooNode1"},
		Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	fooNode2 := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "fooNode2"},
		Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}
	correctNode := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "correctNode"},
		Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockInstanceID}}

	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2, correctNode}}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	rcRollingUpgrade := createReconciler()
	name := rcRollingUpgrade.getNodeName(&autoscalingInstance, &nodeList, ruObj)

	g.Expect(name).To(gomega.Equal("correctNode"))
}

func TestGetNodeNameMissingNode(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockInstanceID := "123456"
	autoscalingInstance := autoscaling.Instance{InstanceId: &mockInstanceID}

	fooNode1 := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "fooNode1"},
		Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	fooNode2 := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "fooNode2"},
		Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}

	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2}}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	rcRollingUpgrade := createReconciler()
	name := rcRollingUpgrade.getNodeName(&autoscalingInstance, &nodeList, ruObj)

	g.Expect(name).To(gomega.Equal(""))
}

func TestGetNodeFromAsgFoundNode(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockInstanceID := "123456"
	autoscalingInstance := autoscaling.Instance{InstanceId: &mockInstanceID}

	fooNode1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	fooNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}

	correctNode := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockInstanceID}}

	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2, correctNode}}
	rcRollingUpgrade := createReconciler()
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	node := rcRollingUpgrade.getNodeFromAsg(&autoscalingInstance, &nodeList, ruObj)

	g.Expect(node).To(gomega.Not(gomega.BeNil()))
	g.Expect(node).To(gomega.Equal(&correctNode))
}

func TestGetNodeFromAsgMissingNode(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockInstanceID := "123456"
	autoscalingInstance := autoscaling.Instance{InstanceId: &mockInstanceID}

	fooNode1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	fooNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}

	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2}}
	rcRollingUpgrade := createReconciler()
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	node := rcRollingUpgrade.getNodeFromAsg(&autoscalingInstance, &nodeList, ruObj)

	g.Expect(node).To(gomega.BeNil())
}

func TestPopulateAsgSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	correctAsg := "correct-asg"
	mockAsg := &autoscaling.Group{
		AutoScalingGroupName: &correctAsg,
	}
	mockAsgClient := MockAutoscalingGroup{
		autoScalingGroups: []*autoscaling.Group{mockAsg},
	}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		TypeMeta: metav1.TypeMeta{Kind: "RollingUpgrade", APIVersion: "v1alpha1"},
		Spec:     upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: "correct-asg"}}

	rcRollingUpgrade := &RollingUpgradeReconciler{
		Log:          log2.NullLogger{},
		ClusterState: NewClusterState(),
		ASGClient:    mockAsgClient,
		EC2Client:    MockEC2{},
		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
	}
	err := rcRollingUpgrade.populateAsg(ruObj)

	g.Expect(err).To(gomega.BeNil())

	expectedAsg := autoscaling.Group{AutoScalingGroupName: &correctAsg}

	requestedAsg, ok := rcRollingUpgrade.ruObjNameToASG.Load(ruObj.NamespacedName())
	g.Expect(ok).To(gomega.BeTrue())
	g.Expect(requestedAsg.AutoScalingGroupName).To(gomega.Equal(expectedAsg.AutoScalingGroupName))
}

func TestPopulateAsgTooMany(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsg1 := &autoscaling.Group{
		AutoScalingGroupName: aws.String("too-many"),
	}
	mockAsg2 := &autoscaling.Group{
		AutoScalingGroupName: aws.String("too-many"),
	}
	mockAsgClient := MockAutoscalingGroup{
		autoScalingGroups: []*autoscaling.Group{mockAsg1, mockAsg2},
	}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		TypeMeta: metav1.TypeMeta{Kind: "RollingUpgrade", APIVersion: "v1alpha1"},
		Spec:     upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: "too-many"}}

	rcRollingUpgrade := &RollingUpgradeReconciler{
		Log:          log2.NullLogger{},
		ClusterState: NewClusterState(),
		ASGClient:    mockAsgClient,
		EC2Client:    MockEC2{},
		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
	}
	err := rcRollingUpgrade.populateAsg(ruObj)

	g.Expect(err).To(gomega.Not(gomega.BeNil()))
	g.Expect(err.Error()).To(gomega.Equal("Too many ASGs"))
}

func TestPopulateAsgNone(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		TypeMeta: metav1.TypeMeta{Kind: "RollingUpgrade", APIVersion: "v1alpha1"},
		Spec:     upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: "no-asg-at-all"}}

	rcRollingUpgrade := &RollingUpgradeReconciler{
		Log:          log2.NullLogger{},
		ClusterState: NewClusterState(),
		ASGClient:    &MockAutoscalingGroup{},
		EC2Client:    MockEC2{},
	}
	err := rcRollingUpgrade.populateAsg(ruObj)

	g.Expect(err).To(gomega.Not(gomega.BeNil()))
	g.Expect(err.Error()).To(gomega.Equal("No ASG found"))
}

func TestParallelAsgTracking(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	asgAName := "asg-a"
	asgBName := "asg-b"

	ruObjA := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo-a", Namespace: "default"},
		TypeMeta: metav1.TypeMeta{Kind: "RollingUpgrade", APIVersion: "v1alpha1"},
		Spec:     upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: asgAName}}
	ruObjB := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo-b", Namespace: "default"},
		TypeMeta: metav1.TypeMeta{Kind: "RollingUpgrade", APIVersion: "v1alpha1"},
		Spec:     upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: asgBName}}

	expectedAsgA := autoscaling.Group{AutoScalingGroupName: &asgAName}
	expectedAsgB := autoscaling.Group{AutoScalingGroupName: &asgBName}

	rcRollingUpgrade := &RollingUpgradeReconciler{
		Log:          log2.NullLogger{},
		ClusterState: NewClusterState(),
		ASGClient:    &MockAutoscalingGroup{},
		EC2Client:    MockEC2{},
	}

	err := rcRollingUpgrade.populateAsg(ruObjA)
	g.Expect(err).To(gomega.BeNil())

	err = rcRollingUpgrade.populateAsg(ruObjB)
	g.Expect(err).To(gomega.BeNil())

	//This test ensures that we can lookup each of 2 separate ASGs after populating both
	requestedAsgA, ok := rcRollingUpgrade.ruObjNameToASG.Load(ruObjA.NamespacedName())
	g.Expect(ok).To(gomega.BeTrue())

	requestedAsgB, ok := rcRollingUpgrade.ruObjNameToASG.Load(ruObjB.NamespacedName())
	g.Expect(ok).To(gomega.BeTrue())

	g.Expect(requestedAsgA.AutoScalingGroupName).To(gomega.Equal(expectedAsgA.AutoScalingGroupName))
	g.Expect(requestedAsgB.AutoScalingGroupName).To(gomega.Equal(expectedAsgB.AutoScalingGroupName))
}

type MockNodeList struct {
	v1.NodeInterface

	// used to return errors if needed
	errorFlag bool
}

func (nodeInterface *MockNodeList) List(options metav1.ListOptions) (*corev1.NodeList, error) {
	list := &corev1.NodeList{}

	if nodeInterface.errorFlag {
		return list, errors.New("error flag raised")
	}

	node1 := corev1.Node{TypeMeta: metav1.TypeMeta{Kind: "Node", APIVersion: "v1beta1"},
		ObjectMeta: metav1.ObjectMeta{Name: "node1"}}
	node2 := corev1.Node{TypeMeta: metav1.TypeMeta{Kind: "Node", APIVersion: "v1beta1"},
		ObjectMeta: metav1.ObjectMeta{Name: "node2"}}
	node3 := corev1.Node{TypeMeta: metav1.TypeMeta{Kind: "Node", APIVersion: "v1beta1"},
		ObjectMeta: metav1.ObjectMeta{Name: "node3"}}

	list.Items = []corev1.Node{node1, node2, node3}
	return list, nil
}

func TestPopulateNodeListSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		TypeMeta: metav1.TypeMeta{Kind: "RollingUpgrade", APIVersion: "v1alpha1"}}
	rcRollingUpgrade := createReconciler()

	mockNodeListInterface := &MockNodeList{errorFlag: false}
	err := rcRollingUpgrade.populateNodeList(ruObj, mockNodeListInterface)

	g.Expect(err).To(gomega.BeNil())
	g.Expect(rcRollingUpgrade.NodeList.Items[0].Name).To(gomega.Equal("node1"))
	g.Expect(rcRollingUpgrade.NodeList.Items[1].Name).To(gomega.Equal("node2"))
	g.Expect(rcRollingUpgrade.NodeList.Items[2].Name).To(gomega.Equal("node3"))
}

func TestPopulateNodeListError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		TypeMeta: metav1.TypeMeta{Kind: "RollingUpgrade", APIVersion: "v1alpha1"}}
	rcRollingUpgrade := createReconciler()

	mockNodeListInterface := &MockNodeList{errorFlag: true}
	err := rcRollingUpgrade.populateNodeList(ruObj, mockNodeListInterface)

	g.Expect(err).To(gomega.Not(gomega.BeNil()))
	g.Expect(err.Error()).To(gomega.HavePrefix(ruObj.Name + ": Failed to get all nodes in the cluster:"))
}

func TestFinishExecutionCompleted(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		TypeMeta: metav1.TypeMeta{Kind: "RollingUpgrade", APIVersion: "v1alpha1"}}
	startTime := time.Now()
	ruObj.Status.StartTime = startTime.Format(time.RFC3339)

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	rcRollingUpgrade := &RollingUpgradeReconciler{Client: mgr.GetClient(),
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		Log:             log2.NullLogger{},
		ClusterState:    NewClusterState(),
	}
	ctx := context.TODO()
	mockNodesProcessed := 3

	rcRollingUpgrade.finishExecution(upgrademgrv1alpha1.StatusComplete, mockNodesProcessed, &ctx, ruObj)

	g.Expect(ruObj.Status.CurrentStatus).To(gomega.Equal(upgrademgrv1alpha1.StatusComplete))
	g.Expect(ruObj.Status.NodesProcessed).To(gomega.Equal(mockNodesProcessed))
	g.Expect(ruObj.Status.EndTime).To(gomega.Not(gomega.BeNil()))
	g.Expect(ruObj.Status.TotalProcessingTime).To(gomega.Not(gomega.BeNil()))
	g.Expect(ruObj.Status.Conditions).To(gomega.Equal(
		[]upgrademgrv1alpha1.RollingUpgradeCondition{
			{
				Type:   upgrademgrv1alpha1.UpgradeComplete,
				Status: corev1.ConditionTrue,
			},
		},
	))
}

func TestFinishExecutionError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		ClusterState:    NewClusterState(),
	}
	startTime := time.Now()
	ruObj.Status.StartTime = startTime.Format(time.RFC3339)
	ctx := context.TODO()
	mockNodesProcessed := 3

	rcRollingUpgrade.finishExecution(upgrademgrv1alpha1.StatusError, mockNodesProcessed, &ctx, ruObj)

	g.Expect(ruObj.Status.CurrentStatus).To(gomega.Equal(upgrademgrv1alpha1.StatusError))
	g.Expect(ruObj.Status.NodesProcessed).To(gomega.Equal(mockNodesProcessed))
	g.Expect(ruObj.Status.EndTime).To(gomega.Not(gomega.BeNil()))
	g.Expect(ruObj.Status.TotalProcessingTime).To(gomega.Not(gomega.BeNil()))
	g.Expect(ruObj.Status.Conditions).To(gomega.Equal(
		[]upgrademgrv1alpha1.RollingUpgradeCondition{
			{
				Type:   upgrademgrv1alpha1.UpgradeComplete,
				Status: corev1.ConditionTrue,
			},
		},
	))
}

// RunRestack() goes through the entire process without errors
func TestRunRestackSuccessOneNode(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	someLaunchConfig := "some-launch-config"
	diffLaunchConfig := "different-launch-config"
	az := "az-1"

	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &diffLaunchConfig, AvailabilityZone: &az}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance}}

	strategy := upgrademgrv1alpha1.UpdateStrategy{Mode: upgrademgrv1alpha1.UpdateStrategyModeLazy}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec:       upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg, Strategy: strategy},
	}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	fooNode1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	fooNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}
	// correctNode has the same mockID as the mockInstance and a node name to be processed
	correctNode := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}

	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2, correctNode}}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		ASGClient:       MockAutoscalingGroup{},
		EC2Client:       MockEC2{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		ScriptRunner:    NewScriptRunner(log2.NullLogger{}),
		NodeList:        &nodeList,
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
	}
	rcRollingUpgrade.admissionMap.Store(ruObj.NamespacedName(), "processing")
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.NamespacedName(), &mockAsg)

	ctx := context.TODO()

	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj)
	g.Expect(nodesProcessed).To(gomega.Equal(1))
	g.Expect(err).To(gomega.BeNil())
	_, exists := rcRollingUpgrade.inProcessASGs.Load(someAsg)
	g.Expect(exists).To(gomega.BeTrue())
}

func TestRunRestackSuccessMultipleNodes(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	mockID2 := "some-id-2"
	someLaunchConfig := "some-launch-config"
	diffLaunchConfig := "different-launch-config"
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &diffLaunchConfig, AvailabilityZone: &az}
	mockInstance2 := autoscaling.Instance{InstanceId: &mockID2, LaunchConfigurationName: &diffLaunchConfig, AvailabilityZone: &az}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance, &mockInstance2}}

	strategy := upgrademgrv1alpha1.UpdateStrategy{Mode: upgrademgrv1alpha1.UpdateStrategyModeLazy}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg, Strategy: strategy}}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	fooNode1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	fooNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}
	correctNode := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}
	correctNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID2},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node2"}}

	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2, correctNode, correctNode2}}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		ASGClient:       MockAutoscalingGroup{},
		EC2Client:       MockEC2{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		ScriptRunner:    NewScriptRunner(log2.NullLogger{}),
		NodeList:        &nodeList,
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
	}
	rcRollingUpgrade.admissionMap.Store(ruObj.NamespacedName(), "processing")
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.NamespacedName(), &mockAsg)

	ctx := context.TODO()

	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj)
	g.Expect(nodesProcessed).To(gomega.Equal(2))
	g.Expect(err).To(gomega.BeNil())
}

func TestRunRestackSameLaunchConfig(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	someLaunchConfig := "some-launch-config"
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &someLaunchConfig, AvailabilityZone: &az}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance}}

	strategy := upgrademgrv1alpha1.UpdateStrategy{Mode: upgrademgrv1alpha1.UpdateStrategyModeLazy}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg, Strategy: strategy}}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		ASGClient:       MockAutoscalingGroup{},
		EC2Client:       MockEC2{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
	}
	rcRollingUpgrade.admissionMap.Store(ruObj.NamespacedName(), "processing")
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.NamespacedName(), &mockAsg)

	ctx := context.TODO()

	// This execution should not perform drain or termination, but should pass
	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj)
	g.Expect(nodesProcessed).To(gomega.Equal(1))
	g.Expect(err).To(gomega.BeNil())
}

func TestRunRestackRollingUpgradeNotInMap(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	strategy := upgrademgrv1alpha1.UpdateStrategy{Mode: upgrademgrv1alpha1.UpdateStrategyModeLazy}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{Strategy: strategy},
	}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Log:          log2.NullLogger{},
		ClusterState: NewClusterState(),
		ASGClient:    MockAutoscalingGroup{},
		EC2Client:    MockEC2{},
	}
	ctx := context.TODO()

	g.Expect(rcRollingUpgrade.ruObjNameToASG.Load(ruObj.NamespacedName())).To(gomega.BeNil())
	int, err := rcRollingUpgrade.runRestack(&ctx, ruObj)
	g.Expect(int).To(gomega.Equal(0))
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
	g.Expect(err.Error()).To(gomega.HavePrefix("Unable to load ASG with name: foo"))
}

func TestRunRestackRollingUpgradeNodeNameNotFound(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	someLaunchConfig := "some-launch-config"
	diffLaunchConfig := "different-launch-config"
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &diffLaunchConfig, AvailabilityZone: &az}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance}}

	strategy := upgrademgrv1alpha1.UpdateStrategy{Mode: upgrademgrv1alpha1.UpdateStrategyModeLazy}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg, Strategy: strategy}}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	emptyNodeList := corev1.NodeList{}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		ASGClient:       MockAutoscalingGroup{},
		EC2Client:       MockEC2{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		NodeList:        &emptyNodeList,
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
	}
	rcRollingUpgrade.admissionMap.Store(ruObj.NamespacedName(), "processing")
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.NamespacedName(), &mockAsg)

	ctx := context.TODO()

	// This execution gets past the different launch config check, but fails to be found at the node level
	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj)
	g.Expect(nodesProcessed).To(gomega.Equal(1))
	g.Expect(err).To(gomega.BeNil())
}

func TestRunRestackNoNodeName(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	someLaunchConfig := "some-launch-config"
	diffLaunchConfig := "different-launch-config"
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &diffLaunchConfig, AvailabilityZone: &az}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance}}

	strategy := upgrademgrv1alpha1.UpdateStrategy{Mode: upgrademgrv1alpha1.UpdateStrategyModeLazy}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg, Strategy: strategy}}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	fooNode1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	fooNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}
	// correctNode has the same mockID as the mockInstance
	correctNode := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID}}

	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2, correctNode}}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		ASGClient:       MockAutoscalingGroup{},
		EC2Client:       MockEC2{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		NodeList:        &nodeList,
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
	}
	rcRollingUpgrade.admissionMap.Store(ruObj.NamespacedName(), "processing")
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.NamespacedName(), &mockAsg)

	ctx := context.TODO()

	// This execution gets past the different launch config check, but since there is no node name, it is skipped
	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj)
	g.Expect(nodesProcessed).To(gomega.Equal(1))
	g.Expect(err).To(gomega.BeNil())
}

func TestRunRestackDrainNodeFail(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	someLaunchConfig := "some-launch-config"
	diffLaunchConfig := "different-launch-config"
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID,
		LaunchConfigurationName: &diffLaunchConfig,
		AvailabilityZone:        &az,
	}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance},
	}

	somePreDrain := upgrademgrv1alpha1.PreDrainSpec{
		Script: "exit 1",
	}

	// Will fail upon running the preDrain() script
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  someAsg,
			PreDrain: somePreDrain,
			Strategy: upgrademgrv1alpha1.UpdateStrategy{
				Mode: "eager",
			},
		},
	}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	fooNode1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	fooNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}
	// correctNode has the same mockID as the mockInstance and a node name to be processed
	correctNode := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}

	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2, correctNode}}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		ASGClient:       MockAutoscalingGroup{},
		EC2Client:       MockEC2{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		ScriptRunner:    NewScriptRunner(log2.NullLogger{}),
		NodeList:        &nodeList,
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
	}
	rcRollingUpgrade.admissionMap.Store(ruObj.NamespacedName(), "processing")
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.NamespacedName(), &mockAsg)

	ctx := context.TODO()

	// This execution gets past the different launch config check, but fails to drain the node because of a predrain failing script
	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj)

	g.Expect(nodesProcessed).To(gomega.Equal(1))
	g.Expect(err.Error()).To(gomega.HavePrefix("Error updating instances, ErrorCount: 1, Errors: ["))
}

func TestRunRestackTerminateNodeFail(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	someLaunchConfig := "some-launch-config"
	diffLaunchConfig := "different-launch-config"
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &diffLaunchConfig, AvailabilityZone: &az}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance}}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName: someAsg,
			Strategy: upgrademgrv1alpha1.UpdateStrategy{
				Mode: "lazy",
			},
		},
	}
	// Error flag set, should return error
	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: true, awsErr: awserr.New("some-other-aws-error",
		"some message",
		errors.New("some error"))}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	fooNode1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	fooNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}
	// correctNode has the same mockID as the mockInstance and a node name to be processed
	correctNode := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}

	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2, correctNode}}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		ASGClient:       mockAutoscalingGroup,
		EC2Client:       MockEC2{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		NodeList:        &nodeList,
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
		ScriptRunner:    NewScriptRunner(log2.NullLogger{}),
	}
	rcRollingUpgrade.admissionMap.Store(ruObj.NamespacedName(), "processing")
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.NamespacedName(), &mockAsg)

	ctx := context.TODO()

	// This execution gets past the different launch config check, but fails to terminate node
	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj)
	g.Expect(nodesProcessed).To(gomega.Equal(1))
	g.Expect(err.Error()).To(gomega.HavePrefix("Error updating instances, ErrorCount: 1, Errors: ["))
	g.Expect(err.Error()).To(gomega.ContainSubstring("some error"))
}

func constructAutoScalingInstance(instanceId string, launchConfigName string, azName string) *autoscaling.Instance {
	return &autoscaling.Instance{InstanceId: &instanceId, LaunchConfigurationName: &launchConfigName, AvailabilityZone: &azName}
}

func TestUniformAcrossAzUpdateSuccessMultipleNodes(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	someLaunchConfig := "some-launch-config"
	diffLaunchConfig := "different-launch-config"
	az := "az-1"
	az2 := "az-2"
	az3 := "az-3"
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances: []*autoscaling.Instance{
			constructAutoScalingInstance(mockID+"1"+az, diffLaunchConfig, az),
			constructAutoScalingInstance(mockID+"2"+az, diffLaunchConfig, az),
			constructAutoScalingInstance(mockID+"1"+az2, diffLaunchConfig, az2),
			constructAutoScalingInstance(mockID+"2"+az2, diffLaunchConfig, az2),
			constructAutoScalingInstance(mockID+"3"+az2, diffLaunchConfig, az2),
			constructAutoScalingInstance(mockID+"1"+az3, diffLaunchConfig, az3),
			constructAutoScalingInstance(mockID+"2"+az3, diffLaunchConfig, az3),
			constructAutoScalingInstance(mockID+"3"+az3, diffLaunchConfig, az3),
			constructAutoScalingInstance(mockID+"4"+az3, diffLaunchConfig, az3),
		},
	}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName: someAsg,
			Strategy: upgrademgrv1alpha1.UpdateStrategy{
				Mode: "lazy",
				Type: upgrademgrv1alpha1.UniformAcrossAzUpdateStrategy,
			},
		},
	}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	fooNode1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	fooNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}
	correctNode1az1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID + "1" + az},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}
	correctNode2az1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID + "2" + az},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}
	correctNode1az2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID + "1" + az2},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}
	correctNode2az2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID + "2" + az2},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}
	correctNode3az2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID + "3" + az2},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}
	correctNode1az3 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID + "1" + az3},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}
	correctNode2az3 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID + "2" + az3},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}
	correctNode3az3 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID + "3" + az3},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}
	correctNode4az3 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID + "4" + az3},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}

	nodeList := corev1.NodeList{Items: []corev1.Node{
		fooNode1, fooNode2,
		correctNode1az1, correctNode2az1,
		correctNode1az2, correctNode2az2, correctNode3az2,
		correctNode1az3, correctNode2az3, correctNode3az3, correctNode4az3,
	}}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		ASGClient:       MockAutoscalingGroup{},
		EC2Client:       MockEC2{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		ScriptRunner:    NewScriptRunner(log2.NullLogger{}),
		NodeList:        &nodeList,
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
	}
	rcRollingUpgrade.admissionMap.Store(ruObj.NamespacedName(), "processing")
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.NamespacedName(), &mockAsg)

	ctx := context.TODO()

	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj)
	g.Expect(nodesProcessed).To(gomega.Equal(9))
	g.Expect(err).To(gomega.BeNil())
}

func TestUpdateInstances(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	mockID2 := "some-id-2"
	someLaunchConfig := "some-launch-config"
	diffLaunchConfig := "different-launch-config"
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &diffLaunchConfig, AvailabilityZone: &az}
	mockInstance2 := autoscaling.Instance{InstanceId: &mockID2, LaunchConfigurationName: &diffLaunchConfig, AvailabilityZone: &az}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance, &mockInstance2}}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName: someAsg,
			Strategy: upgrademgrv1alpha1.UpdateStrategy{
				Mode: "lazy",
			},
		},
	}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	fooNode1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	fooNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}
	correctNode := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}
	correctNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID2},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node2"}}

	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2, correctNode, correctNode2}}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		ASGClient:       MockAutoscalingGroup{},
		EC2Client:       MockEC2{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		NodeList:        &nodeList,
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
		ScriptRunner:    NewScriptRunner(log2.NullLogger{}),
	}
	rcRollingUpgrade.admissionMap.Store(ruObj.NamespacedName(), "processing")
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.NamespacedName(), &mockAsg)

	ctx := context.TODO()

	lcName := "A"
	rcRollingUpgrade.ScriptRunner.KubectlCall = "exit 0;"

	err = rcRollingUpgrade.UpdateInstances(&ctx,
		ruObj, mockAsg.Instances, &launchDefinition{launchConfigurationName: &lcName})
	g.Expect(err).ShouldNot(gomega.HaveOccurred())
}

func TestUpdateInstancesError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	mockID2 := "some-id-2"
	someLaunchConfig := "some-launch-config"
	diffLaunchConfig := "different-launch-config"
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &diffLaunchConfig, AvailabilityZone: &az}
	mockInstance2 := autoscaling.Instance{InstanceId: &mockID2, LaunchConfigurationName: &diffLaunchConfig, AvailabilityZone: &az}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance, &mockInstance2}}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName: someAsg,
			Strategy: upgrademgrv1alpha1.UpdateStrategy{
				Mode: "lazy",
			},
		},
	}

	mockAutoScalingGroup := MockAutoscalingGroup{
		errorFlag: true,
		awsErr: awserr.New("UnKnownError",
			"some message",
			nil)}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	fooNode1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	fooNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}
	correctNode := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}
	correctNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID2},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node2"}}

	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2, correctNode, correctNode2}}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		ASGClient:       mockAutoScalingGroup,
		EC2Client:       MockEC2{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		NodeList:        &nodeList,
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
		ScriptRunner:    NewScriptRunner(log2.NullLogger{}),
	}
	rcRollingUpgrade.admissionMap.Store(ruObj.NamespacedName(), "processing")
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.NamespacedName(), &mockAsg)
	rcRollingUpgrade.ScriptRunner.KubectlCall = "exit 0;"

	ctx := context.TODO()

	lcName := "A"
	err = rcRollingUpgrade.UpdateInstances(&ctx,
		ruObj, mockAsg.Instances, &launchDefinition{launchConfigurationName: &lcName})
	g.Expect(err).Should(gomega.HaveOccurred())
	g.Expect(err).Should(gomega.BeAssignableToTypeOf(&UpdateInstancesError{}))
	if updateInstancesError, ok := err.(*UpdateInstancesError); ok {
		g.Expect(len(updateInstancesError.InstanceUpdateErrors)).Should(gomega.Equal(2))
		g.Expect(updateInstancesError.Error()).Should(gomega.ContainSubstring("Error updating instances, ErrorCount: 2"))
	}
}

func TestUpdateInstancesHandlesDeletedInstances(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	someLaunchConfig := "some-launch-config"
	diffLaunchConfig := "different-launch-config"
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &diffLaunchConfig, AvailabilityZone: &az}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance}}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName: someAsg,
			Strategy: upgrademgrv1alpha1.UpdateStrategy{
				Mode: "lazy",
			},
		},
	}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	fooNode1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	correctNode := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}

	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, correctNode}}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:    mgr.GetClient(),
		Log:       log2.NullLogger{},
		ASGClient: MockAutoscalingGroup{},
		EC2Client: MockEC2{
			awsErr: awserr.New("InvalidInstanceID.NotFound", "Instance not found", nil),
		},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		NodeList:        &nodeList,
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
		ScriptRunner:    NewScriptRunner(log2.NullLogger{}),
	}
	rcRollingUpgrade.admissionMap.Store(ruObj.NamespacedName(), "processing")
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.NamespacedName(), &mockAsg)
	rcRollingUpgrade.ScriptRunner.KubectlCall = "exit 0;"

	ctx := context.TODO()

	lcName := "A"
	err = rcRollingUpgrade.UpdateInstances(&ctx,
		ruObj, mockAsg.Instances, &launchDefinition{launchConfigurationName: &lcName})
	g.Expect(err).Should(gomega.BeNil())
}

func TestUpdateInstancesPartialError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	mockID2 := "some-id-2"
	someLaunchConfig := "some-launch-config"
	diffLaunchConfig := "different-launch-config"
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &diffLaunchConfig, AvailabilityZone: &az}
	mockInstance2 := autoscaling.Instance{InstanceId: &mockID2, LaunchConfigurationName: &diffLaunchConfig, AvailabilityZone: &az}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance, &mockInstance2}}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName: someAsg,
			Strategy: upgrademgrv1alpha1.UpdateStrategy{
				Mode: "lazy",
			},
		},
	}

	mockAutoScalingGroup := MockAutoscalingGroup{
		errorFlag: true,
		awsErr: awserr.New("UnKnownError",
			"some message",
			nil),
		errorInstanceId: mockID2,
	}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	fooNode1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	fooNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}
	correctNode := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}
	correctNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID2},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node2"}}

	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2, correctNode, correctNode2}}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		ASGClient:       mockAutoScalingGroup,
		EC2Client:       MockEC2{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		NodeList:        &nodeList,
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
		ScriptRunner:    NewScriptRunner(log2.NullLogger{}),
	}
	rcRollingUpgrade.admissionMap.Store(ruObj.NamespacedName(), "processing")
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.NamespacedName(), &mockAsg)
	rcRollingUpgrade.ScriptRunner.KubectlCall = "exit 0;"
	ctx := context.TODO()

	lcName := "A"
	err = rcRollingUpgrade.UpdateInstances(&ctx,
		ruObj, mockAsg.Instances, &launchDefinition{launchConfigurationName: &lcName})
	g.Expect(err).Should(gomega.HaveOccurred())
	g.Expect(err).Should(gomega.BeAssignableToTypeOf(&UpdateInstancesError{}))
	if updateInstancesError, ok := err.(*UpdateInstancesError); ok {
		g.Expect(len(updateInstancesError.InstanceUpdateErrors)).Should(gomega.Equal(1))
		g.Expect(updateInstancesError.Error()).Should(gomega.Equal("Error updating instances, ErrorCount: 1, Errors: [UnKnownError: some message]"))
	}
}

func TestUpdateInstancesWithZeroInstances(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
		ScriptRunner:    NewScriptRunner(log2.NullLogger{}),
	}
	rcRollingUpgrade.ScriptRunner.KubectlCall = "exit 0;"

	ctx := context.TODO()

	lcName := "A"
	err = rcRollingUpgrade.UpdateInstances(&ctx,
		nil, nil, &launchDefinition{launchConfigurationName: &lcName})
	g.Expect(err).ShouldNot(gomega.HaveOccurred())
}

func TestTestCallKubectlDrainWithoutDrainTimeout(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockKubeCtlCall := "sleep 1; echo"
	mockNodeName := "some-node-name"
	mockAsgName := "some-asg"
	rcRollingUpgrade := createReconciler()
	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: mockAsgName}}

	errChan := make(chan error)
	ctx := context.TODO()

	go rcRollingUpgrade.CallKubectlDrain(mockNodeName, ruObj, errChan)

	output := ""
	select {
	case <-ctx.Done():
		log.Printf("Kubectl drain timed out for node - %s", mockNodeName)
		log.Print(ctx.Err())
		output = "timed-out"
		break
	case err := <-errChan:
		if err != nil {
			log.Printf("Kubectl drain errored for node - %s, error: %s", mockNodeName, err.Error())
			output = "error"
			break
		}
		log.Printf("Kubectl drain completed for node - %s", mockNodeName)
		output = "completed"
		break
	}

	g.Expect(output).To(gomega.ContainSubstring("completed"))
}

func TestTestCallKubectlDrainWithDrainTimeout(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockKubeCtlCall := "sleep 1; echo"
	mockNodeName := "some-node-name"
	rcRollingUpgrade := createReconciler()
	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall

	mockAsgName := "some-asg"
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: mockAsgName}}

	errChan := make(chan error)
	ctx := context.TODO()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	go rcRollingUpgrade.CallKubectlDrain(mockNodeName, ruObj, errChan)

	output := ""
	select {
	case <-ctx.Done():
		log.Printf("Kubectl drain timed out for node - %s", mockNodeName)
		log.Print(ctx.Err())
		output = "timed-out"
		break
	case err := <-errChan:
		if err != nil {
			log.Printf("Kubectl drain errored for node - %s, error: %s", mockNodeName, err.Error())
			output = "error"
			break
		}
		log.Printf("Kubectl drain completed for node - %s", mockNodeName)
		output = "completed"
		break
	}

	g.Expect(output).To(gomega.ContainSubstring("completed"))
}

func TestTestCallKubectlDrainWithZeroDrainTimeout(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockKubeCtlCall := "sleep 1; echo"
	mockNodeName := "some-node-name"
	rcRollingUpgrade := createReconciler()
	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall

	mockAsgName := "some-asg"
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: mockAsgName}}

	errChan := make(chan error)
	ctx := context.TODO()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	go rcRollingUpgrade.CallKubectlDrain(mockNodeName, ruObj, errChan)

	output := ""
	select {
	case <-ctx.Done():
		log.Printf("Kubectl drain timed out for node - %s", mockNodeName)
		log.Print(ctx.Err())
		output = "timed-out"
		break
	case err := <-errChan:
		if err != nil {
			log.Printf("Kubectl drain errored for node - %s, error: %s", mockNodeName, err.Error())
			output = "error"
			break
		}
		log.Printf("Kubectl drain completed for node - %s", mockNodeName)
		output = "completed"
		break
	}

	g.Expect(output).To(gomega.ContainSubstring("completed"))
}

func TestTestCallKubectlDrainWithError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockKubeCtlCall := "cat xyz"
	mockNodeName := "some-node-name"
	rcRollingUpgrade := createReconciler()
	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall

	mockAsgName := "some-asg"
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: mockAsgName}}

	errChan := make(chan error)
	ctx := context.TODO()

	go rcRollingUpgrade.CallKubectlDrain(mockNodeName, ruObj, errChan)

	output := ""
	select {
	case <-ctx.Done():
		log.Printf("Kubectl drain timed out for node - %s", mockNodeName)
		log.Print(ctx.Err())
		output = "timed-out"
		break
	case err := <-errChan:
		if err != nil {
			log.Printf("Kubectl drain errored for node - %s, error: %s", mockNodeName, err.Error())
			output = "error"
			break
		}
		log.Printf("Kubectl drain completed for node - %s", mockNodeName)
		output = "completed"
		break
	}

	g.Expect(output).To(gomega.ContainSubstring("error"))
}

func TestTestCallKubectlDrainWithTimeoutOccurring(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockKubeCtlCall := "sleep 1; echo"
	mockNodeName := "some-node-name"
	rcRollingUpgrade := createReconciler()
	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall

	mockAsgName := "some-asg"
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: mockAsgName}}

	errChan := make(chan error)
	ctx := context.TODO()
	ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	go rcRollingUpgrade.CallKubectlDrain(mockNodeName, ruObj, errChan)

	output := ""
	select {
	case <-ctx.Done():
		log.Printf("Kubectl drain timed out for node - %s", mockNodeName)
		log.Print(ctx.Err())
		output = "timed-out"
		break
	case err := <-errChan:
		if err != nil {
			log.Printf("Kubectl drain errored for node - %s, error: %s", mockNodeName, err.Error())
			output = "error"
			break
		}
		log.Printf("Kubectl drain completed for node - %s", mockNodeName)
		output = "completed"
		break
	}

	g.Expect(output).To(gomega.ContainSubstring("timed-out"))
}

func TestValidateRuObj(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{
		Type:           upgrademgrv1alpha1.RandomUpdateStrategy,
		MaxUnavailable: intstr.Parse("75"),
	}

	rcRollingUpgrade := createReconciler()
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}

	err := rcRollingUpgrade.validateRollingUpgradeObj(ruObj)
	g.Expect(err).To(gomega.BeNil())
}

func TestValidateruObjInvalidMaxUnavailable(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{
		Type:           upgrademgrv1alpha1.RandomUpdateStrategy,
		MaxUnavailable: intstr.Parse("150%"),
	}

	rcRollingUpgrade := createReconciler()
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}

	err := rcRollingUpgrade.validateRollingUpgradeObj(ruObj)
	g.Expect(err.Error()).To(gomega.ContainSubstring("Invalid value for maxUnavailable"))
}

func TestValidateruObjMaxUnavailableZeroPercent(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{
		Type:           upgrademgrv1alpha1.RandomUpdateStrategy,
		MaxUnavailable: intstr.Parse("0%"),
	}

	rcRollingUpgrade := createReconciler()
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}

	err := rcRollingUpgrade.validateRollingUpgradeObj(ruObj)
	g.Expect(err.Error()).To(gomega.ContainSubstring("Invalid value for maxUnavailable"))
}

func TestValidateruObjMaxUnavailableInt(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{
		Type:           upgrademgrv1alpha1.RandomUpdateStrategy,
		MaxUnavailable: intstr.Parse("10"),
	}

	rcRollingUpgrade := createReconciler()
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}

	err := rcRollingUpgrade.validateRollingUpgradeObj(ruObj)
	g.Expect(err).To(gomega.BeNil())
}

func TestValidateruObjMaxUnavailableIntZero(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{
		Type:           upgrademgrv1alpha1.RandomUpdateStrategy,
		MaxUnavailable: intstr.Parse("0"),
	}

	rcRollingUpgrade := createReconciler()
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}

	err := rcRollingUpgrade.validateRollingUpgradeObj(ruObj)
	g.Expect(err.Error()).To(gomega.ContainSubstring("Invalid value for maxUnavailable"))
}

func TestValidateruObjMaxUnavailableIntNegativeValue(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{
		Type:           upgrademgrv1alpha1.RandomUpdateStrategy,
		MaxUnavailable: intstr.Parse("-1"),
	}

	rcRollingUpgrade := createReconciler()
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}

	err := rcRollingUpgrade.validateRollingUpgradeObj(ruObj)
	g.Expect(err.Error()).To(gomega.ContainSubstring("Invalid value for maxUnavailable"))
}

func TestValidateruObjWithStrategyAndDrainTimeoutOnly(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{
		Type: upgrademgrv1alpha1.RandomUpdateStrategy,
	}

	rcRollingUpgrade := createReconciler()
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}

	err := rcRollingUpgrade.validateRollingUpgradeObj(ruObj)
	g.Expect(err.Error()).To(gomega.ContainSubstring("Invalid value for maxUnavailable"))
}

func TestValidateruObjWithoutStrategyOnly(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	ruObj := upgrademgrv1alpha1.RollingUpgrade{}

	rcRollingUpgrade := createReconciler()
	err := rcRollingUpgrade.validateRollingUpgradeObj(&ruObj)

	g.Expect(err).To(gomega.BeNil())
}

func TestValidateruObjStrategyType(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{
		Type:           upgrademgrv1alpha1.RandomUpdateStrategy,
		MaxUnavailable: intstr.Parse("10"),
	}

	rcRollingUpgrade := createReconciler()
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}

	err := rcRollingUpgrade.validateRollingUpgradeObj(ruObj)
	g.Expect(err).To(gomega.BeNil())
}

func TestValidateruObjInvalidStrategyType(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{
		Type:           "xyx",
		MaxUnavailable: intstr.Parse("10"),
	}

	rcRollingUpgrade := createReconciler()
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}

	err := rcRollingUpgrade.validateRollingUpgradeObj(ruObj)
	g.Expect(err.Error()).To(gomega.ContainSubstring("Invalid value for strategy type"))
}

func TestValidateruObjWithYaml(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	strategyYaml := `
drainTimeout: 30
maxUnavailable: 100%
type: randomUpdate
`

	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := yaml.Unmarshal([]byte(strategyYaml), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy yaml object, error: %s", err.Error())
	}

	rcRollingUpgrade := createReconciler()
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}
	rcRollingUpgrade.setDefaultsForRollingUpdateStrategy(ruObj)
	err = rcRollingUpgrade.validateRollingUpgradeObj(ruObj)
	g.Expect(err).To(gomega.BeNil())
}

func TestSetDefaultsForRollingUpdateStrategy(t *testing.T) {

	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}

	rcRollingUpgrade := createReconciler()
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}
	rcRollingUpgrade.setDefaultsForRollingUpdateStrategy(ruObj)

	g.Expect(string(ruObj.Spec.Strategy.Type)).To(gomega.ContainSubstring(string(upgrademgrv1alpha1.RandomUpdateStrategy)))
	g.Expect(ruObj.Spec.Strategy.DrainTimeout).To(gomega.Equal(-1))
	g.Expect(ruObj.Spec.Strategy.MaxUnavailable).To(gomega.Equal(intstr.IntOrString{Type: 0, IntVal: 1}))
}

func TestValidateruObjStrategyAfterSettingDefaults(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{
		Type: upgrademgrv1alpha1.RandomUpdateStrategy,
	}

	rcRollingUpgrade := createReconciler()
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}
	rcRollingUpgrade.setDefaultsForRollingUpdateStrategy(ruObj)
	err := rcRollingUpgrade.validateRollingUpgradeObj(ruObj)

	g.Expect(err).To(gomega.BeNil())
}

func TestValidateruObjStrategyAfterSettingDefaultsWithInvalidStrategyType(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{
		Type: "xyz",
	}

	rcRollingUpgrade := createReconciler()
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}
	rcRollingUpgrade.setDefaultsForRollingUpdateStrategy(ruObj)
	error := rcRollingUpgrade.validateRollingUpgradeObj(ruObj)

	g.Expect(error).To(gomega.Not(gomega.BeNil()))
}

func TestValidateruObjStrategyAfterSettingDefaultsWithOnlyDrainTimeout(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{
		Type:         upgrademgrv1alpha1.RandomUpdateStrategy,
		DrainTimeout: 15,
	}

	rcRollingUpgrade := createReconciler()
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}
	rcRollingUpgrade.setDefaultsForRollingUpdateStrategy(ruObj)
	error := rcRollingUpgrade.validateRollingUpgradeObj(ruObj)

	g.Expect(error).To(gomega.BeNil())
}

func TestValidateruObjStrategyAfterSettingDefaultsWithOnlyMaxUnavailable(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{
		Type:           upgrademgrv1alpha1.RandomUpdateStrategy,
		MaxUnavailable: intstr.Parse("100%"),
	}

	rcRollingUpgrade := createReconciler()
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}
	rcRollingUpgrade.setDefaultsForRollingUpdateStrategy(ruObj)
	error := rcRollingUpgrade.validateRollingUpgradeObj(ruObj)

	g.Expect(error).To(gomega.BeNil())
}

func TestRunRestackNoNodeInAsg(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	someLaunchConfig := "some-launch-config"
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{}}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  someAsg,
			Strategy: upgrademgrv1alpha1.UpdateStrategy{Type: upgrademgrv1alpha1.RandomUpdateStrategy},
		},
	}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	nodeList := corev1.NodeList{Items: []corev1.Node{}}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		ASGClient:       MockAutoscalingGroup{},
		EC2Client:       MockEC2{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		admissionMap:    sync.Map{},
		NodeList:        &nodeList,
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
	}
	rcRollingUpgrade.admissionMap.Store(ruObj.NamespacedName(), "processing")
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.NamespacedName(), &mockAsg)

	ctx := context.TODO()

	// This execution gets past the different launch config check, but since there is no node name, it is skipped
	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj)
	g.Expect(nodesProcessed).To(gomega.Equal(0))
	g.Expect(err).To(gomega.BeNil())
}

func TestWaitForTermination(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	TerminationTimeoutSeconds = 1
	TerminationSleepIntervalSeconds = 1

	mockNodeName := "node-123"
	mockNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: mockNodeName,
		},
	}
	kuberenetesClient := fake.NewSimpleClientset()
	nodeInterface := kuberenetesClient.CoreV1().Nodes()

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		ClusterState:    NewClusterState(),
	}
	_, err = nodeInterface.Create(mockNode)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foo",
			Namespace: "default",
		},
	}

	unjoined, err := rcRollingUpgrade.WaitForTermination(ruObj, mockNodeName, nodeInterface)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(unjoined).To(gomega.BeFalse())

	err = nodeInterface.Delete(mockNodeName, &metav1.DeleteOptions{})
	g.Expect(err).NotTo(gomega.HaveOccurred())

	unjoined, err = rcRollingUpgrade.WaitForTermination(ruObj, mockNodeName, nodeInterface)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(unjoined).To(gomega.BeTrue())
}

func TestWaitForTerminationWhenNodeIsNotFound(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	TerminationTimeoutSeconds = 1
	TerminationSleepIntervalSeconds = 1

	// nodeName is empty when a node is not found.
	mockNodeName := ""
	mockNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: mockNodeName,
		},
	}
	kuberenetesClient := fake.NewSimpleClientset()
	nodeInterface := kuberenetesClient.CoreV1().Nodes()

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		ClusterState:    NewClusterState(),
	}
	_, err = nodeInterface.Create(mockNode)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foo",
			Namespace: "default",
		},
	}

	unjoined, err := rcRollingUpgrade.WaitForTermination(ruObj, mockNodeName, nodeInterface)
	g.Expect(unjoined).To(gomega.BeTrue())
	g.Expect(err).To(gomega.BeNil())
}

func buildManager() (manager.Manager, error) {
	err := upgrademgrv1alpha1.AddToScheme(scheme.Scheme)
	if err != nil {
		return nil, err
	}
	return manager.New(cfg, manager.Options{MetricsBindAddress: "0"})
}

func TestRunRestackWithNodesLessThanMaxUnavailable(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	someLaunchConfig := "some-launch-config"
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &someLaunchConfig, AvailabilityZone: &az}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance}}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName: someAsg,
			Strategy: upgrademgrv1alpha1.UpdateStrategy{
				MaxUnavailable: intstr.IntOrString{Type: 0, IntVal: 2},
				Type:           upgrademgrv1alpha1.RandomUpdateStrategy,
			},
		},
	}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		ASGClient:       MockAutoscalingGroup{},
		EC2Client:       MockEC2{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.NamespacedName(), &mockAsg)
	rcRollingUpgrade.ClusterState.deleteAllInstancesInAsg(someAsg)
	ctx := context.TODO()

	// This execution should not perform drain or termination, but should pass
	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj)
	g.Expect(err).To(gomega.BeNil())
	g.Expect(nodesProcessed).To(gomega.Equal(1))
}

func TestRequiresRefreshHandlesLaunchConfiguration(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	r := &RollingUpgradeReconciler{Log: log2.NullLogger{}}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}

	mockID := "some-id"
	someLaunchConfig := "some-launch-config-v1"
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &someLaunchConfig, AvailabilityZone: &az}

	newLaunchConfig := "some-launch-config-v2"
	definition := launchDefinition{
		launchConfigurationName: &newLaunchConfig,
	}

	result := r.requiresRefresh(ruObj, &mockInstance, &definition)
	g.Expect(result).To(gomega.Equal(true))
}

func TestRequiresRefreshHandlesLaunchTemplateNameVersionUpdate(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockID := "some-id"
	oldLaunchTemplate := &autoscaling.LaunchTemplateSpecification{
		LaunchTemplateName: aws.String("launch-template"),
		Version:            aws.String("1"),
	}
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchTemplate: oldLaunchTemplate, AvailabilityZone: &az}

	newLaunchTemplate := &autoscaling.LaunchTemplateSpecification{
		LaunchTemplateName: aws.String("launch-template"),
		Version:            aws.String("2"),
	}
	definition := launchDefinition{
		launchTemplate: newLaunchTemplate,
	}

	r := &RollingUpgradeReconciler{Log: log2.NullLogger{}}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	result := r.requiresRefresh(ruObj, &mockInstance, &definition)
	g.Expect(result).To(gomega.Equal(true))
}

func TestRequiresRefreshHandlesLaunchTemplateIDVersionUpdate(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockID := "some-id"
	oldLaunchTemplate := &autoscaling.LaunchTemplateSpecification{
		LaunchTemplateId: aws.String("launch-template-id-v1"),
		Version:          aws.String("1"),
	}
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchTemplate: oldLaunchTemplate, AvailabilityZone: &az}

	newLaunchTemplate := &autoscaling.LaunchTemplateSpecification{
		LaunchTemplateId: aws.String("launch-template-id-v1"),
		Version:          aws.String("2"),
	}
	definition := launchDefinition{
		launchTemplate: newLaunchTemplate,
	}
	r := &RollingUpgradeReconciler{Log: log2.NullLogger{}}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	result := r.requiresRefresh(ruObj, &mockInstance, &definition)
	g.Expect(result).To(gomega.Equal(true))
}

func TestRequiresRefreshHandlesLaunchTemplateNameUpdate(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockID := "some-id"
	oldLaunchTemplate := &autoscaling.LaunchTemplateSpecification{
		LaunchTemplateName: aws.String("launch-template"),
		Version:            aws.String("1"),
	}
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchTemplate: oldLaunchTemplate, AvailabilityZone: &az}

	newLaunchTemplate := &autoscaling.LaunchTemplateSpecification{
		LaunchTemplateName: aws.String("launch-template-v2"),
		Version:            aws.String("1"),
	}
	definition := launchDefinition{
		launchTemplate: newLaunchTemplate,
	}
	r := &RollingUpgradeReconciler{Log: log2.NullLogger{}}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	result := r.requiresRefresh(ruObj, &mockInstance, &definition)
	g.Expect(result).To(gomega.Equal(true))
}

func TestRequiresRefreshHandlesLaunchTemplateIDUpdate(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockID := "some-id"
	oldLaunchTemplate := &autoscaling.LaunchTemplateSpecification{
		LaunchTemplateId: aws.String("launch-template-id-v1"),
		Version:          aws.String("1"),
	}
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchTemplate: oldLaunchTemplate, AvailabilityZone: &az}

	newLaunchTemplate := &autoscaling.LaunchTemplateSpecification{
		LaunchTemplateId: aws.String("launch-template-id-v2"),
		Version:          aws.String("1"),
	}
	definition := launchDefinition{
		launchTemplate: newLaunchTemplate,
	}
	r := &RollingUpgradeReconciler{Log: log2.NullLogger{}}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	result := r.requiresRefresh(ruObj, &mockInstance, &definition)
	g.Expect(result).To(gomega.Equal(true))
}

func TestRequiresRefreshNotUpdateIfNoVersionChange(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockID := "some-id"
	instanceLaunchTemplate := &autoscaling.LaunchTemplateSpecification{
		LaunchTemplateId: aws.String("launch-template-id-v1"),
		Version:          aws.String("1"),
	}
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchTemplate: instanceLaunchTemplate, AvailabilityZone: &az}

	launchTemplate := &ec2.LaunchTemplate{
		LaunchTemplateId:    aws.String("launch-template-id-v1"),
		LatestVersionNumber: aws.Int64(1),
	}
	definition := launchDefinition{
		launchTemplate: instanceLaunchTemplate,
	}
	r := &RollingUpgradeReconciler{Log: log2.NullLogger{}}
	r.LaunchTemplates = append(r.LaunchTemplates, launchTemplate)
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	result := r.requiresRefresh(ruObj, &mockInstance, &definition)
	g.Expect(result).To(gomega.Equal(false))
}

func TestForceRefresh(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// Even if launchtemplate is identical but forceRefresh is set, requiresRefresh should return true.
	mockID := "some-id"
	launchTemplate := &autoscaling.LaunchTemplateSpecification{
		LaunchTemplateId: aws.String("launch-template-id-v1"),
		Version:          aws.String("1"),
	}
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchTemplate: launchTemplate, AvailabilityZone: &az}

	definition := launchDefinition{
		launchTemplate: launchTemplate,
	}

	ec2launchTemplate := &ec2.LaunchTemplate{
		LaunchTemplateId:    aws.String("launch-template-id-v1"),
		LatestVersionNumber: aws.Int64(1),
	}

	r := &RollingUpgradeReconciler{Log: log2.NullLogger{}}
	r.LaunchTemplates = append(r.LaunchTemplates, ec2launchTemplate)
	currentTime := metav1.NewTime(metav1.Now().Time)
	oldTime := metav1.NewTime(currentTime.Time.AddDate(0, 0, -1))
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default", CreationTimestamp: currentTime},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			Strategy:     upgrademgrv1alpha1.UpdateStrategy{DrainTimeout: -1},
			ForceRefresh: true,
		},
	}
	// If the node was created before the rollingupgrade object, requiresRefresh should return true
	k8sNode := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "k8sNode", CreationTimestamp: oldTime},
		Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID}}
	nodeList := corev1.NodeList{Items: []corev1.Node{k8sNode}}
	r.NodeList = &nodeList
	result := r.requiresRefresh(ruObj, &mockInstance, &definition)
	g.Expect(result).To(gomega.Equal(true))

	// If the node was created at the same time as rollingupgrade object, requiresRefresh should return false
	k8sNode.CreationTimestamp = currentTime
	nodeList = corev1.NodeList{Items: []corev1.Node{k8sNode}}
	r.NodeList = &nodeList
	result = r.requiresRefresh(ruObj, &mockInstance, &definition)
	g.Expect(result).To(gomega.Equal(false))

	// Reset the timestamp on the k8s node
	k8sNode.CreationTimestamp = oldTime
	nodeList = corev1.NodeList{Items: []corev1.Node{k8sNode}}
	r.NodeList = &nodeList

	// If launchTempaltes are different and forceRefresh is true, requiresRefresh should return true
	newLaunchTemplate := &autoscaling.LaunchTemplateSpecification{
		LaunchTemplateId: aws.String("launch-template-id-v1"),
		Version:          aws.String("1"),
	}

	definition = launchDefinition{
		launchTemplate: newLaunchTemplate,
	}
	result = r.requiresRefresh(ruObj, &mockInstance, &definition)
	g.Expect(result).To(gomega.Equal(true))

	// If launchTemplares are identical AND forceRefresh is false, requiresRefresh should return false
	ruObj.Spec.ForceRefresh = false
	result = r.requiresRefresh(ruObj, &mockInstance, &definition)
	g.Expect(result).To(gomega.Equal(false))

	// If launchConfigs are identical but forceRefresh is true, requiresRefresh should return true
	ruObj.Spec.ForceRefresh = true
	launchConfig := "launch-config"
	mockInstance = autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &launchConfig, AvailabilityZone: &az}
	definition = launchDefinition{
		launchConfigurationName: &launchConfig,
	}
	result = r.requiresRefresh(ruObj, &mockInstance, &definition)
	g.Expect(result).To(gomega.Equal(true))

	// If launchConfigs are identical AND forceRefresh is false, requiresRefresh should return false
	ruObj.Spec.ForceRefresh = false
	result = r.requiresRefresh(ruObj, &mockInstance, &definition)
	g.Expect(result).To(gomega.Equal(false))
}

func TestDrainNodeTerminateTerminatesWhenIgnoreDrainFailuresSet(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"

	// Force quit from the rest of the command
	mockKubeCtlCall := "exit 1;"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			Strategy:            upgrademgrv1alpha1.UpdateStrategy{DrainTimeout: -1},
			IgnoreDrainFailures: true,
			PreDrain: upgrademgrv1alpha1.PreDrainSpec{
				Script: mockKubeCtlCall,
			},
		},
	}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		ClusterState: NewClusterState(),
		Log:          log2.NullLogger{},
		EC2Client:    MockEC2{},
		ASGClient: MockAutoscalingGroup{
			errorFlag: false,
			awsErr:    nil,
		},
		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
	}

	err := rcRollingUpgrade.DrainTerminate(ruObj, mockNode, mockNode)
	g.Expect(err).To(gomega.BeNil()) // don't expect errors.

	// nodeName is empty when node isn't part of the cluster. It must skip drain and terminate.
	err = rcRollingUpgrade.DrainTerminate(ruObj, "", mockNode)
	g.Expect(err).To(gomega.BeNil()) // don't expect errors.

}

func TestUpdateInstancesNotExists(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	someLaunchConfig := "some-launch-config"
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &someLaunchConfig, AvailabilityZone: &az}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance}}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName: someAsg,
			Strategy: upgrademgrv1alpha1.UpdateStrategy{
				Mode: "lazy",
			},
		},
	}

	mgr, _ := buildManager()
	client := mgr.GetClient()
	fooNode := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}

	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode}}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          client,
		Log:             log2.NullLogger{},
		ASGClient:       MockAutoscalingGroup{},
		EC2Client:       MockEC2{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		NodeList:        &nodeList,
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.NamespacedName(), &mockAsg)
	rcRollingUpgrade.ScriptRunner.KubectlCall = "date"

	// Intentionally do not populate the admissionMap with the ruObj

	ctx := context.TODO()
	lcName := "A"
	instChan := make(chan error)
	mockInstanceName1 := "foo1"
	instance1 := autoscaling.Instance{InstanceId: &mockInstanceName1, AvailabilityZone: &az}
	go rcRollingUpgrade.UpdateInstance(&ctx, ruObj, &instance1, &launchDefinition{launchConfigurationName: &lcName}, instChan)
	processCount := 0
	select {
	case <-ctx.Done():
		break
	case err := <-instChan:
		if err == nil {
			processCount++
		}
		break
	}

	g.Expect(processCount).To(gomega.Equal(1))
}

func TestValidateNodesLaunchDefinitionSameLaunchConfig(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someLaunchConfig := "some-launch-config"
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: aws.String("some-id"), LaunchConfigurationName: &someLaunchConfig, AvailabilityZone: &az}
	mockAsg := &autoscaling.Group{
		AutoScalingGroupName:    aws.String("my-asg"),
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance},
	}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: "my-asg"}}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	mockAsgClient := MockAutoscalingGroup{
		autoScalingGroups: []*autoscaling.Group{mockAsg},
	}

	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		ASGClient:       mockAsgClient,
		EC2Client:       MockEC2{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
	}
	rcRollingUpgrade.admissionMap.Store(ruObj.NamespacedName(), "processing")
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.NamespacedName(), mockAsg)

	// This execution should not perform drain or termination, but should pass
	err = rcRollingUpgrade.validateNodesLaunchDefinition(ruObj)
	g.Expect(err).To(gomega.BeNil())
}

func TestValidateNodesLaunchDefinitionDifferentLaunchConfig(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someLaunchConfig := "some-launch-config"
	someOtherLaunchConfig := "some-other-launch-config"
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: aws.String("some-id"), LaunchConfigurationName: &someLaunchConfig, AvailabilityZone: &az}
	mockAsg := &autoscaling.Group{
		AutoScalingGroupName:    aws.String("my-asg"),
		LaunchConfigurationName: &someOtherLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance}}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: "my-asg"}}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	mockAsgClient := MockAutoscalingGroup{
		autoScalingGroups: []*autoscaling.Group{mockAsg},
	}

	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		ASGClient:       mockAsgClient,
		EC2Client:       MockEC2{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
	}
	rcRollingUpgrade.admissionMap.Store(ruObj.NamespacedName(), "processing")
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.NamespacedName(), mockAsg)

	// This execution should not perform drain or termination, but should pass
	err = rcRollingUpgrade.validateNodesLaunchDefinition(ruObj)
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
}

func TestValidateNodesLaunchDefinitionSameLaunchTemplate(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	someLaunchTemplate := &autoscaling.LaunchTemplateSpecification{LaunchTemplateId: aws.String("launch-template-id-v1")}
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: aws.String("some-id"), LaunchTemplate: someLaunchTemplate, AvailabilityZone: &az}
	mockAsg := &autoscaling.Group{
		AutoScalingGroupName: aws.String("my-asg"),
		LaunchTemplate:       someLaunchTemplate,
		Instances:            []*autoscaling.Instance{&mockInstance}}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: "my-asg"}}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	mockAsgClient := MockAutoscalingGroup{
		autoScalingGroups: []*autoscaling.Group{mockAsg},
	}

	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		ASGClient:       mockAsgClient,
		EC2Client:       MockEC2{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
	}
	rcRollingUpgrade.admissionMap.Store(ruObj.NamespacedName(), "processing")
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.NamespacedName(), mockAsg)

	// This execution should not perform drain or termination, but should pass
	err = rcRollingUpgrade.validateNodesLaunchDefinition(ruObj)
	g.Expect(err).To(gomega.BeNil())
}

func TestValidateNodesLaunchDefinitionDifferentLaunchTemplate(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	someLaunchTemplate := &autoscaling.LaunchTemplateSpecification{LaunchTemplateId: aws.String("launch-template-id-v1")}
	someOtherLaunchTemplate := &autoscaling.LaunchTemplateSpecification{LaunchTemplateId: aws.String("launch-template-id-v2")}
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: aws.String("some-id"), LaunchTemplate: someLaunchTemplate, AvailabilityZone: &az}
	mockAsg := &autoscaling.Group{
		AutoScalingGroupName: aws.String("my-asg"),
		LaunchTemplate:       someOtherLaunchTemplate,
		Instances:            []*autoscaling.Instance{&mockInstance}}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: "my-asg"}}

	mgr, err := buildManager()
	g.Expect(err).NotTo(gomega.HaveOccurred())

	mockAsgClient := MockAutoscalingGroup{
		autoScalingGroups: []*autoscaling.Group{mockAsg},
	}

	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		Log:             log2.NullLogger{},
		ASGClient:       mockAsgClient,
		EC2Client:       MockEC2{},
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		ClusterState:    NewClusterState(),
		CacheConfig:     cache.NewConfig(0*time.Second, 0, 0),
	}
	rcRollingUpgrade.admissionMap.Store(ruObj.NamespacedName(), "processing")
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.NamespacedName(), mockAsg)

	// This execution should not perform drain or termination, but should pass
	err = rcRollingUpgrade.validateNodesLaunchDefinition(ruObj)
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
}
