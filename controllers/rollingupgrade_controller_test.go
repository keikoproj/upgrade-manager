package controllers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	log "github.com/keikoproj/upgrade-manager/pkg/log"

	"k8s.io/apimachinery/pkg/util/intstr"

	"gopkg.in/yaml.v2"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/autoscaling/autoscalingiface"
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

	upgrademgrv1alpha1 "github.com/keikoproj/upgrade-manager/api/v1alpha1"
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
		ClusterState:    NewClusterState(),
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

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	err := rcRollingUpgrade.preDrainHelper(instance)
	g.Expect(err).To(gomega.BeNil())
}

func TestPreDrainScriptError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	instance := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	instance.Spec.PreDrain.Script = "exit 1"

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	err := rcRollingUpgrade.preDrainHelper(instance)
	g.Expect(err.Error()).To(gomega.HavePrefix("Failed to run preDrain script"))
}

func TestPostDrainHelperPostDrainScriptSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostDrain.Script = "echo Hello, postDrainScript!"

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	err := rcRollingUpgrade.postDrainHelper(ruObj, mockNode, mockKubeCtlCall)

	g.Expect(err).To(gomega.BeNil())
}

func TestPostDrainHelperPostDrainScriptError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostDrain.Script = "exit 1"

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
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

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
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

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	err := rcRollingUpgrade.postDrainHelper(ruObj, mockNode, mockKubeCtlCall)

	g.Expect(err).To(gomega.Not(gomega.BeNil()))
}

func TestDrainNodeSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}

	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, mockKubeCtlCall, ruObj.Spec.Strategy.DrainTimeout)
	g.Expect(err).To(gomega.BeNil())
}

func TestDrainNodePreDrainError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PreDrain.Script = "exit 1"
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}

	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, mockKubeCtlCall, ruObj.Spec.Strategy.DrainTimeout)
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
}

func TestDrainNodePostDrainScriptError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostDrain.Script = "exit 1"
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}

	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, mockKubeCtlCall, ruObj.Spec.Strategy.DrainTimeout)
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
}

func TestDrainNodePostDrainWaitScriptError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"
	mockKubeCtlCall := "echo"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostDrain.PostWaitScript = "exit 1"
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}

	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, mockKubeCtlCall, ruObj.Spec.Strategy.DrainTimeout)
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
}

func TestDrainNodePostDrainFailureToDrainNotFound(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-name"

	// Force quit from the rest of the command
	mockKubeCtlCall := "echo 'Error from server (NotFound)'; exit 1;"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}

	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, mockKubeCtlCall, ruObj.Spec.Strategy.DrainTimeout)
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
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}

	err := rcRollingUpgrade.DrainNode(ruObj, mockNode, mockKubeCtlCall, ruObj.Spec.Strategy.DrainTimeout)
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
}

type MockAutoscalingGroup struct {
	autoscalingiface.AutoScalingAPI
	errorFlag       bool
	awsErr          awserr.Error
	errorInstanceId string
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

func TestTerminateNodeSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: false, awsErr: nil}
	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, mockAutoscalingGroup)
	g.Expect(err).To(gomega.BeNil())
}

func TestTerminateNodeErrorNotFound(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: true, awsErr: awserr.New("InvalidInstanceID.NotFound",
		"ValidationError: Instance Id not found - No managed instance found for instance ID i-0bba",
		nil)}

	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, mockAutoscalingGroup)
	g.Expect(err).To(gomega.BeNil())
}

func TestTerminateNodeErrorOtherError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: true, awsErr: awserr.New("some-other-aws-error",
		"some message",
		errors.New("some error"))}

	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, mockAutoscalingGroup)
	g.Expect(err.Error()).To(gomega.ContainSubstring("some error"))
}

func TestTerminateNodePostTerminateScriptSuccess(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostTerminate.Script = "echo hello!"
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: false, awsErr: nil}

	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, mockAutoscalingGroup)
	g.Expect(err).To(gomega.BeNil())
}

func TestTerminateNodePostTerminateScriptErrorNotFoundFromServer(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostTerminate.Script = "echo 'Error from server (NotFound)'; exit 1"
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: false, awsErr: nil}

	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, mockAutoscalingGroup)
	g.Expect(err).To(gomega.BeNil())
}

