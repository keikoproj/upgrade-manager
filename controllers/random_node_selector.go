package controllers

import (
	"github.com/aws/aws-sdk-go/service/autoscaling"
	upgrademgrv1alpha1 "github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"log"
)

type RandomNodeSelector struct {
	maxUnavailable int
	ruObj          *upgrademgrv1alpha1.RollingUpgrade
	asg            *autoscaling.Group
}

func NewRandomNodeSelector(
	asg *autoscaling.Group,
	ruObj *upgrademgrv1alpha1.RollingUpgrade,
) *RandomNodeSelector {
	maxUnavailable := getMaxUnavailable(ruObj.Spec.Strategy, len(asg.Instances))
	log.Printf("Max unavailable calculated for %s is %d", ruObj.Name, maxUnavailable)
	return &RandomNodeSelector{
		maxUnavailable: maxUnavailable,
		ruObj:          ruObj,
		asg:            asg,
	}
}

func (selector *RandomNodeSelector) SelectNodesForRestack(
	state ClusterState,
) []*autoscaling.Instance {
	return getNextAvailableInstances(selector.ruObj.Spec.AsgName,
		selector.maxUnavailable, selector.asg.Instances, state)
}
