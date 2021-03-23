package controllers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDrainNodeSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	rollingUpgrade := createRollingUpgrade()
	rollingUpgrade.Name = "mock-rollup"
	rollingUpgrade.Namespace = "default"
	rollingUpgrade.Spec.PostDrainDelaySeconds = 30
	rollingUpgrade.Spec.Strategy.DrainTimeout = 30

	mockNode := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	mockNode.Name = "mock-node"
	mockNode.Namespace = "default"

	r := createRollingUpgradeReconciler(t)
	r.Cloud.ClusterNodes = &corev1.NodeList{Items: []corev1.Node{mockNode}}

	ctx := context.Background()
	nodeInterface := r.Auth.Kubernetes.CoreV1().Nodes()
	nodeList, err := nodeInterface.List(ctx, metav1.ListOptions{})
	g.Expect(err).To(gomega.BeNil())
	fmt.Println("Nodes - ", nodeList)

	err = r.Auth.DrainNode(&mockNode, time.Duration(rollingUpgrade.PostDrainDelaySeconds()), rollingUpgrade.DrainTimeout(), r.Auth.Kubernetes)
	g.Expect(err).To(gomega.BeNil())
}

// func TestMarkObjForCleanupCompleted(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	ruObj.Status.CurrentStatus = upgrademgrv1alpha1.StatusComplete

// 	g.Expect(ruObj.ObjectMeta.Annotations).To(gomega.BeNil())
// 	MarkObjForCleanup(ruObj)
// 	g.Expect(ruObj.ObjectMeta.Annotations[JanitorAnnotation]).To(gomega.Equal(ClearCompletedFrequency))
// }

// func TestMarkObjForCleanupError(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	ruObj.Status.CurrentStatus = upgrademgrv1alpha1.StatusError

// 	g.Expect(ruObj.ObjectMeta.Annotations).To(gomega.BeNil())
// 	MarkObjForCleanup(ruObj)
// 	g.Expect(ruObj.ObjectMeta.Annotations[JanitorAnnotation]).To(gomega.Equal(ClearErrorFrequency))
// }

// func TestMarkObjForCleanupNothingHappens(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	ruObj.Status.CurrentStatus = "some other status"

// 	g.Expect(ruObj.ObjectMeta.Annotations).To(gomega.BeNil())
// 	MarkObjForCleanup(ruObj)
// 	g.Expect(ruObj.ObjectMeta.Annotations).To(gomega.BeEmpty())
// }

// func TestPreDrainScriptSuccess(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)

// 	instance := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	instance.Spec.PreDrain.Script = "echo 'Predrain script ran without error'"

// 	rcRollingUpgrade := createReconciler()
// 	err := rcRollingUpgrade.preDrainHelper("test-instance-id", "test", instance)
// 	g.Expect(err).To(gomega.BeNil())
// }

// func TestPreDrainScriptError(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)

// 	instance := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	instance.Spec.PreDrain.Script = "exit 1"

// 	rcRollingUpgrade := createReconciler()
// 	err := rcRollingUpgrade.preDrainHelper("test-instance-id", "test", instance)
// 	g.Expect(err.Error()).To(gomega.ContainSubstring("Failed to run preDrain script"))
// }

// func TestPostDrainHelperPostDrainScriptSuccess(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-name"
// 	mockKubeCtlCall := "echo"

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	ruObj.Spec.PostDrain.Script = "echo Hello, postDrainScript!"

// 	rcRollingUpgrade := createReconciler()
// 	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall
// 	err := rcRollingUpgrade.postDrainHelper("test-instance-id", mockNode, ruObj)

// 	g.Expect(err).To(gomega.BeNil())
// }

// func TestPostDrainHelperPostDrainScriptError(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-name"
// 	mockKubeCtlCall := "k() { echo $@ >> cmdlog.txt ; }; k"
// 	os.Remove("cmdlog.txt")

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	ruObj.Spec.PostDrain.Script = "exit 1"

// 	rcRollingUpgrade := createReconciler()
// 	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall
// 	err := rcRollingUpgrade.postDrainHelper("test-instance-id", mockNode, ruObj)

