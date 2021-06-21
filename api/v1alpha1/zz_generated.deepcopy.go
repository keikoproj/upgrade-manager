// +build !ignore_autogenerated

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

// Code generated by controller-gen. DO NOT EDIT.

package v1alpha1

import (
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NodeInProcessing) DeepCopyInto(out *NodeInProcessing) {
	*out = *in
	in.UpgradeStartTime.DeepCopyInto(&out.UpgradeStartTime)
	in.StepStartTime.DeepCopyInto(&out.StepStartTime)
	in.StepEndTime.DeepCopyInto(&out.StepEndTime)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NodeInProcessing.
func (in *NodeInProcessing) DeepCopy() *NodeInProcessing {
	if in == nil {
		return nil
	}
	out := new(NodeInProcessing)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NodeReadinessGate) DeepCopyInto(out *NodeReadinessGate) {
	*out = *in
	if in.MatchLabels != nil {
		in, out := &in.MatchLabels, &out.MatchLabels
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NodeReadinessGate.
func (in *NodeReadinessGate) DeepCopy() *NodeReadinessGate {
	if in == nil {
		return nil
	}
	out := new(NodeReadinessGate)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NodeStepDuration) DeepCopyInto(out *NodeStepDuration) {
	*out = *in
	out.Duration = in.Duration
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NodeStepDuration.
func (in *NodeStepDuration) DeepCopy() *NodeStepDuration {
	if in == nil {
		return nil
	}
	out := new(NodeStepDuration)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PostDrainSpec) DeepCopyInto(out *PostDrainSpec) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PostDrainSpec.
func (in *PostDrainSpec) DeepCopy() *PostDrainSpec {
	if in == nil {
		return nil
	}
	out := new(PostDrainSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PostTerminateSpec) DeepCopyInto(out *PostTerminateSpec) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PostTerminateSpec.
func (in *PostTerminateSpec) DeepCopy() *PostTerminateSpec {
	if in == nil {
		return nil
	}
	out := new(PostTerminateSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PreDrainSpec) DeepCopyInto(out *PreDrainSpec) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PreDrainSpec.
func (in *PreDrainSpec) DeepCopy() *PreDrainSpec {
	if in == nil {
		return nil
	}
	out := new(PreDrainSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RollingUpgrade) DeepCopyInto(out *RollingUpgrade) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RollingUpgrade.
func (in *RollingUpgrade) DeepCopy() *RollingUpgrade {
	if in == nil {
		return nil
	}
	out := new(RollingUpgrade)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *RollingUpgrade) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RollingUpgradeCondition) DeepCopyInto(out *RollingUpgradeCondition) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RollingUpgradeCondition.
func (in *RollingUpgradeCondition) DeepCopy() *RollingUpgradeCondition {
	if in == nil {
		return nil
	}
	out := new(RollingUpgradeCondition)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RollingUpgradeList) DeepCopyInto(out *RollingUpgradeList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]RollingUpgrade, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RollingUpgradeList.
func (in *RollingUpgradeList) DeepCopy() *RollingUpgradeList {
	if in == nil {
		return nil
	}
	out := new(RollingUpgradeList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *RollingUpgradeList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RollingUpgradeSpec) DeepCopyInto(out *RollingUpgradeSpec) {
	*out = *in
	out.PreDrain = in.PreDrain
	out.PostDrain = in.PostDrain
	out.PostTerminate = in.PostTerminate
	out.Strategy = in.Strategy
	if in.ReadinessGates != nil {
		in, out := &in.ReadinessGates, &out.ReadinessGates
		*out = make([]NodeReadinessGate, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RollingUpgradeSpec.
func (in *RollingUpgradeSpec) DeepCopy() *RollingUpgradeSpec {
	if in == nil {
		return nil
	}
	out := new(RollingUpgradeSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RollingUpgradeStatistics) DeepCopyInto(out *RollingUpgradeStatistics) {
	*out = *in
	out.DurationSum = in.DurationSum
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RollingUpgradeStatistics.
func (in *RollingUpgradeStatistics) DeepCopy() *RollingUpgradeStatistics {
	if in == nil {
		return nil
	}
	out := new(RollingUpgradeStatistics)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RollingUpgradeStatus) DeepCopyInto(out *RollingUpgradeStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]RollingUpgradeCondition, len(*in))
		copy(*out, *in)
	}
	if in.LastNodeTerminationTime != nil {
		in, out := &in.LastNodeTerminationTime, &out.LastNodeTerminationTime
		*out = (*in).DeepCopy()
	}
	if in.LastNodeDrainTime != nil {
		in, out := &in.LastNodeDrainTime, &out.LastNodeDrainTime
		*out = (*in).DeepCopy()
	}
	if in.Statistics != nil {
		in, out := &in.Statistics, &out.Statistics
		*out = make([]*RollingUpgradeStatistics, len(*in))
		for i := range *in {
			if (*in)[i] != nil {
				in, out := &(*in)[i], &(*out)[i]
				*out = new(RollingUpgradeStatistics)
				**out = **in
			}
		}
	}
	if in.LastBatchNodes != nil {
		in, out := &in.LastBatchNodes, &out.LastBatchNodes
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.NodeInProcessing != nil {
		in, out := &in.NodeInProcessing, &out.NodeInProcessing
		*out = make(map[string]*NodeInProcessing, len(*in))
		for key, val := range *in {
			var outVal *NodeInProcessing
			if val == nil {
				(*out)[key] = nil
			} else {
				in, out := &val, &outVal
				*out = new(NodeInProcessing)
				(*in).DeepCopyInto(*out)
			}
			(*out)[key] = outVal
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RollingUpgradeStatus.
func (in *RollingUpgradeStatus) DeepCopy() *RollingUpgradeStatus {
	if in == nil {
		return nil
	}
	out := new(RollingUpgradeStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UpdateStrategy) DeepCopyInto(out *UpdateStrategy) {
	*out = *in
	out.MaxUnavailable = in.MaxUnavailable
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UpdateStrategy.
func (in *UpdateStrategy) DeepCopy() *UpdateStrategy {
	if in == nil {
		return nil
	}
	out := new(UpdateStrategy)
	in.DeepCopyInto(out)
	return out
}
