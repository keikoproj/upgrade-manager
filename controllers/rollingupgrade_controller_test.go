package controllers

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/autoscaling/autoscalingiface"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/pkg/errors"

	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/onsi/gomega"
	"golang.org/x/net/context"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	upgrademgrv1alpha1 "github.com/orkaproj/upgrade-manager/api/v1alpha1"
)

var c client.Client

var expectedRequest = reconcile.Request{NamespacedName: types.NamespacedName{Name: "foo", Namespace: "default"}}
var depKey = types.NamespacedName{Name: "foo-deployment", Namespace: "default"}
var defaultMsgPrefix = "ru-foo"

const timeout = time.Second * 5

func TestMain(m *testing.M) {
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "config", "crd", "bases")},
	}

	cfg, _ = testEnv.Start()
	m.Run()
}

func TestEchoScript(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	out, err := runScript("echo hello", false, defaultMsgPrefix)

	g.Expect(err).To(gomega.BeNil())
	g.Expect(out).To(gomega.Equal("hello\n"))
}

func TestEchoBackgroundScript(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	out, err := runScript("echo background", true, defaultMsgPrefix)

	g.Expect(err).To(gomega.BeNil())
	g.Expect(out).To(gomega.Equal(""))
}

func TestRunScriptFailure(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	out, err := runScript("echo this will fail; exit 1", false, defaultMsgPrefix)

	g.Expect(err).To(gomega.Not(gomega.BeNil()))
	g.Expect(out).To(gomega.Not(gomega.Equal("")))
}

func TestErrorStatusMarkJanitor(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	instance := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}

	mgr, err := manager.New(cfg, manager.Options{})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	c = mgr.GetClient()

	rcRollingUpgrade := &RollingUpgradeReconciler{Client: mgr.GetClient(),
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		admissionMap:    sync.Map{},
		ruObjNameToASG:  sync.Map{},
	}

	ctx := context.TODO()
	rcRollingUpgrade.finishExecution(StatusError, 3, &ctx, instance)
	g.Expect(instance.ObjectMeta.Annotations[JanitorAnnotation]).To(gomega.Equal(ClearErrorFrequency))
}

func TestMarkObjForCleanupCompleted(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Status.CurrentStatus = StatusComplete

	g.Expect(ruObj.ObjectMeta.Annotations).To(gomega.BeNil())
	MarkObjForCleanup(ruObj)
	g.Expect(ruObj.ObjectMeta.Annotations[JanitorAnnotation]).To(gomega.Equal(ClearCompletedFrequency))
}

func TestMarkObjForCleanupError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Status.CurrentStatus = StatusError

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

	rcRollingUpgrade := &RollingUpgradeReconciler{}
	err := rcRollingUpgrade.preDrainHelper(instance)
	g.Expect(err).To(gomega.BeNil())
}

func TestPreDrainScriptError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	instance := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	instance.Spec.PreDrain.Script = "exit 1"

	rcRollingUpgrade := &RollingUpgradeReconciler{}
	err := rcRollingUpgrade.preDrainHelper(instance)
	g.Expect(err.Error()).To(gomega.HavePrefix("Failed to run preDrain script"))
}

func TestPostDrainHelperPostDrainScriptSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostDrain.Script = "echo Hello, postDrainScript!"

	rcRollingUpgrade := &RollingUpgradeReconciler{}
	err := rcRollingUpgrade.postDrainHelper(ruObj, mockNode, mockKubeCtlCall)

	g.Expect(err).To(gomega.BeNil())
}

func TestPostDrainHelperPostDrainScriptError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostDrain.Script = "exit 1"

	rcRollingUpgrade := &RollingUpgradeReconciler{}
	err := rcRollingUpgrade.postDrainHelper(ruObj, mockNode, mockKubeCtlCall)

	g.Expect(err).To(gomega.Not(gomega.BeNil()))
}