// 	g.Expect(err).To(gomega.Not(gomega.BeNil()))

// 	// assert node was uncordoned
// 	cmdlog, _ := ioutil.ReadFile("cmdlog.txt")
// 	g.Expect(string(cmdlog)).To(gomega.Equal(fmt.Sprintf("uncordon %s\n", mockNode)))
// 	os.Remove("cmdlog.txt")
// }

// func TestPostDrainHelperPostDrainScriptErrorWithIgnoreDrainFailures(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-name"
// 	mockKubeCtlCall := "k() { echo $@ >> cmdlog.txt ; }; k"
// 	os.Remove("cmdlog.txt")

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
// 		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
// 		Spec:       upgrademgrv1alpha1.RollingUpgradeSpec{IgnoreDrainFailures: true}}
// 	ruObj.Spec.PostDrain.Script = "exit 1"

// 	rcRollingUpgrade := createReconciler()
// 	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall
// 	err := rcRollingUpgrade.postDrainHelper("test-instance-id", mockNode, ruObj)

// 	g.Expect(err).To(gomega.Not(gomega.BeNil()))

// 	// assert node was not uncordoned
// 	cmdlog, _ := ioutil.ReadFile("cmdlog.txt")
// 	g.Expect(string(cmdlog)).To(gomega.Equal(""))
// 	os.Remove("cmdlog.txt")
// }

// func TestPostDrainHelperPostDrainWaitScriptSuccess(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-name"
// 	mockKubeCtlCall := "echo"

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	ruObj.Spec.PostDrain.PostWaitScript = "echo Hello, postDrainWaitScript!"
// 	ruObj.Spec.PostDrainDelaySeconds = 0

// 	rcRollingUpgrade := createReconciler()
// 	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall
// 	err := rcRollingUpgrade.postDrainHelper("test-instance-id", mockNode, ruObj)

// 	g.Expect(err).To(gomega.BeNil())
// }

// func TestPostDrainHelperPostDrainWaitScriptError(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-name"
// 	mockKubeCtlCall := "k() { echo $@ >> cmdlog.txt ; }; k"
// 	os.Remove("cmdlog.txt")

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	ruObj.Spec.PostDrain.PostWaitScript = "exit 1"
// 	ruObj.Spec.PostDrainDelaySeconds = 0

// 	rcRollingUpgrade := createReconciler()
// 	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall
// 	err := rcRollingUpgrade.postDrainHelper("test-instance-id", mockNode, ruObj)

// 	g.Expect(err).To(gomega.Not(gomega.BeNil()))

// 	// assert node was uncordoned
// 	cmdlog, _ := ioutil.ReadFile("cmdlog.txt")
// 	g.Expect(string(cmdlog)).To(gomega.Equal(fmt.Sprintf("uncordon %s\n", mockNode)))
// 	os.Remove("cmdlog.txt")
// }

// func TestPostDrainHelperPostDrainWaitScriptErrorWithIgnoreDrainFailures(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-name"
// 	mockKubeCtlCall := "k() { echo $@ >> cmdlog.txt ; }; k"
// 	os.Remove("cmdlog.txt")

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
// 		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
// 		Spec:       upgrademgrv1alpha1.RollingUpgradeSpec{IgnoreDrainFailures: true}}
// 	ruObj.Spec.PostDrain.PostWaitScript = "exit 1"
// 	ruObj.Spec.PostDrainDelaySeconds = 0

// 	rcRollingUpgrade := createReconciler()
// 	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall
// 	err := rcRollingUpgrade.postDrainHelper("test-instance-id", mockNode, ruObj)

// 	g.Expect(err).To(gomega.Not(gomega.BeNil()))

// 	// assert node was not uncordoned
// 	cmdlog, _ := ioutil.ReadFile("cmdlog.txt")
// 	g.Expect(string(cmdlog)).To(gomega.Equal(""))
// 	os.Remove("cmdlog.txt")
// }

// func TestDrainNodeSuccess(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-name"
// 	mockKubeCtlCall := "echo"

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	rcRollingUpgrade := createReconciler()
// 	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall

// 	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, "test-id", ruObj.Spec.Strategy.DrainTimeout)
// 	g.Expect(err).To(gomega.BeNil())
// }

