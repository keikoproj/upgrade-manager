/*
Copyright 2021 Intuit Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/keikoproj/upgrade-manager/controllers/common"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// RollingUpgradeSpec defines the desired state of RollingUpgrade
type RollingUpgradeSpec struct {
	PostDrainDelaySeconds int                 `json:"postDrainDelaySeconds,omitempty"`
	NodeIntervalSeconds   int                 `json:"nodeIntervalSeconds,omitempty"`
	AsgName               string              `json:"asgName,omitempty"`
	PreDrain              PreDrainSpec        `json:"preDrain,omitempty"`
	PostDrain             PostDrainSpec       `json:"postDrain,omitempty"`
	PostTerminate         PostTerminateSpec   `json:"postTerminate,omitempty"`
	Strategy              UpdateStrategy      `json:"strategy,omitempty"`
	IgnoreDrainFailures   bool                `json:"ignoreDrainFailures,omitempty"`
	ForceRefresh          bool                `json:"forceRefresh,omitempty"`
	ReadinessGates        []NodeReadinessGate `json:"readinessGates,omitempty"`
}

// RollingUpgradeStatus defines the observed state of RollingUpgrade
type RollingUpgradeStatus struct {
	CurrentStatus           string                    `json:"currentStatus,omitempty"`
	StartTime               string                    `json:"startTime,omitempty"`
	EndTime                 string                    `json:"endTime,omitempty"`
	TotalProcessingTime     string                    `json:"totalProcessingTime,omitempty"`
	NodesProcessed          int                       `json:"nodesProcessed,omitempty"`
	TotalNodes              int                       `json:"totalNodes,omitempty"`
	Conditions              []RollingUpgradeCondition `json:"conditions,omitempty"`
	LastNodeTerminationTime metav1.Time               `json:"lastTerminationTime,omitempty"`
	LastNodeDrainTime       metav1.Time               `json:"lastDrainTime,omitempty"`

	Statistics        []*RollingUpgradeStatistics  `json:"statistics,omitempty"`
	InProcessingNodes map[string]*NodeInProcessing `json:"inProcessingNodes,omitempty"`
}

// RollingUpgrade Statistics, includes summary(sum/count) from each step
type RollingUpgradeStatistics struct {
	StepName      RollingUpgradeStep `json:"stepName,omitempty"`
	DurationSum   metav1.Duration    `json:"durationSum,omitempty"`
	DurationCount int32              `json:"durationCount,omitempty"`
}

// Node In-processing
type NodeInProcessing struct {
	NodeName         string             `json:"nodeName,omitempty"`
	StepName         RollingUpgradeStep `json:"stepName,omitempty"`
	UpgradeStartTime metav1.Time        `json:"upgradeStartTime,omitempty"`
	StepStartTime    metav1.Time        `json:"stepStartTime,omitempty"`
	StepEndTime      metav1.Time        `json:"stepEndTime,omitempty"`
}

// Add one step duration
func (s *RollingUpgradeStatus) addStepDuration(asgName string, stepName RollingUpgradeStep, duration time.Duration) {
	// if step exists, add count and sum, otherwise append
	for _, s := range s.Statistics {
		if s.StepName == stepName {
			s.DurationSum = metav1.Duration{
				Duration: s.DurationSum.Duration + duration,
			}
			s.DurationCount += 1
			return
		}
	}
	s.Statistics = append(s.Statistics, &RollingUpgradeStatistics{
		StepName: stepName,
		DurationSum: metav1.Duration{
			Duration: duration,
		},
		DurationCount: 1,
	})

	//Add to system level statistics
	common.AddRollingUpgradeStepDuration(asgName, string(stepName), duration)
}

// Node turns onto step
func (s *RollingUpgradeStatus) NodeStep(asgName string, nodeName string, stepName RollingUpgradeStep) {
	if s.InProcessingNodes == nil {
		s.InProcessingNodes = make(map[string]*NodeInProcessing)
	}
	var inProcessingNode *NodeInProcessing
	if n, ok := s.InProcessingNodes[nodeName]; !ok {
		inProcessingNode = &NodeInProcessing{
			NodeName:         nodeName,
			StepName:         stepName,
			UpgradeStartTime: metav1.Now(),
			StepStartTime:    metav1.Now(),
		}
		s.InProcessingNodes[nodeName] = inProcessingNode
	} else {
		inProcessingNode = n
		n.StepEndTime = metav1.Now()
		var duration = n.StepEndTime.Sub(n.StepStartTime.Time)
		if stepName == NodeRotationCompleted {
			//Add overall and remove the node from in-processing map
			var total = n.StepEndTime.Sub(n.UpgradeStartTime.Time)
			s.addStepDuration(asgName, inProcessingNode.StepName, duration)
			s.addStepDuration(asgName, NodeRotationTotal, total)
			delete(s.InProcessingNodes, nodeName)
		} else if inProcessingNode.StepName != stepName { //Still same step
			var oldOrder = NodeRotationStepOrders[inProcessingNode.StepName]
			var newOrder = NodeRotationStepOrders[stepName]
			if newOrder > oldOrder { //Make sure the steps running in order
				s.addStepDuration(asgName, inProcessingNode.StepName, duration)
				n.StepStartTime = metav1.Now()
				inProcessingNode.StepName = stepName
			}
		}
	}
}

func (s *RollingUpgradeStatus) SetCondition(cond RollingUpgradeCondition) {
	// if condition exists, overwrite, otherwise append
	for ix, c := range s.Conditions {
		if c.Type == cond.Type {
			s.Conditions[ix] = cond
			return
		}
	}
	s.Conditions = append(s.Conditions, cond)
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=rollingupgrades,scope=Namespaced,shortName=ru
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.currentStatus",description="current status of the rollingupgarde"
// +kubebuilder:printcolumn:name="TotalNodes",type="string",JSONPath=".status.totalNodes",description="total nodes involved in the rollingupgarde"
// +kubebuilder:printcolumn:name="NodesProcessed",type="string",JSONPath=".status.nodesProcessed",description="current number of nodes processed in the rollingupgarde"

// RollingUpgrade is the Schema for the rollingupgrades API
type RollingUpgrade struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RollingUpgradeSpec   `json:"spec,omitempty"`
	Status RollingUpgradeStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// RollingUpgradeList contains a list of RollingUpgrade
type RollingUpgradeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RollingUpgrade `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RollingUpgrade{}, &RollingUpgradeList{})
}

// PreDrainSpec contains the fields for actions taken before draining the node.
type PreDrainSpec struct {
	Script string `json:"script,omitempty"`
}

// PostDrainSpec contains the fields for actions taken after draining the node.
type PostDrainSpec struct {
	Script         string `json:"script,omitempty"`
	WaitSeconds    int64  `json:"waitSeconds,omitempty"`
	PostWaitScript string `json:"postWaitScript,omitempty"`
}

// PostTerminateSpec contains the fields for actions taken after terminating the node.
type PostTerminateSpec struct {
	Script string `json:"script,omitempty"`
}

type NodeReadinessGate struct {
	MatchLabels map[string]string `json:"matchLabels,omitempty" protobuf:"bytes,1,rep,name=matchLabels"`
}

type RollingUpgradeStep string

const (
	// Status
	StatusInit     = "init"
	StatusRunning  = "running"
	StatusComplete = "completed"
	StatusError    = "error"

	// Conditions
	UpgradeComplete UpgradeConditionType = "Complete"

	NodeRotationTotal RollingUpgradeStep = "total"

	NodeRotationKickoff          RollingUpgradeStep = "kickoff"
	NodeRotationDesiredNodeReady RollingUpgradeStep = "desired_node_ready"
	NodeRotationPredrainScript   RollingUpgradeStep = "predrain_script"
	NodeRotationDrain            RollingUpgradeStep = "drain"
	NodeRotationPostdrainScript  RollingUpgradeStep = "postdrain_script"
	NodeRotationPostWait         RollingUpgradeStep = "post_wait"
	NodeRotationTerminate        RollingUpgradeStep = "terminate"
	NodeRotationPostTerminate    RollingUpgradeStep = "post_terminate"
	NodeRotationCompleted        RollingUpgradeStep = "completed"
)

var NodeRotationStepOrders = map[RollingUpgradeStep]int{
	NodeRotationKickoff:          10,
	NodeRotationDesiredNodeReady: 20,
	NodeRotationPredrainScript:   30,
	NodeRotationDrain:            40,
	NodeRotationPostdrainScript:  50,
	NodeRotationPostWait:         60,
	NodeRotationTerminate:        70,
	NodeRotationPostTerminate:    80,
	NodeRotationCompleted:        1000,
}

var (
	FiniteStates        = []string{StatusComplete, StatusError}
	AllowedStrategyType = []string{string(RandomUpdateStrategy), string(UniformAcrossAzUpdateStrategy)}
	AllowedStrategyMode = []string{string(UpdateStrategyModeLazy), string(UpdateStrategyModeEager)}
)

// RollingUpgradeCondition describes the state of the RollingUpgrade
type RollingUpgradeCondition struct {
	Type   UpgradeConditionType   `json:"type,omitempty"`
	Status corev1.ConditionStatus `json:"status,omitempty"`
}

type UpdateStrategyType string
type UpdateStrategyMode string
type UpgradeConditionType string

const (
	RandomUpdateStrategy          UpdateStrategyType = "randomUpdate"
	UniformAcrossAzUpdateStrategy UpdateStrategyType = "uniformAcrossAzUpdate"

	UpdateStrategyModeLazy  UpdateStrategyMode = "lazy"
	UpdateStrategyModeEager UpdateStrategyMode = "eager"
)

// UpdateStrategy holds the information needed to perform update based on different update strategies
type UpdateStrategy struct {
	Type           UpdateStrategyType `json:"type,omitempty"`
	Mode           UpdateStrategyMode `json:"mode,omitempty"`
	MaxUnavailable intstr.IntOrString `json:"maxUnavailable,omitempty"`
	DrainTimeout   int                `json:"drainTimeout"`
}

func (c UpdateStrategyMode) String() string {
	return string(c)
}

// NamespacedName returns namespaced name of the object.
func (r *RollingUpgrade) NamespacedName() string {
	return fmt.Sprintf("%s/%s", r.Namespace, r.Name)
}

func (r *RollingUpgrade) ScalingGroupName() string {
	return r.Spec.AsgName
}

func (r *RollingUpgrade) DrainTimeout() int {
	return r.Spec.Strategy.DrainTimeout
}

func (r *RollingUpgrade) PostTerminateScript() string {
	return r.Spec.PostTerminate.Script
}

func (r *RollingUpgrade) PostWaitScript() string {
	return r.Spec.PostDrain.PostWaitScript
}

func (r *RollingUpgrade) PreDrainScript() string {
	return r.Spec.PreDrain.Script
}

func (r *RollingUpgrade) PostDrainScript() string {
	return r.Spec.PostDrain.Script
}

func (r *RollingUpgrade) CurrentStatus() string {
	return r.Status.CurrentStatus
}

func (r *RollingUpgrade) UpdateStrategyType() UpdateStrategyType {
	return r.Spec.Strategy.Type
}

func (r *RollingUpgrade) MaxUnavailable() intstr.IntOrString {
	return r.Spec.Strategy.MaxUnavailable
}

func (r *RollingUpgrade) LastNodeTerminationTime() metav1.Time {
	return r.Status.LastNodeTerminationTime
}

func (r *RollingUpgrade) SetLastNodeTerminationTime(t metav1.Time) {
	r.Status.LastNodeTerminationTime = t
}

func (r *RollingUpgrade) LastNodeDrainTime() metav1.Time {
	return r.Status.LastNodeDrainTime
}

func (r *RollingUpgrade) SetLastNodeDrainTime(t metav1.Time) {
	r.Status.LastNodeDrainTime = t
}

func (r *RollingUpgrade) NodeIntervalSeconds() int {
	return r.Spec.NodeIntervalSeconds
}

func (r *RollingUpgrade) PostDrainDelaySeconds() int {
	return r.Spec.PostDrainDelaySeconds
}

func (r *RollingUpgrade) SetCurrentStatus(status string) {
	r.Status.CurrentStatus = status
}

func (r *RollingUpgrade) SetStartTime(t string) {
	r.Status.StartTime = t
}

func (r *RollingUpgrade) StartTime() string {
	return r.Status.StartTime
}

func (r *RollingUpgrade) SetEndTime(t string) {
	r.Status.EndTime = t
}

func (r *RollingUpgrade) EndTime() string {
	return r.Status.EndTime
}

func (r *RollingUpgrade) SetTotalNodes(n int) {
	r.Status.TotalNodes = n
}

func (r *RollingUpgrade) SetNodesProcessed(n int) {
	r.Status.NodesProcessed = n
}

func (r *RollingUpgrade) GetStatus() RollingUpgradeStatus {
	return r.Status
}

func (r *RollingUpgrade) IsForceRefresh() bool {
	return r.Spec.ForceRefresh
}

func (r *RollingUpgrade) StrategyMode() UpdateStrategyMode {
	return r.Spec.Strategy.Mode
}

func (r *RollingUpgrade) Validate() (bool, error) {
	strategy := r.Spec.Strategy

	// validating the Type value
	if strategy.Type == "" {
		r.Spec.Strategy.Type = RandomUpdateStrategy
	} else if !common.ContainsEqualFold(AllowedStrategyType, string(strategy.Type)) {
		err := fmt.Errorf("%s: Invalid value for startegy Type - %d", r.Name, strategy.MaxUnavailable.IntVal)
		return false, err
	}

	// validating the Mode value
	if strategy.Mode == "" {
		r.Spec.Strategy.Mode = UpdateStrategyModeLazy
	} else if !common.ContainsEqualFold(AllowedStrategyMode, string(strategy.Mode)) {
		err := fmt.Errorf("%s: Invalid value for startegy Mode - %d", r.Name, strategy.MaxUnavailable.IntVal)
		return false, err
	}

	// validating the maxUnavailable value
	if strategy.MaxUnavailable.Type == intstr.Int && strategy.MaxUnavailable.IntVal == 0 {
		r.Spec.Strategy.MaxUnavailable.IntVal = 1
	} else if strategy.MaxUnavailable.Type == intstr.Int && strategy.MaxUnavailable.IntVal < 0 {
		err := fmt.Errorf("%s: Invalid value for startegy maxUnavailable - %d", r.Name, strategy.MaxUnavailable.IntVal)
		return false, err
	} else if strategy.MaxUnavailable.Type == intstr.String {
		intValue, _ := strconv.Atoi(strings.Trim(strategy.MaxUnavailable.StrVal, "%"))
		if intValue <= 0 || intValue > 100 {
			err := fmt.Errorf("%s: Invalid value for startegy maxUnavailable - %s", r.Name, strategy.MaxUnavailable.StrVal)
			return false, err
		}
	}

	// validating the DrainTimeout value
	if strategy.DrainTimeout == 0 {
		r.Spec.Strategy.DrainTimeout = -1
	} else if strategy.DrainTimeout < -1 {
		err := fmt.Errorf("%s: Invalid value for startegy DrainTimeout - %d", r.Name, strategy.MaxUnavailable.IntVal)
		return false, err
	}

	return true, nil
}