func TestPostDrainHelperPostDrainWaitScriptSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostDrain.PostWaitScript = "echo Hello, postDrainWaitScript!"
	ruObj.Spec.PostDrainDelaySeconds = 0

	rcRollingUpgrade := &RollingUpgradeReconciler{}
	err := rcRollingUpgrade.postDrainHelper(ruObj, mockNode, mockKubeCtlCall)

	g.Expect(err).To(gomega.BeNil())
}

func TestPostDrainHelperPostDrainWaitScriptError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostDrain.PostWaitScript = "exit 1"
	ruObj.Spec.PostDrainDelaySeconds = 0

	rcRollingUpgrade := &RollingUpgradeReconciler{}
	err := rcRollingUpgrade.postDrainHelper(ruObj, mockNode, mockKubeCtlCall)

	g.Expect(err).To(gomega.Not(gomega.BeNil()))
}

func TestDrainNodeSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	rcRollingUpgrade := &RollingUpgradeReconciler{}

	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, mockKubeCtlCall)
	g.Expect(err).To(gomega.BeNil())
}

func TestDrainNodePreDrainError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PreDrain.Script = "exit 1"
	rcRollingUpgrade := &RollingUpgradeReconciler{}

	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, mockKubeCtlCall)
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
}

func TestDrainNodePostDrainScriptError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostDrain.Script = "exit 1"
	rcRollingUpgrade := &RollingUpgradeReconciler{}

	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, mockKubeCtlCall)
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
}

func TestDrainNodePostDrainWaitScriptError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostDrain.PostWaitScript = "exit 1"
	rcRollingUpgrade := &RollingUpgradeReconciler{}

	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, mockKubeCtlCall)
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
}

func TestDrainNodePostDrainFailureToDrainNotFound(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"

	// Force quit from the rest of the command
	mockKubeCtlCall := "echo 'Error from server (NotFound)'; exit 1;"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	rcRollingUpgrade := &RollingUpgradeReconciler{}

	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, mockKubeCtlCall)
	g.Expect(err).To(gomega.BeNil())
}

func TestDrainNodePostDrainFailureToDrain(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"

	// Force quit from the rest of the command
	mockKubeCtlCall := "exit 1;"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	rcRollingUpgrade := &RollingUpgradeReconciler{}

	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, mockKubeCtlCall)
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
}

type MockEC2Instance struct {
	ec2iface.EC2API
	errorFlag bool
	awsErr    awserr.Error
}

func (mockEC2Instance MockEC2Instance) TerminateInstances(input *ec2.TerminateInstancesInput) (*ec2.TerminateInstancesOutput, error) {
	output := &ec2.TerminateInstancesOutput{}
	if mockEC2Instance.errorFlag {
		if mockEC2Instance.awsErr != nil {
			return output, mockEC2Instance.awsErr
		}
	}
	name := input.InstanceIds[0]
	instanceChange := ec2.InstanceStateChange{InstanceId: name}
	output.TerminatingInstances = []*ec2.InstanceStateChange{&instanceChange}
	return output, nil
}

func TestTerminateNodeSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	rcRollingUpgrade := &RollingUpgradeReconciler{}
	mockEC2Instance := MockEC2Instance{errorFlag: false, awsErr: nil}

	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, mockEC2Instance)
	g.Expect(err).To(gomega.BeNil())
}

func TestTerminateNodeErrorNotFound(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	rcRollingUpgrade := &RollingUpgradeReconciler{}
	mockEC2Instance := MockEC2Instance{errorFlag: true, awsErr: awserr.New("InvalidInstanceID.NotFound",
		"some message",
		nil)}

	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, mockEC2Instance)
	g.Expect(err).To(gomega.BeNil())
}

func TestTerminateNodeErrorOtherError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	rcRollingUpgrade := &RollingUpgradeReconciler{}
	mockEC2Instance := MockEC2Instance{errorFlag: true, awsErr: awserr.New("some-other-aws-error",
		"some message",
		errors.New("some error"))}

	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, mockEC2Instance)
	g.Expect(err.Error()).To(gomega.ContainSubstring("some error"))
}

func TestTerminateNodePostTerminateScriptSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostTerminate.Script = "echo hello!"
	rcRollingUpgrade := &RollingUpgradeReconciler{}
	mockEC2Instance := MockEC2Instance{errorFlag: false, awsErr: nil}

	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, mockEC2Instance)
	g.Expect(err).To(gomega.BeNil())
}

func TestTerminateNodePostTerminateScriptErrorNotFoundFromServer(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostTerminate.Script = "echo 'Error from server (NotFound)'; exit 1"
	rcRollingUpgrade := &RollingUpgradeReconciler{}
	mockEC2Instance := MockEC2Instance{errorFlag: false, awsErr: nil}

	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, mockEC2Instance)
	g.Expect(err).To(gomega.BeNil())
}

func TestTerminateNodePostTerminateScriptErrorOtherError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostTerminate.Script = "exit 1"
	rcRollingUpgrade := &RollingUpgradeReconciler{}
	mockEC2Instance := MockEC2Instance{errorFlag: false, awsErr: nil}

	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, mockEC2Instance)
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
	g.Expect(err.Error()).To(gomega.HavePrefix("Failed to run postTerminate script: "))
}

func TestLoadEnvironmentVariables(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	ruInstance := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec:       upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: "asg-foo"}}

	mockID := "aws:///us-west-2a/fake-id-foo"
	mockName := "instance-name-foo"
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: mockName},
		Spec:       corev1.NodeSpec{ProviderID: mockID}}

	err := loadEnvironmentVariables(ruInstance, &node)
	g.Expect(err).To(gomega.BeNil())

	g.Expect(os.Getenv(asgNameKey)).To(gomega.Equal("asg-foo"))
	g.Expect(os.Getenv(instanceIDKey)).To(gomega.Equal("fake-id-foo"))
	g.Expect(os.Getenv(instanceNameKey)).To(gomega.Equal("instance-name-foo"))
}

func TestSetDefaults(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	rcRollingUpgrade := &RollingUpgradeReconciler{}

	g.Expect(ruObj.Spec.Region).To(gomega.Equal(""))
	rcRollingUpgrade.setDefaults(ruObj)
	g.Expect(ruObj.Spec.Region).To(gomega.Equal("us-west-2"))
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
	rcRollingUpgrade := RollingUpgradeReconciler{}
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
	rcRollingUpgrade := RollingUpgradeReconciler{}
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
	rcRollingUpgrade := RollingUpgradeReconciler{}
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
	rcRollingUpgrade := RollingUpgradeReconciler{}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	node := rcRollingUpgrade.getNodeFromAsg(&autoscalingInstance, &nodeList, ruObj)

	g.Expect(node).To(gomega.BeNil())
}

type MockAutoScalingAPI struct {
	autoscalingiface.AutoScalingAPI
}

func (mockAutoScalingInstance *MockAutoScalingAPI) DescribeAutoScalingGroups(input *autoscaling.DescribeAutoScalingGroupsInput) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	output := autoscaling.DescribeAutoScalingGroupsOutput{
		AutoScalingGroups: []*autoscaling.Group{},
	}

	correctAsg := "correct-asg"
	tooMany := "too-many"

	switch *input.AutoScalingGroupNames[0] {
	case correctAsg:
		output.AutoScalingGroups = []*autoscaling.Group{
			{AutoScalingGroupName: &correctAsg},
		}
	case tooMany:
		output.AutoScalingGroups = []*autoscaling.Group{
			{AutoScalingGroupName: &tooMany},
			{AutoScalingGroupName: &tooMany},
		}
	default:
		output.AutoScalingGroups = []*autoscaling.Group{}
	}

	return &output, nil
}

func TestPopulateAsgSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		TypeMeta: metav1.TypeMeta{Kind: "RollingUpgrade", APIVersion: "v1alpha1"},
		Spec:     upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: "correct-asg"}}

	mockAsgAPI := &MockAutoScalingAPI{}
	rcRollingUpgrade := &RollingUpgradeReconciler{}
	err := rcRollingUpgrade.populateAsg(ruObj, mockAsgAPI)

	g.Expect(err).To(gomega.BeNil())

	correctAsg := "correct-asg"
	expectedAsg := autoscaling.Group{AutoScalingGroupName: &correctAsg}

	requestedAsg, ok := rcRollingUpgrade.ruObjNameToASG.Load(ruObj.Name)
	g.Expect(ok).To(gomega.BeTrue())
	g.Expect(requestedAsg.(*autoscaling.Group).AutoScalingGroupName).To(gomega.Equal(expectedAsg.AutoScalingGroupName))
}

func TestPopulateAsgTooMany(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		TypeMeta: metav1.TypeMeta{Kind: "RollingUpgrade", APIVersion: "v1alpha1"},
		Spec:     upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: "too-many"}}

	mockAsgAPI := &MockAutoScalingAPI{}
	rcRollingUpgrade := &RollingUpgradeReconciler{}
	err := rcRollingUpgrade.populateAsg(ruObj, mockAsgAPI)

	g.Expect(err).To(gomega.Not(gomega.BeNil()))
	g.Expect(err.Error()).To(gomega.Equal("Too many asgs"))
}

func TestPopulateAsgNone(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		TypeMeta: metav1.TypeMeta{Kind: "RollingUpgrade", APIVersion: "v1alpha1"},
		Spec:     upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: "no-asg-at-all"}}

	mockAsgAPI := &MockAutoScalingAPI{}
	rcRollingUpgrade := &RollingUpgradeReconciler{}
	err := rcRollingUpgrade.populateAsg(ruObj, mockAsgAPI)

	g.Expect(err).To(gomega.Not(gomega.BeNil()))
	g.Expect(err.Error()).To(gomega.Equal("No ASG found"))
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
	rcRollingUpgrade := &RollingUpgradeReconciler{}

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
	rcRollingUpgrade := &RollingUpgradeReconciler{}

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

	mgr, err := manager.New(cfg, manager.Options{})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	c = mgr.GetClient()

	rcRollingUpgrade := &RollingUpgradeReconciler{Client: mgr.GetClient(),
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		admissionMap:    sync.Map{},
		ruObjNameToASG:  sync.Map{}}
	ctx := context.TODO()
	mockNodesProcessed := 3

	result, err := rcRollingUpgrade.finishExecution(StatusComplete, mockNodesProcessed, &ctx, ruObj)
	g.Expect(err).To(gomega.BeNil())
	g.Expect(result).To(gomega.Not(gomega.BeNil()))

	g.Expect(ruObj.Status.CurrentStatus).To(gomega.Equal(StatusComplete))
	g.Expect(ruObj.Status.NodesProcessed).To(gomega.Equal(mockNodesProcessed))
	g.Expect(ruObj.Status.EndTime).To(gomega.Not(gomega.BeNil()))
	g.Expect(ruObj.Status.TotalProcessingTime).To(gomega.Not(gomega.BeNil()))
}

func TestFinishExecutionError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}

	mgr, err := manager.New(cfg, manager.Options{})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	c = mgr.GetClient()
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		admissionMap:    sync.Map{},
		ruObjNameToASG:  sync.Map{},
	}
	startTime := time.Now()
	ruObj.Status.StartTime = startTime.Format(time.RFC3339)
	ctx := context.TODO()
	mockNodesProcessed := 3

	result, err := rcRollingUpgrade.finishExecution(StatusError, mockNodesProcessed, &ctx, ruObj)
	g.Expect(err).To(gomega.BeNil())
	g.Expect(result).To(gomega.Not(gomega.BeNil()))

	g.Expect(ruObj.Status.CurrentStatus).To(gomega.Equal(StatusError))
	g.Expect(ruObj.Status.NodesProcessed).To(gomega.Equal(mockNodesProcessed))
	g.Expect(ruObj.Status.EndTime).To(gomega.Not(gomega.BeNil()))
	g.Expect(ruObj.Status.TotalProcessingTime).To(gomega.Not(gomega.BeNil()))
}