func TestTerminateNodePostTerminateScriptErrorOtherError(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	mockNode := "some-node-id"

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	ruObj.Spec.PostTerminate.Script = "exit 1"
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: false, awsErr: nil}

	err := rcRollingUpgrade.TerminateNode(ruObj, mockNode, mockAutoscalingGroup)
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
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}

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
	rcRollingUpgrade := RollingUpgradeReconciler{ClusterState: NewClusterState()}
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
	rcRollingUpgrade := RollingUpgradeReconciler{ClusterState: NewClusterState()}
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
	rcRollingUpgrade := RollingUpgradeReconciler{ClusterState: NewClusterState()}
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
	rcRollingUpgrade := RollingUpgradeReconciler{ClusterState: NewClusterState()}
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
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
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
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
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
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
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
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}

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
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}

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
		ruObjNameToASG:  sync.Map{},
		ClusterState:    NewClusterState(),
	}
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
		ClusterState:    NewClusterState(),
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
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &diffLaunchConfig, AvailabilityZone: &az}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance}}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec:       upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg},
	}
	mockAutoscalingGroup := MockAutoscalingGroup{}

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
		ClusterState:    NewClusterState(),
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)

	ctx := context.TODO()

	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj, mockAutoscalingGroup, "exit 0;")
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
	az := "az-1"
	mockInstance := autoscaling.Instance{InstanceId: &mockID, LaunchConfigurationName: &diffLaunchConfig, AvailabilityZone: &az}
	mockInstance2 := autoscaling.Instance{InstanceId: &mockID2, LaunchConfigurationName: &diffLaunchConfig, AvailabilityZone: &az}
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances:               []*autoscaling.Instance{&mockInstance, &mockInstance2}}

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg}}
	mockAutoscalingGroup := MockAutoscalingGroup{}

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
		ClusterState:    NewClusterState(),
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)

	ctx := context.TODO()

	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj, mockAutoscalingGroup, "exit 0;")
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

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg}}
	mockAutoscalingGroup := MockAutoscalingGroup{}

	mgr, err := manager.New(cfg, manager.Options{})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	c = mgr.GetClient()

	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		admissionMap:    sync.Map{},
		ruObjNameToASG:  sync.Map{},
		ClusterState:    NewClusterState(),
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)

	ctx := context.TODO()

	// This execution should not perform drain or termination, but should pass
	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj, mockAutoscalingGroup, KubeCtlBinary)
	g.Expect(nodesProcessed).To(gomega.Equal(1))
	g.Expect(err).To(gomega.BeNil())
}

