package controllers

import (
	"github.com/aws/aws-sdk-go/service/autoscaling"
	upgrademgrv1alpha1 "github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
)

func TestGetRandomNodeSelector(t *testing.T) {
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

	nodeSelector := getNodeSelector(&mockAsg, ruObj)

	g.Expect(nodeSelector).Should(gomega.BeAssignableToTypeOf(&RandomNodeSelector{}))
}

func TestGetUniformAcrossAzNodeSelector(t *testing.T) {
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
				Type: upgrademgrv1alpha1.UniformAcrossAzUpdateStrategy,
			},
		},
	}

	nodeSelector := getNodeSelector(&mockAsg, ruObj)

	g.Expect(nodeSelector).Should(gomega.BeAssignableToTypeOf(&UniformAcrossAzNodeSelector{}))
}

func TestGetNodeSelectorWithInvalidStrategy(t *testing.T) {
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
				Type: "invalid",
			},
		},
	}

	nodeSelector := getNodeSelector(&mockAsg, ruObj)

	g.Expect(nodeSelector).Should(gomega.BeAssignableToTypeOf(&RandomNodeSelector{}))
}
