package controllers

import (
	"github.com/aws/aws-sdk-go/service/autoscaling"
	upgrademgrv1alpha1 "github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
)

func TestUniformAcrossAzNodeSelectorSelectNodes(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	someLaunchConfig := "some-launch-config"
	diffLaunchConfig := "different-launch-config"
	az := "az-1"
	az2 := "az-2"
	az1Instance1 := constructAutoScalingInstance(mockID+"1-"+az, diffLaunchConfig, az)
	az2Instance1 := constructAutoScalingInstance(mockID+"1-"+az2, diffLaunchConfig, az2)
	az2Instance2 := constructAutoScalingInstance(mockID+"2-"+az2, diffLaunchConfig, az2)
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances: []*autoscaling.Instance{
			az1Instance1,
			az2Instance1,
			az2Instance2,
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

	clusterState := NewClusterState()
	clusterState.initializeAsg(*mockAsg.AutoScalingGroupName, mockAsg.Instances)

	nodeSelector := NewUniformAcrossAzNodeSelector(&mockAsg, ruObj)
	instances := nodeSelector.SelectNodesForRestack(clusterState)

	g.Expect(2).To(gomega.Equal(len(instances)))

	// group instances by AZ
	instancesByAz := make(map[string][]*autoscaling.Instance)
	for _, instance := range instances {
		az := instance.AvailabilityZone
		if _, ok := instancesByAz[*az]; !ok {
			instancesInAz := make([]*autoscaling.Instance, 0, len(instances))
			instancesByAz[*az] = instancesInAz
		}
		instancesByAz[*az] = append(instancesByAz[*az], instance)
	}

	// assert on number of instances in each az
	g.Expect(1).To(gomega.Equal(len(instancesByAz[az])))
	g.Expect(1).To(gomega.Equal(len(instancesByAz[az2])))
}

func TestUniformAcrossAzNodeSelectorSelectNodesOneAzComplete(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	someAsg := "some-asg"
	mockID := "some-id"
	someLaunchConfig := "some-launch-config"
	diffLaunchConfig := "different-launch-config"
	az := "az-1"
	az2 := "az-2"
	az1Instance1 := constructAutoScalingInstance(mockID+"1-"+az, diffLaunchConfig, az)
	az2Instance1 := constructAutoScalingInstance(mockID+"1-"+az2, diffLaunchConfig, az2)
	az2Instance2 := constructAutoScalingInstance(mockID+"2-"+az2, diffLaunchConfig, az2)
	mockAsg := autoscaling.Group{AutoScalingGroupName: &someAsg,
		LaunchConfigurationName: &someLaunchConfig,
		Instances: []*autoscaling.Instance{
			az1Instance1,
			az2Instance1,
			az2Instance2,
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

	clusterState := NewClusterState()
	clusterState.initializeAsg(*mockAsg.AutoScalingGroupName, mockAsg.Instances)
	clusterState.markUpdateCompleted(mockID + "1-" + az)
	clusterState.markUpdateCompleted(mockID + "1-" + az2)

	nodeSelector := NewUniformAcrossAzNodeSelector(&mockAsg, ruObj)
	instances := nodeSelector.SelectNodesForRestack(clusterState)

	g.Expect(1).To(gomega.Equal(len(instances)))
	g.Expect(&az2).To(gomega.Equal(instances[0].AvailabilityZone))
}