func TestRunRestackRollingUpgradeNotInMap(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}
	mockAutoscalingGroup := MockAutoscalingGroup{}
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	ctx := context.TODO()

	g.Expect(rcRollingUpgrade.ruObjNameToASG.Load(ruObj.Name)).To(gomega.BeNil())
	int, err := rcRollingUpgrade.runRestack(&ctx, ruObj, mockAutoscalingGroup, KubeCtlBinary)
	g.Expect(int).To(gomega.Equal(0))
	g.Expect(err).To(gomega.Not(gomega.BeNil()))
	g.Expect(err.Error()).To(gomega.HavePrefix("Failed to find rollingUpgrade name in map."))
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

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg}}
	mockAutoscalingGroup := MockAutoscalingGroup{}

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
		ClusterState:    NewClusterState(),
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)

	ctx := context.TODO()

	// This execution gets past the different launch config check, but fails to be found at the node level
	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj, mockAutoscalingGroup, KubeCtlBinary)
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

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg}}
	mockAutoscalingGroup := MockAutoscalingGroup{}

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
		ClusterState:    NewClusterState(),
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)

	ctx := context.TODO()

	// This execution gets past the different launch config check, but since there is no node name, it is skipped
	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj, mockAutoscalingGroup, KubeCtlBinary)
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
		},
	}
	mockAutoscalingGroup := MockAutoscalingGroup{}

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
		ClusterState:    NewClusterState(),
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)

	ctx := context.TODO()

	// This execution gets past the different launch config check, but fails to drain the node because of a predrain failing script
	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj, mockAutoscalingGroup, KubeCtlBinary)
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

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg}}
	// Error flag set, should return error
	mockAutoscalingGroup := MockAutoscalingGroup{errorFlag: true, awsErr: awserr.New("some-other-aws-error",
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
		ClusterState:    NewClusterState(),
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)

	ctx := context.TODO()

	// This execution gets past the different launch config check, but fails to terminate node
	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj, mockAutoscalingGroup, "exit 0;")
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
				Type: upgrademgrv1alpha1.UniformAcrossAzUpdateStrategy,
			},
		},
	}
	mockAutoscalingGroup := MockAutoscalingGroup{}

	mgr, err := manager.New(cfg, manager.Options{})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	c = mgr.GetClient()

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
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		admissionMap:    sync.Map{},
		ruObjNameToASG:  sync.Map{},
		NodeList:        &nodeList,
		ClusterState:    NewClusterState(),
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)

	ctx := context.TODO()

	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj, mockAutoscalingGroup, "exit 0;")
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

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg}}
	mockAutoscalingGroup := MockAutoscalingGroup{}

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
		ClusterState:    NewClusterState(),
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)

	ctx := context.TODO()

	err = rcRollingUpgrade.UpdateInstances(&ctx,
		ruObj, mockAsg.Instances, "A", "exit 0;", mockAutoscalingGroup)
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

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg}}
	mockAutoScalingGroup := MockAutoscalingGroup{
		errorFlag: true,
		awsErr: awserr.New("UnKnownError",
			"some message",
			nil)}

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
		ClusterState:    NewClusterState(),
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)

	ctx := context.TODO()

	err = rcRollingUpgrade.UpdateInstances(&ctx,
		ruObj, mockAsg.Instances, "A", "exit 0;", mockAutoScalingGroup)
	g.Expect(err).Should(gomega.HaveOccurred())
	g.Expect(err).Should(gomega.BeAssignableToTypeOf(&UpdateInstancesError{}))
	if updateInstancesError, ok := err.(*UpdateInstancesError); ok {
		g.Expect(len(updateInstancesError.InstanceUpdateErrors)).Should(gomega.Equal(2))
		g.Expect(updateInstancesError.Error()).Should(gomega.ContainSubstring("Error updating instances, ErrorCount: 2"))
	}
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

	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: someAsg}}
	mockAutoScalingGroup := MockAutoscalingGroup{
		errorFlag: true,
		awsErr: awserr.New("UnKnownError",
			"some message",
			nil),
		errorInstanceId: mockID2,
	}

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
		ClusterState:    NewClusterState(),
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)

	ctx := context.TODO()

	err = rcRollingUpgrade.UpdateInstances(&ctx,
		ruObj, mockAsg.Instances, "A", "exit 0;", mockAutoScalingGroup)
	g.Expect(err).Should(gomega.HaveOccurred())
	g.Expect(err).Should(gomega.BeAssignableToTypeOf(&UpdateInstancesError{}))
	if updateInstancesError, ok := err.(*UpdateInstancesError); ok {
		g.Expect(len(updateInstancesError.InstanceUpdateErrors)).Should(gomega.Equal(1))
		g.Expect(updateInstancesError.Error()).Should(gomega.Equal("Error updating instances, ErrorCount: 1, Errors: [UnKnownError: some message]"))
	}
}

func TestUpdateInstancesWithZeroInstances(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mgr, err := manager.New(cfg, manager.Options{})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	c = mgr.GetClient()

	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		admissionMap:    sync.Map{},
		ruObjNameToASG:  sync.Map{},
		ClusterState:    NewClusterState(),
	}

	ctx := context.TODO()

	err = rcRollingUpgrade.UpdateInstances(&ctx,
		nil, nil, "A", "exit 0;", nil)
	g.Expect(err).ShouldNot(gomega.HaveOccurred())
}

func TestTestCallKubectlDrainWithoutDrainTimeout(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	mockKubeCtlCall := "sleep 1; echo"
	mockNodeName := "some-node-name"
	mockAsgName := "some-asg"
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: mockAsgName}}

	errChan := make(chan error)
	ctx := context.TODO()

	go rcRollingUpgrade.CallKubectlDrain(ctx, mockNodeName, mockKubeCtlCall, ruObj, errChan)

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
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}

	mockAsgName := "some-asg"
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: mockAsgName}}

	errChan := make(chan error)
	ctx := context.TODO()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	go rcRollingUpgrade.CallKubectlDrain(ctx, mockNodeName, mockKubeCtlCall, ruObj, errChan)

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
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}

	mockAsgName := "some-asg"
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: mockAsgName}}

	errChan := make(chan error)
	ctx := context.TODO()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	go rcRollingUpgrade.CallKubectlDrain(ctx, mockNodeName, mockKubeCtlCall, ruObj, errChan)

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
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}

	mockAsgName := "some-asg"
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: mockAsgName}}

	errChan := make(chan error)
	ctx := context.TODO()

	go rcRollingUpgrade.CallKubectlDrain(ctx, mockNodeName, mockKubeCtlCall, ruObj, errChan)

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
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}

	mockAsgName := "some-asg"
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{AsgName: mockAsgName}}

	errChan := make(chan error)
	ctx := context.TODO()
	ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	go rcRollingUpgrade.CallKubectlDrain(ctx, mockNodeName, mockKubeCtlCall, ruObj, errChan)

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

	strategyJsonString := "{ \"type\": \"randomUpdate\", \"maxUnavailable\": 75, \"drainTimeout\": 15 }"
	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := json.Unmarshal([]byte(strategyJsonString), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy object, error: %s", err.Error())
	}

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}

	err = rcRollingUpgrade.validateRollingUpgradeObj(ruObj)
	g.Expect(err).To(gomega.BeNil())
}