// func TestDrainNodePreDrainError(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-name"
// 	mockKubeCtlCall := "echo"

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	ruObj.Spec.PreDrain.Script = "exit 1"
// 	rcRollingUpgrade := createReconciler()
// 	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall

// 	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, "test-id", ruObj.Spec.Strategy.DrainTimeout)
// 	g.Expect(err).To(gomega.Not(gomega.BeNil()))
// }

// func TestDrainNodePostDrainScriptError(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-name"
// 	mockKubeCtlCall := "echo"

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	ruObj.Spec.PostDrain.Script = "exit 1"
// 	rcRollingUpgrade := createReconciler()
// 	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall

// 	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, "test-id", ruObj.Spec.Strategy.DrainTimeout)
// 	g.Expect(err).To(gomega.Not(gomega.BeNil()))
// }

// func TestDrainNodePostDrainWaitScriptError(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-name"
// 	mockKubeCtlCall := "echo"

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	ruObj.Spec.PostDrain.PostWaitScript = "exit 1"
// 	rcRollingUpgrade := createReconciler()
// 	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall

// 	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, "test-id", ruObj.Spec.Strategy.DrainTimeout)
// 	g.Expect(err).To(gomega.Not(gomega.BeNil()))
// }

// func TestDrainNodePostDrainFailureToDrainNotFound(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-name"

// 	// Force quit from the rest of the command
// 	mockKubeCtlCall := "echo 'Error from server (NotFound)'; exit 1;"

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	rcRollingUpgrade := createReconciler()
// 	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall

// 	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, "test-id", ruObj.Spec.Strategy.DrainTimeout)
// 	g.Expect(err).To(gomega.BeNil())
// }

// func TestDrainNodePostDrainFailureToDrain(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-name"

// 	// Force quit from the rest of the command
// 	mockKubeCtlCall := "exit 1;"

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
// 		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
// 		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
// 			Strategy: upgrademgrv1alpha1.UpdateStrategy{DrainTimeout: -1},
// 		},
// 	}
// 	rcRollingUpgrade := createReconciler()
// 	rcRollingUpgrade.ScriptRunner.KubectlCall = mockKubeCtlCall

// 	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, "test-id", ruObj.Spec.Strategy.DrainTimeout)
// 	g.Expect(err).To(gomega.Not(gomega.BeNil()))
// }

// func createReconciler() *RollingUpgradeReconciler {
// 	return &RollingUpgradeReconciler{
// 		ClusterState: NewClusterState(),
// 		Log:          log2.NullLogger{},
// 		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
// 	}
// }

// type MockEC2 struct {
// 	ec2iface.EC2API
// 	awsErr       awserr.Error
// 	reservations []*ec2.Reservation
// }

// type MockAutoscalingGroup struct {
// 	autoscalingiface.AutoScalingAPI
// 	errorFlag         bool
// 	awsErr            awserr.Error
// 	errorInstanceId   string
// 	autoScalingGroups []*autoscaling.Group
// }

// func (m MockEC2) CreateTags(_ *ec2.CreateTagsInput) (*ec2.CreateTagsOutput, error) {
// 	if m.awsErr != nil {
// 		return nil, m.awsErr
// 	}
// 	return &ec2.CreateTagsOutput{}, nil
// }

// func (m MockEC2) DescribeInstances(_ *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
// 	return &ec2.DescribeInstancesOutput{Reservations: m.reservations}, nil
// }

// func (m MockEC2) DescribeInstancesPages(input *ec2.DescribeInstancesInput, callback func(*ec2.DescribeInstancesOutput, bool) bool) error {
// 	page, err := m.DescribeInstances(input)
// 	if err != nil {
// 		return err
// 	}
// 	callback(page, false)
// 	return nil
// }

// func (mockAutoscalingGroup MockAutoscalingGroup) EnterStandby(_ *autoscaling.EnterStandbyInput) (*autoscaling.EnterStandbyOutput, error) {
// 	output := &autoscaling.EnterStandbyOutput{}
// 	return output, nil
// }

