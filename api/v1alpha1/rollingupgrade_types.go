/*

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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

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

// RollingUpgradeSpec defines the desired state of RollingUpgrade
type RollingUpgradeSpec struct {
	PostDrainDelaySeconds int `json:"postDrainDelaySeconds,omitempty"`
	NodeIntervalSeconds   int `json:"nodeIntervalSeconds,omitempty"`
	// AsgName is AWS Autoscaling Group name to roll.
	AsgName       string            `json:"asgName,omitempty"`
	PreDrain      PreDrainSpec      `json:"preDrain,omitempty"`
	PostDrain     PostDrainSpec     `json:"postDrain,omitempty"`
	PostTerminate PostTerminateSpec `json:"postTerminate,omitempty"`
	Strategy      UpdateStrategy    `json:"strategy,omitempty"`
	// IgnoreDrainFailures allows ignoring node drain failures and proceed with rolling upgrade.
	IgnoreDrainFailures bool `json:"ignoreDrainFailures,omitempty"`
	// ForceRefresh enables draining and terminating the node even if the launch config/template hasn't changed.
	ForceRefresh bool `json:"forceRefresh,omitempty"`
	// ReadinessGates allow to specify label selectors that node must match to be considered ready.
	ReadinessGates []NodeReadinessGate `json:"readinessGates,omitempty"`
}

type NodeReadinessGate struct {
	MatchLabels map[string]string `json:"matchLabels,omitempty" protobuf:"bytes,1,rep,name=matchLabels"`
}

// RollingUpgradeStatus defines the observed state of RollingUpgrade
type RollingUpgradeStatus struct {
	CurrentStatus       string `json:"currentStatus,omitempty"`
	StartTime           string `json:"startTime,omitempty"`
	EndTime             string `json:"endTime,omitempty"`
	TotalProcessingTime string `json:"totalProcessingTime,omitempty"`
	NodesProcessed      int    `json:"nodesProcessed,omitempty"`
	TotalNodes          int    `json:"totalNodes,omitempty"`

	Conditions []RollingUpgradeCondition `json:"conditions,omitempty"`
}

const (
	// StatusRunning marks the CR to be running.
	StatusRunning = "running"
	// StatusComplete marks the CR as completed.
	StatusComplete = "completed"
	// StatusError marks the CR as errored out.
	StatusError = "error"
)

// RollingUpgradeCondition describes the state of the RollingUpgrade
type RollingUpgradeCondition struct {
	Type   UpgradeConditionType   `json:"type,omitempty"`
	Status corev1.ConditionStatus `json:"status,omitempty"`
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

// +kubebuilder:object:root=true

// RollingUpgradeList contains a list of RollingUpgrade
type RollingUpgradeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RollingUpgrade `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RollingUpgrade{}, &RollingUpgradeList{})
}

// UpdateStrategyType indicates how the update has to be rolled out
// whether to roll the update AZ wise or all Azs at once
type UpdateStrategyType string

type UpdateStrategyMode string

type UpgradeConditionType string

const (
	// RandomUpdate strategy treats all the availability zones as a single unit and picks random nodes for update.
	RandomUpdateStrategy UpdateStrategyType = "randomUpdate"

	// UniformAcrossAzUpdateStrategy Picks same number of nodes or same percentage of nodes from each AZ for update.
	UniformAcrossAzUpdateStrategy UpdateStrategyType = "uniformAcrossAzUpdate"

	UpdateStrategyModeLazy  UpdateStrategyMode = "lazy"
	UpdateStrategyModeEager UpdateStrategyMode = "eager"

	// Other update strategies such as rolling update by AZ or rolling update with a pre-defined instance list
	// can be implemented in future by adding more update strategy types

	UpgradeComplete UpgradeConditionType = "Complete"
)

func (c UpdateStrategyMode) String() string {
	return string(c)
}

// NamespacedName returns namespaced name of the object.
func (r RollingUpgrade) NamespacedName() string {
	return fmt.Sprintf("%s/%s", r.Namespace, r.Name)
}

// UpdateStrategy holds the information needed to perform update based on different update strategies
type UpdateStrategy struct {
	Type UpdateStrategyType `json:"type,omitempty"`
	Mode UpdateStrategyMode `json:"mode,omitempty"`
	// MaxUnavailable can be specified as number of nodes or the percent of total number of nodes
	MaxUnavailable intstr.IntOrString `json:"maxUnavailable,omitempty"`
	// Node will be terminated after drain timeout even if `kubectl drain` has not been completed
	// and value has to be specified in seconds
	DrainTimeout int `json:"drainTimeout"`
}