func TestValidateruObjInvalidMaxUnavailable(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	strategyJsonString := "{ \"type\": \"randomUpdate\", \"maxUnavailable\": \"150%\", \"drainTimeout\": 15 }"
	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := json.Unmarshal([]byte(strategyJsonString), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy object, error: %s", err.Error())
	}

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}

	err = rcRollingUpgrade.validateRollingUpgradeObj(ruObj)
	g.Expect(err.Error()).To(gomega.ContainSubstring("Invalid value for maxUnavailable"))
}

func TestValidateruObjMaxUnavailableZeroPercent(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	strategyJsonString := "{ \"type\": \"randomUpdate\", \"maxUnavailable\": \"0%\", \"drainTimeout\": 15 }"
	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := json.Unmarshal([]byte(strategyJsonString), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy object, error: %s", err.Error())
	}

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}

	err = rcRollingUpgrade.validateRollingUpgradeObj(ruObj)
	g.Expect(err.Error()).To(gomega.ContainSubstring("Invalid value for maxUnavailable"))
}

func TestValidateruObjMaxUnavailableInt(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	strategyJsonString := "{ \"type\": \"randomUpdate\", \"maxUnavailable\": 10, \"drainTimeout\": 15 }"
	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := json.Unmarshal([]byte(strategyJsonString), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy object, error: %s", err.Error())
	}

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}

	err = rcRollingUpgrade.validateRollingUpgradeObj(ruObj)
	g.Expect(err).To(gomega.BeNil())
}

func TestValidateruObjMaxUnavailableIntZero(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	strategyJsonString := "{ \"type\": \"randomUpdate\", \"maxUnavailable\": 0, \"drainTimeout\": 15 }"
	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := json.Unmarshal([]byte(strategyJsonString), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy object, error: %s", err.Error())
	}

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}

	err = rcRollingUpgrade.validateRollingUpgradeObj(ruObj)
	g.Expect(err.Error()).To(gomega.ContainSubstring("Invalid value for maxUnavailable"))
}

func TestValidateruObjMaxUnavailableIntNegativeValue(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	strategyJsonString := "{ \"type\": \"randomUpdate\", \"maxUnavailable\": -1, \"drainTimeout\": 15 }"
	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := json.Unmarshal([]byte(strategyJsonString), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy object, error: %s", err.Error())
	}

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}

	err = rcRollingUpgrade.validateRollingUpgradeObj(ruObj)
	g.Expect(err.Error()).To(gomega.ContainSubstring("Invalid value for maxUnavailable"))
}

func TestValidateruObjWithStrategyAndDrainTimeoutOnly(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	strategyJsonString := "{ \"type\": \"randomUpdate\", \"drainTimeout\": 15 }"
	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := json.Unmarshal([]byte(strategyJsonString), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy object, error: %s", err.Error())
	}

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}

	err = rcRollingUpgrade.validateRollingUpgradeObj(ruObj)
	g.Expect(err.Error()).To(gomega.ContainSubstring("Invalid value for maxUnavailable"))
}

func TestValidateruObjWithoutStrategyOnly(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	RollingUpgradeJsonString := "{}"
	ruObj := upgrademgrv1alpha1.RollingUpgrade{}
	err := json.Unmarshal([]byte(RollingUpgradeJsonString), &ruObj)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling RollingUpgrade object, error: %s", err.Error())
	}
	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	err = rcRollingUpgrade.validateRollingUpgradeObj(&ruObj)

	g.Expect(err).To(gomega.BeNil())
}