// RunRestack() goes through the entire process without errors
func TestRunRestackSuccessOneNode(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	someLaunchConfig := "some-launch-config"
	diffLaunchConfig := "different-launch-config"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &diffLaunchConfig}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance}}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg}}
	mockEC2Instance := MockEC2Instance{}

	mgr, err := manager.New(cfg, manager.Options{})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	c = mgr.GetClient()

	fooNode1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	fooNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}
	// correctNode has the same mockID as the mockInstance and a node name to be processed
	correctNode := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}

	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2, correctNode}}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		admissionMap:    sync.Map{},
		ruObjNameToASG:  sync.Map{},
		NodeList:        &nodeList,
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)

	ctx := context.TODO()

	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj, mockEC2Instance, "exit 0;")
	g.Expect(nodesProcessed).To(gomega.Equal(1))
	g.Expect(err).To(gomega.BeNil())
}

func TestRunRestackSuccessMultipleNodes(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	mockID2 := "some-id-2"
	someLaunchConfig := "some-launch-config"
	diffLaunchConfig := "different-launch-config"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &diffLaunchConfig}
	mockInstance2 := autoscaling.Instance{InstanceId: &mockID2, LaunchConfigurationName: &diffLaunchConfig}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance, &mockInstance2}}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg}}
	mockEC2Instance := MockEC2Instance{}

	mgr, err := manager.New(cfg, manager.Options{})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	c = mgr.GetClient()

	fooNode1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	fooNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}
	correctNode := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}
	correctNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID2},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node2"}}

	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2, correctNode, correctNode2}}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		admissionMap:    sync.Map{},
		ruObjNameToASG:  sync.Map{},
		NodeList:        &nodeList,
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)

	ctx := context.TODO()

	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj, mockEC2Instance, "exit 0;")
	g.Expect(nodesProcessed).To(gomega.Equal(2))
	g.Expect(err).To(gomega.BeNil())
}

func TestRunRestackSameLaunchConfig(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	someLaunchConfig := "some-launch-config"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &someLaunchConfig}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance}}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg}}
	mockEC2Instance := MockEC2Instance{}

	mgr, err := manager.New(cfg, manager.Options{})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	c = mgr.GetClient()

	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		admissionMap:    sync.Map{},
		ruObjNameToASG:  sync.Map{},
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)

	ctx := context.TODO()

	// This execution should not perform drain or termination, but should pass
	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj, mockEC2Instance, KubeCtlBinary)
	g.Expect(nodesProcessed).To(gomega.Equal(1))
	g.Expect(err).To(gomega.BeNil())
}

func TestRunRestackRollingUpgradeNotInMap(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	mockEC2Instance := MockEC2Instance{}
	rcRollingUpgrade := &RollingUpgradeReconciler{}
	ctx := context.TODO()

	g.Expect(rcRollingUpgrade.ruObjNameToASG.Load(ruObj.Name)).To(gomega.BeNil())
	int, err := rcRollingUpgrade.runRestack(&ctx, ruObj, mockEC2Instance, KubeCtlBinary)
	g.Expect(int).To(gomega.Equal(0))
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
	g.Expect(err.Error()).To(gomega.HavePrefix("Failed to find rollingUpgrade/ name in map."))
}

func TestRunRestackRollingUpgradeNodeNameNotFound(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	someLaunchConfig := "some-launch-config"
	diffLaunchConfig := "different-launch-config"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &diffLaunchConfig}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance}}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg}}
	mockEC2Instance := MockEC2Instance{}

	mgr, err := manager.New(cfg, manager.Options{})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	c = mgr.GetClient()

	emptyNodeList := corev1.NodeList{}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		admissionMap:    sync.Map{},
		ruObjNameToASG:  sync.Map{},
		NodeList:        &emptyNodeList,
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)

	ctx := context.TODO()

	// This execution gets past the different launch config check, but fails to be found at the node level
	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj, mockEC2Instance, KubeCtlBinary)
	g.Expect(nodesProcessed).To(gomega.Equal(1))
	g.Expect(err).To(gomega.BeNil())
}