// func (mockAutoscalingGroup MockAutoscalingGroup) DescribeAutoScalingGroups(input *autoscaling.DescribeAutoScalingGroupsInput) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
// 	var err error
// 	output := autoscaling.DescribeAutoScalingGroupsOutput{
// 		AutoScalingGroups: []*autoscaling.Group{},
// 	}
// 	//To support parallel ASG tracking.
// 	asgA, asgB := "asg-a", "asg-b"

// 	if mockAutoscalingGroup.errorFlag {
// 		err = mockAutoscalingGroup.awsErr
// 	}
// 	switch *input.AutoScalingGroupNames[0] {
// 	case asgA:
// 		output.AutoScalingGroups = []*autoscaling.Group{
// 			{AutoScalingGroupName: &asgA},
// 		}
// 	case asgB:
// 		output.AutoScalingGroups = []*autoscaling.Group{
// 			{AutoScalingGroupName: &asgB},
// 		}
// 	default:
// 		output.AutoScalingGroups = mockAutoscalingGroup.autoScalingGroups
// 	}
// 	return &output, err
// }

// func (mockAutoscalingGroup MockAutoscalingGroup) TerminateInstanceInAutoScalingGroup(input *autoscaling.TerminateInstanceInAutoScalingGroupInput) (*autoscaling.TerminateInstanceInAutoScalingGroupOutput, error) {
// 	output := &autoscaling.TerminateInstanceInAutoScalingGroupOutput{}
// 	if mockAutoscalingGroup.errorFlag {
// 		if mockAutoscalingGroup.awsErr != nil {
// 			if len(mockAutoscalingGroup.errorInstanceId) <= 0 ||
// 				mockAutoscalingGroup.errorInstanceId == *input.InstanceId {
// 				return output, mockAutoscalingGroup.awsErr
// 			}
// 		}
// 	}
// 	asgChange := autoscaling.Activity{ActivityId: aws.String("xxx"), AutoScalingGroupName: aws.String("sss"), Cause: aws.String("xxx"), StartTime: aws.Time(time.Now()), StatusCode: aws.String("200"), StatusMessage: aws.String("success")}
// 	output.Activity = &asgChange
// 	return output, nil
// }

// func TestGetInProgressInstances(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockInstances := []*autoscaling.Instance{
// 		{
// 			InstanceId: aws.String("i-0123foo"),
// 		},
// 		{
// 			InstanceId: aws.String("i-0123bar"),
// 		},
// 	}
// 	expectedInstance := &autoscaling.Instance{
// 		InstanceId: aws.String("i-0123foo"),
// 	}
// 	mockReservations := []*ec2.Reservation{
// 		{
// 			Instances: []*ec2.Instance{
// 				{
// 					InstanceId: aws.String("i-0123foo"),
// 				},
// 			},
// 		},
// 	}
// 	reconciler := &RollingUpgradeReconciler{
// 		ClusterState: NewClusterState(),
// 		EC2Client:    MockEC2{reservations: mockReservations},
// 		ASGClient: MockAutoscalingGroup{
// 			errorFlag: false,
// 			awsErr:    nil,
// 		},
// 		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
// 	}
// 	inProgressInstances, err := reconciler.getInProgressInstances(mockInstances)
// 	g.Expect(err).To(gomega.BeNil())
// 	g.Expect(inProgressInstances).To(gomega.ContainElement(expectedInstance))
// 	g.Expect(inProgressInstances).To(gomega.HaveLen(1))
// }

