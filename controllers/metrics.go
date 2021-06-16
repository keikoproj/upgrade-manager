package controllers

import (
	"time"

	"github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"github.com/keikoproj/upgrade-manager/controllers/common"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Update last batch nodes
func (s *RollingUpgradeContext) UpdateLastBatchNodes(batchNodes map[string]*v1alpha1.NodeInProcessing) {
	keys := make([]string, 0, len(batchNodes))
	for k := range batchNodes {
		keys = append(keys, k)
	}
	s.RollingUpgrade.Status.LastBatchNodes = keys
}

// Update Node Statistics
func (s *RollingUpgradeContext) UpdateStatistics(nodeSteps map[string][]v1alpha1.NodeStepDuration) {
	for _, v := range nodeSteps {
		for _, step := range v {
			s.AddNodeStepDuration(step)
		}
	}
}

// Add one step duration
func (s *RollingUpgradeContext) AddNodeStepDuration(nsd v1alpha1.NodeStepDuration) {
	// if step exists, add count and sum, otherwise append
	for _, s := range s.RollingUpgrade.Status.Statistics {
		if s.StepName == nsd.StepName {
			s.DurationSum = metav1.Duration{
				Duration: s.DurationSum.Duration + nsd.Duration.Duration,
			}
			s.DurationCount += 1
			return
		}
	}
	s.RollingUpgrade.Status.Statistics = append(s.RollingUpgrade.Status.Statistics, &v1alpha1.RollingUpgradeStatistics{
		StepName: nsd.StepName,
		DurationSum: metav1.Duration{
			Duration: nsd.Duration.Duration,
		},
		DurationCount: 1,
	})
}

// Add one step duration
func (s *RollingUpgradeContext) ToStepDuration(groupName, nodeName string, stepName v1alpha1.RollingUpgradeStep, duration time.Duration) v1alpha1.NodeStepDuration {
	//Add to system level statistics
	common.AddStepDuration(groupName, string(stepName), duration)
	return v1alpha1.NodeStepDuration{
		GroupName: groupName,
		NodeName:  nodeName,
		StepName:  stepName,
		Duration: metav1.Duration{
			Duration: duration,
		},
	}
}

// Node turns onto step
func (s *RollingUpgradeContext) NodeStep(InProcessingNodes map[string]*v1alpha1.NodeInProcessing,
	nodeSteps map[string][]v1alpha1.NodeStepDuration, groupName, nodeName string, stepName v1alpha1.RollingUpgradeStep) {

	var inProcessingNode *v1alpha1.NodeInProcessing
	if n, ok := InProcessingNodes[nodeName]; !ok {
		inProcessingNode = &v1alpha1.NodeInProcessing{
			NodeName:         nodeName,
			StepName:         stepName,
			UpgradeStartTime: metav1.Now(),
			StepStartTime:    metav1.Now(),
		}
		InProcessingNodes[nodeName] = inProcessingNode
	} else {
		inProcessingNode = n
	}

	inProcessingNode.StepEndTime = metav1.Now()
	var duration = inProcessingNode.StepEndTime.Sub(inProcessingNode.StepStartTime.Time)
	if stepName == v1alpha1.NodeRotationCompleted {
		//Add overall and remove the node from in-processing map
		var total = inProcessingNode.StepEndTime.Sub(inProcessingNode.UpgradeStartTime.Time)
		duration1 := s.ToStepDuration(groupName, nodeName, inProcessingNode.StepName, duration)
		duration2 := s.ToStepDuration(groupName, nodeName, v1alpha1.NodeRotationTotal, total)
		s.addNodeStepDuration(nodeSteps, nodeName, duration1)
		s.addNodeStepDuration(nodeSteps, nodeName, duration2)
	} else if inProcessingNode.StepName != stepName { //Still same step
		var oldOrder = v1alpha1.NodeRotationStepOrders[inProcessingNode.StepName]
		var newOrder = v1alpha1.NodeRotationStepOrders[stepName]
		if newOrder > oldOrder { //Make sure the steps running in order
			stepDuration := s.ToStepDuration(groupName, nodeName, inProcessingNode.StepName, duration)
			inProcessingNode.StepStartTime = metav1.Now()
			inProcessingNode.StepName = stepName
			s.addNodeStepDuration(nodeSteps, nodeName, stepDuration)
		}
	}
}

func (s *RollingUpgradeContext) addNodeStepDuration(steps map[string][]v1alpha1.NodeStepDuration, nodeName string, nsd v1alpha1.NodeStepDuration) {
	s.metricsMutex.Lock()
	if stepDuration, ok := steps[nodeName]; !ok {
		steps[nodeName] = []v1alpha1.NodeStepDuration{
			nsd,
		}
	} else {
		stepDuration = append(stepDuration, nsd)
		steps[nodeName] = stepDuration
	}
	s.metricsMutex.Unlock()
}