func TestValidateruObjStrategyType(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	strategyJsonString := "{ \"type\": \"randomUpdate\", \"maxUnavailable\": 10, \"drainTimeout\": 15 }"
	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := json.Unmarshal([]byte(strategyJsonString), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy object, error: %s", err.Error())
	}

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}

	err = rcRollingUpgrade.validateRollingUpgradeObj(ruObj)
	g.Expect(err).To(gomega.BeNil())
}

func TestValidateruObjInvalidStrategyType(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	strategyJsonString := "{ \"type\": \"xyz\", \"maxUnavailable\": 10, \"drainTimeout\": 15 }"
	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := json.Unmarshal([]byte(strategyJsonString), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy object, error: %s", err.Error())
	}

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}

	err = rcRollingUpgrade.validateRollingUpgradeObj(ruObj)
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

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
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

	strategyJsonString := "{ }"
	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := json.Unmarshal([]byte(strategyJsonString), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy object, error: %s", err.Error())
	}

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
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

	strategyJsonString := "{ \"type\": \"randomUpdate\" }"
	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := json.Unmarshal([]byte(strategyJsonString), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy object, error: %s", err.Error())
	}

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
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

func TestValidateruObjStrategyAfterSettingDefaultsWithInvalidStrategyType(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	strategyJsonString := "{ \"type\": \"xyz\" }"
	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := json.Unmarshal([]byte(strategyJsonString), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy object, error: %s", err.Error())
	}

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
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

	strategyJsonString := "{ \"type\": \"randomUpdate\", \"drainTimeout\": 15 }"
	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := json.Unmarshal([]byte(strategyJsonString), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy object, error: %s", err.Error())
	}

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
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

	strategyJsonString := "{ \"type\": \"randomUpdate\", \"maxUnavailable\": \"100%\" }"
	mockAsgName := "some-asg"
	strategy := upgrademgrv1alpha1.UpdateStrategy{}
	err := json.Unmarshal([]byte(strategyJsonString), &strategy)
	if err != nil {
		fmt.Printf("Error occurred while unmarshalling strategy object, error: %s", err.Error())
	}

	rcRollingUpgrade := &RollingUpgradeReconciler{ClusterState: NewClusterState()}
	ruObj := &upgrademgrv1alpha1.RollingUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: upgrademgrv1alpha1.RollingUpgradeSpec{
			AsgName:  mockAsgName,
			Strategy: strategy,
		},
	}
	rcRollingUpgrade.setDefaultsForRollingUpdateStrategy(ruObj)
	error := rcRollingUpgrade.validateRollingUpgradeObj(ruObj)

	g.Expect(error).To((gomega.BeNil()))
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
	mockAutoscalingGroup := MockAutoscalingGroup{}

	mgr, err := manager.New(cfg, manager.Options{})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	c = mgr.GetClient()

	nodeList := corev1.NodeList{Items: []corev1.Node{}}
	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		admissionMap:    sync.Map{},
		NodeList:        &nodeList,
		ClusterState:    NewClusterState(),
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)

	ctx := context.TODO()

	// This execution gets past the different launch config check, but since there is no node name, it is skipped
	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj, mockAutoscalingGroup, KubeCtlBinary)
	g.Expect(nodesProcessed).To(gomega.Equal(0))
	g.Expect(err).To(gomega.BeNil())
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
	mockAutoscalingGroup := MockAutoscalingGroup{}

	mgr, err := manager.New(cfg, manager.Options{})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	c = mgr.GetClient()

	rcRollingUpgrade := &RollingUpgradeReconciler{
		Client:          mgr.GetClient(),
		generatedClient: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		admissionMap:    sync.Map{},
		ruObjNameToASG:  sync.Map{},
		ClusterState:    NewClusterState(),
	}
	rcRollingUpgrade.ruObjNameToASG.Store(ruObj.Name, &mockAsg)
	rcRollingUpgrade.ClusterState.deleteEntryOfAsg(someAsg)
	ctx := context.TODO()

	// This execution should not perform drain or termination, but should pass
	nodesProcessed, err := rcRollingUpgrade.runRestack(&ctx, ruObj, mockAutoscalingGroup, KubeCtlBinary)
	g.Expect(err).To(gomega.BeNil())
	g.Expect(nodesProcessed).To(gomega.Equal(1))
}