func TestRunRestackNoNodeName(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	someLaunchConfig := "some-launch-config"
	diffLaunchConfig := "different-launch-config"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &diffLaunchConfig}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance}}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg}}
	mockEC2Instance := MockEC2Instance{}

	mgr, err := manager.New(cfg, manager.Options{})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	c = mgr.GetClient()

	fooNode1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	fooNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}
	// correctNode has the same mockID as the mockInstance
	correctNode := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID}}

	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2, correctNode}}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		admissionMap:    sync.Map{},
		ruObjNameToASG:  sync.Map{},
		NodeList:        &nodeList,
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)

	ctx := context.TODO()

	// This execution gets past the different launch config check, but since there is no node name, it is skipped
	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj, mockEC2Instance, KubeCtlBinary)
	g.Expect(nodesProcessed).To(gomega.Equal(1))
	g.Expect(err).To(gomega.BeNil())
}

func TestRunRestackDrainNodeFail(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	someLaunchConfig := "some-launch-config"
	diffLaunchConfig := "different-launch-config"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &diffLaunchConfig}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance}}

	somePreDrain := upgrademgrv1alpha1.PreDrainSpec{
		Script: "exit 1",
	}

	// Will fail upon running the preDrain() script
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  someAsg,
			PreDrain: somePreDrain,
		},
	}
	mockEC2Instance := MockEC2Instance{}

	mgr, err := manager.New(cfg, manager.Options{})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	c = mgr.GetClient()

	fooNode1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	fooNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}
	// correctNode has the same mockID as the mockInstance and a node name to be processed
	correctNode := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}

	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2, correctNode}}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		admissionMap:    sync.Map{},
		ruObjNameToASG:  sync.Map{},
		NodeList:        &nodeList,
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)

	ctx := context.TODO()

	// This execution gets past the different launch config check, but fails to drain the node because of a predrain failing script
	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj, mockEC2Instance, KubeCtlBinary)
	g.Expect(nodesProcessed).To(gomega.Equal(0))
	g.Expect(err.Error()).To(gomega.HavePrefix(ruObj.Name + ": Predrain script failed"))
}

func TestRunRestackTerminateNodeFail(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	someLaunchConfig := "some-launch-config"
	diffLaunchConfig := "different-launch-config"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &diffLaunchConfig}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance}}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg}}
	// Error flag set, should return error
	mockEC2Instance := MockEC2Instance{errorFlag: true, awsErr: awserr.New("some-other-aws-error",
		"some message",
		errors.New("some error"))}

	mgr, err := manager.New(cfg, manager.Options{})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	c = mgr.GetClient()

	fooNode1 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/9213851"}}
	fooNode2 := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "foo-bar/1234501"}}
	// correctNode has the same mockID as the mockInstance and a node name to be processed
	correctNode := corev1.Node{Spec: corev1.NodeSpec{ProviderID: "fake-separator/" + mockID},
		ObjectMeta: metav1.ObjectMeta{Name: "correct-node"}}

	nodeList := corev1.NodeList{Items: []corev1.Node{fooNode1, fooNode2, correctNode}}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		admissionMap:    sync.Map{},
		ruObjNameToASG:  sync.Map{},
		NodeList:        &nodeList,
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)

	ctx := context.TODO()

	// This execution gets past the different launch config check, but fails to terminate node
	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj, mockEC2Instance, "exit 0;")
	g.Expect(nodesProcessed).To(gomega.Equal(0))
	g.Expect(err.Error()).To(gomega.ContainSubstring("some error"))
}