// func TestTerminateNodeSuccess(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-id"

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	rcRollingUpgrade := &RollingUpgradeReconciler{
// 		ClusterState: NewClusterState(),
// 		Log:          log2.NullLogger{},
// 		EC2Client:    MockEC2{},
// 		ASGClient: MockAutoscalingGroup{
// 			errorFlag: false,
// 			awsErr:    nil,
// 		},
// 		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
// 	}

// 	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, "")
// 	g.Expect(err).To(gomega.BeNil())
// }

// func TestTerminateNodeErrorNotFound(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-id"

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: true, awsErr: awserr.New("InvalidInstanceID.NotFound",
// 		"ValidationError: Instance Id not found - No managed instance found for instance ID i-0bba",
// 		nil)}
// 	rcRollingUpgrade := &RollingUpgradeReconciler{
// 		ClusterState: NewClusterState(),
// 		Log:          log2.NullLogger{},
// 		ASGClient:    mockAutoscalingGroup,
// 		EC2Client:    MockEC2{},
// 		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
// 	}

// 	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, "")
// 	g.Expect(err).To(gomega.BeNil())
// }

// func init() {
// 	WaiterMaxDelay = time.Second * 2
// 	WaiterMinDelay = time.Second * 1
// 	WaiterMaxAttempts = uint32(2)
// }

// func TestTerminateNodeErrorScalingActivityInProgressWithRetry(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-id"

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: true, awsErr: awserr.New(autoscaling.ErrCodeScalingActivityInProgressFault,
// 		"Scaling activities in progress",
// 		nil)}
// 	rcRollingUpgrade := &RollingUpgradeReconciler{
// 		ClusterState: NewClusterState(),
// 		Log:          log2.NullLogger{},
// 		ASGClient:    mockAutoscalingGroup,
// 		EC2Client:    MockEC2{},
// 		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
// 	}
// 	go func() {
// 		time.Sleep(WaiterMaxDelay)
// 		rcRollingUpgrade.ASGClient = MockAutoscalingGroup{
// 			errorFlag: false,
// 			awsErr:    nil,
// 		}
// 	}()
// 	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, "")
// 	g.Expect(err).To(gomega.BeNil())
// }

// func TestTerminateNodeErrorScalingActivityInProgress(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-id"

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: true, awsErr: awserr.New(autoscaling.ErrCodeScalingActivityInProgressFault,
// 		"Scaling activities in progress",
// 		nil)}
// 	rcRollingUpgrade := &RollingUpgradeReconciler{
// 		ClusterState: NewClusterState(),
// 		Log:          log2.NullLogger{},
// 		ASGClient:    mockAutoscalingGroup,
// 		EC2Client:    MockEC2{},
// 		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
// 	}
// 	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, "")
// 	g.Expect(err.Error()).To(gomega.ContainSubstring("no more retries left"))
// }

// func TestTerminateNodeErrorResourceContention(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-id"

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: true, awsErr: awserr.New(autoscaling.ErrCodeResourceContentionFault,
// 		"Have a pending update on resource",
// 		nil)}
// 	rcRollingUpgrade := &RollingUpgradeReconciler{
// 		ClusterState: NewClusterState(),
// 		Log:          log2.NullLogger{},
// 		ASGClient:    mockAutoscalingGroup,
// 		EC2Client:    MockEC2{},
// 		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
// 	}

// 	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, "")
// 	g.Expect(err.Error()).To(gomega.ContainSubstring("no more retries left"))
// }

// func TestTerminateNodeErrorOtherError(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-id"

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: true, awsErr: awserr.New("some-other-aws-error",
// 		"some message",
// 		fmt.Errorf("some error"))}

// 	rcRollingUpgrade := &RollingUpgradeReconciler{
// 		ClusterState: NewClusterState(),
// 		Log:          log2.NullLogger{},
// 		ASGClient:    mockAutoscalingGroup,
// 		EC2Client:    MockEC2{},
// 		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
// 	}
// 	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, "")
// 	g.Expect(err.Error()).To(gomega.ContainSubstring("some error"))
// }

// func TestTerminateNodePostTerminateScriptSuccess(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-id"

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	ruObj.Spec.PostTerminate.Script = "echo hello!"
// 	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: false, awsErr: nil}
// 	rcRollingUpgrade := &RollingUpgradeReconciler{
// 		ClusterState: NewClusterState(),
// 		Log:          log2.NullLogger{},
// 		ASGClient:    mockAutoscalingGroup,
// 		EC2Client:    MockEC2{},
// 		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
// 	}
// 	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, "")
// 	g.Expect(err).To(gomega.BeNil())
// }

// func TestTerminateNodePostTerminateScriptErrorNotFoundFromServer(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-id"

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	ruObj.Spec.PostTerminate.Script = "echo 'Error from server (NotFound)'; exit 1"
// 	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: false, awsErr: nil}
// 	rcRollingUpgrade := &RollingUpgradeReconciler{
// 		ClusterState: NewClusterState(),
// 		Log:          log2.NullLogger{},
// 		ASGClient:    mockAutoscalingGroup,
// 		EC2Client:    MockEC2{},
// 		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
// 	}
// 	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, "")
// 	g.Expect(err).To(gomega.BeNil())
// }

// func TestTerminateNodePostTerminateScriptErrorOtherError(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)
// 	mockNode := "some-node-id"

// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	ruObj.Spec.PostTerminate.Script = "exit 1"
// 	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: false, awsErr: nil}
// 	rcRollingUpgrade := &RollingUpgradeReconciler{
// 		ClusterState: NewClusterState(),
// 		Log:          log2.NullLogger{},
// 		ASGClient:    mockAutoscalingGroup,
// 		EC2Client:    MockEC2{},
// 		ScriptRunner: NewScriptRunner(log2.NullLogger{}),
// 	}
// 	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, "")
// 	g.Expect(err).To(gomega.Not(gomega.BeNil()))
// 	g.Expect(err.Error()).To(gomega.ContainSubstring("Failed to run postTerminate script: "))
// }

// func TestLoadEnvironmentVariables(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)

// 	r := &ScriptRunner{}

// 	mockID := "fake-id-foo"
// 	mockName := "instance-name-foo"

// 	env := r.buildEnv(&upgrademgrv1alpha1.RollingUpgrade{
// 		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
// 			AsgName: "asg-foo",
// 		},
// 	}, mockID, mockName)
// 	g.Expect(env).To(gomega.HaveLen(3))

// }

// func TestGetNodeNameFoundNode(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)

// 	mockInstanceID := "123456"
// 	autoscalingInstance := autoscaling.Instance{InstanceId: &mockInstanceID}

// 	fooNode1 := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "fooNode1"},
// 		Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
// 	fooNode2 := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "fooNode2"},
// 		Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}
// 	correctNode := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "correctNode"},
// 		Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockInstanceID}}

// 	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2, correctNode}}
// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	rcRollingUpgrade := createReconciler()
// 	name := rcRollingUpgrade.getNodeName(&autoscalingInstance, &nodeList, ruObj)

// 	g.Expect(name).To(gomega.Equal("correctNode"))
// }

// func TestGetNodeNameMissingNode(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)

// 	mockInstanceID := "123456"
// 	autoscalingInstance := autoscaling.Instance{InstanceId: &mockInstanceID}

// 	fooNode1 := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "fooNode1"},
// 		Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
// 	fooNode2 := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "fooNode2"},
// 		Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}

// 	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2}}
// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	rcRollingUpgrade := createReconciler()
// 	name := rcRollingUpgrade.getNodeName(&autoscalingInstance, &nodeList, ruObj)

// 	g.Expect(name).To(gomega.Equal(""))
// }

// func TestGetNodeFromAsgFoundNode(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)

// 	mockInstanceID := "123456"
// 	autoscalingInstance := autoscaling.Instance{InstanceId: &mockInstanceID}

// 	fooNode1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
// 	fooNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}

// 	correctNode := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockInstanceID}}

// 	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2, correctNode}}
// 	rcRollingUpgrade := createReconciler()
// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	node := rcRollingUpgrade.getNodeFromAsg(&autoscalingInstance, &nodeList, ruObj)

// 	g.Expect(node).To(gomega.Not(gomega.BeNil()))
// 	g.Expect(node).To(gomega.Equal(&correctNode))
// }

// func TestGetNodeFromAsgMissingNode(t *testing.T) {
// 	g := gomega.NewGomegaWithT(t)

// 	mockInstanceID := "123456"
// 	autoscalingInstance := autoscaling.Instance{InstanceId: &mockInstanceID}

// 	fooNode1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
// 	fooNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}

// 	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2}}
// 	rcRollingUpgrade := createReconciler()
// 	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
// 	node := rcRollingUpgrade.getNodeFromAsg(&autoscalingInstance, &nodeList, ruObj)

// 	g.Expect(node).To(gomega.BeNil())
// }
