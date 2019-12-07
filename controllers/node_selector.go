package controllers

import (
	"github.com/aws/aws-sdk-go/service/autoscaling"
	upgrademgrv1alpha1 "github.com/keikoproj/upgrade-manager/api/v1alpha1"
)

type NodeSelector interface {
	SelectNodesForRestack(state ClusterState) []*autoscaling.Instance
}

func getNodeSelector(
	asg *autoscaling.Group,
	ruObj *upgrademgrv1alpha1.RollingUpgrade,
) NodeSelector {
	switch ruObj.Spec.Strategy.Type {
	case upgrademgrv1alpha1.UniformAcrossAzUpdateStrategy:
		return NewUniformAcrossAzNodeSelector(asg, ruObj)
	default:
		return NewRandomNodeSelector(asg, ruObj)
	}
}
