package v1

import (
	"k8s.io/apimachinery/pkg/runtime"
	
	"github.com/lenaxia/llmsafespace/api/internal/types"
)

// DeepCopyObject implements the runtime.Object interface.
func (in *types.Sandbox) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of Sandbox
func (in *types.Sandbox) DeepCopy() *types.Sandbox {
	if in == nil {
		return nil
	}
	out := new(types.Sandbox)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *types.Sandbox) DeepCopyInto(out *types.Sandbox) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopy().DeepCopyInto(&out.Spec)
	in.Status.DeepCopy().DeepCopyInto(&out.Status)
}

// DeepCopyInto copies all properties of SandboxSpec into another object of the same type
func (in *types.SandboxSpec) DeepCopyInto(out *types.SandboxSpec) {
	*out = *in
	if in.Resources != nil {
		in, out := &in.Resources, &out.Resources
		*out = new(types.ResourceRequirements)
		**out = **in
	}
	if in.NetworkAccess != nil {
		in, out := &in.NetworkAccess, &out.NetworkAccess
		*out = new(types.NetworkAccess)
		(*in).DeepCopyInto(*out)
	}
	if in.Filesystem != nil {
		in, out := &in.Filesystem, &out.Filesystem
		*out = new(types.FilesystemConfig)
		(*in).DeepCopyInto(*out)
	}
	if in.Storage != nil {
		in, out := &in.Storage, &out.Storage
		*out = new(types.StorageConfig)
		**out = **in
	}
	if in.SecurityContext != nil {
		in, out := &in.SecurityContext, &out.SecurityContext
		*out = new(types.SecurityContext)
		(*in).DeepCopyInto(*out)
	}
	if in.ProfileRef != nil {
		in, out := &in.ProfileRef, &out.ProfileRef
		*out = new(types.ProfileReference)
		**out = **in
	}
}

// DeepCopy creates a deep copy of SandboxSpec
func (in *types.SandboxSpec) DeepCopy() *types.SandboxSpec {
	if in == nil {
		return nil
	}
	out := new(types.SandboxSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of NetworkAccess into another object of the same type
func (in *types.NetworkAccess) DeepCopyInto(out *types.NetworkAccess) {
	*out = *in
	if in.Egress != nil {
		in, out := &in.Egress, &out.Egress
		*out = make([]types.EgressRule, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopyInto copies all properties of EgressRule into another object of the same type
func (in *types.EgressRule) DeepCopyInto(out *types.EgressRule) {
	*out = *in
	if in.Ports != nil {
		in, out := &in.Ports, &out.Ports
		*out = make([]types.PortRule, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopyInto copies all properties of PortRule into another object of the same type
func (in *types.PortRule) DeepCopyInto(out *types.PortRule) {
	*out = *in
}

// DeepCopyInto copies all properties of FilesystemConfig into another object of the same type
func (in *types.FilesystemConfig) DeepCopyInto(out *types.FilesystemConfig) {
	*out = *in
	if in.WritablePaths != nil {
		in, out := &in.WritablePaths, &out.WritablePaths
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopyInto copies all properties of SecurityContext into another object of the same type
func (in *types.SecurityContext) DeepCopyInto(out *types.SecurityContext) {
	*out = *in
	if in.SeccompProfile != nil {
		in, out := &in.SeccompProfile, &out.SeccompProfile
		*out = new(types.SeccompProfile)
		**out = **in
	}
}

// DeepCopyInto copies all properties of SandboxStatus into another object of the same type
func (in *types.SandboxStatus) DeepCopyInto(out *types.SandboxStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]types.SandboxCondition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.StartTime != nil {
		in, out := &in.StartTime, &out.StartTime
		*out = (*in).DeepCopy()
	}
	if in.Resources != nil {
		in, out := &in.Resources, &out.Resources
		*out = new(types.ResourceStatus)
		**out = **in
	}
	if in.WarmPodRef != nil {
		in, out := &in.WarmPodRef, &out.WarmPodRef
		*out = new(types.WarmPodReference)
		**out = **in
	}
}

// DeepCopy creates a deep copy of SandboxStatus
func (in *types.SandboxStatus) DeepCopy() *types.SandboxStatus {
	if in == nil {
		return nil
	}
	out := new(types.SandboxStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of SandboxCondition into another object of the same type
func (in *types.SandboxCondition) DeepCopyInto(out *types.SandboxCondition) {
	*out = *in
}

// DeepCopyObject implements the runtime.Object interface.
func (in *types.SandboxList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of SandboxList
func (in *types.SandboxList) DeepCopy() *types.SandboxList {
	if in == nil {
		return nil
	}
	out := new(types.SandboxList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *types.SandboxList) DeepCopyInto(out *types.SandboxList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]types.Sandbox, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// WarmPool DeepCopy implementations

// DeepCopyObject implements the runtime.Object interface.
func (in *types.WarmPool) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of WarmPool
func (in *types.WarmPool) DeepCopy() *types.WarmPool {
	if in == nil {
		return nil
	}
	out := new(types.WarmPool)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *types.WarmPool) DeepCopyInto(out *types.WarmPool) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopyInto copies all properties of WarmPoolSpec into another object of the same type
func (in *types.WarmPoolSpec) DeepCopyInto(out *types.WarmPoolSpec) {
	*out = *in
	if in.Resources != nil {
		in, out := &in.Resources, &out.Resources
		*out = new(types.ResourceRequirements)
		**out = **in
	}
	if in.ProfileRef != nil {
		in, out := &in.ProfileRef, &out.ProfileRef
		*out = new(types.ProfileReference)
		**out = **in
	}
	if in.PreloadPackages != nil {
		in, out := &in.PreloadPackages, &out.PreloadPackages
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.PreloadScripts != nil {
		in, out := &in.PreloadScripts, &out.PreloadScripts
		*out = make([]types.PreloadScript, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.AutoScaling != nil {
		in, out := &in.AutoScaling, &out.AutoScaling
		*out = new(types.AutoScalingConfig)
		**out = **in
	}
}

// DeepCopyInto copies all properties of PreloadScript into another object of the same type
func (in *types.PreloadScript) DeepCopyInto(out *types.PreloadScript) {
	*out = *in
}

// DeepCopyInto copies all properties of WarmPoolStatus into another object of the same type
func (in *types.WarmPoolStatus) DeepCopyInto(out *types.WarmPoolStatus) {
	*out = *in
	if in.LastScaleTime != nil {
		in, out := &in.LastScaleTime, &out.LastScaleTime
		*out = (*in).DeepCopy()
	}
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]types.WarmPoolCondition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopyInto copies all properties of WarmPoolCondition into another object of the same type
func (in *types.WarmPoolCondition) DeepCopyInto(out *types.WarmPoolCondition) {
	*out = *in
}

// DeepCopyObject implements the runtime.Object interface.
func (in *types.WarmPoolList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of WarmPoolList
func (in *types.WarmPoolList) DeepCopy() *types.WarmPoolList {
	if in == nil {
		return nil
	}
	out := new(types.WarmPoolList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *types.WarmPoolList) DeepCopyInto(out *types.WarmPoolList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]types.WarmPool, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// WarmPod DeepCopy implementations

// DeepCopyObject implements the runtime.Object interface.
func (in *types.WarmPod) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of WarmPod
func (in *types.WarmPod) DeepCopy() *types.WarmPod {
	if in == nil {
		return nil
	}
	out := new(types.WarmPod)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *types.WarmPod) DeepCopyInto(out *types.WarmPod) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopyInto copies all properties of WarmPodSpec into another object of the same type
func (in *types.WarmPodSpec) DeepCopyInto(out *types.WarmPodSpec) {
	*out = *in
	in.PoolRef.DeepCopyInto(&out.PoolRef)
	if in.CreationTimestamp != nil {
		in, out := &in.CreationTimestamp, &out.CreationTimestamp
		*out = (*in).DeepCopy()
	}
	if in.LastHeartbeat != nil {
		in, out := &in.LastHeartbeat, &out.LastHeartbeat
		*out = (*in).DeepCopy()
	}
}

// DeepCopyInto copies all properties of PoolReference into another object of the same type
func (in *types.PoolReference) DeepCopyInto(out *types.PoolReference) {
	*out = *in
}

// DeepCopyInto copies all properties of WarmPodStatus into another object of the same type
func (in *types.WarmPodStatus) DeepCopyInto(out *types.WarmPodStatus) {
	*out = *in
	if in.AssignedAt != nil {
		in, out := &in.AssignedAt, &out.AssignedAt
		*out = (*in).DeepCopy()
	}
}

// DeepCopyObject implements the runtime.Object interface.
func (in *types.WarmPodList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of WarmPodList
func (in *types.WarmPodList) DeepCopy() *types.WarmPodList {
	if in == nil {
		return nil
	}
	out := new(types.WarmPodList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *types.WarmPodList) DeepCopyInto(out *types.WarmPodList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]types.WarmPod, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// RuntimeEnvironment DeepCopy implementations

// DeepCopyObject implements the runtime.Object interface.
func (in *types.RuntimeEnvironment) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of RuntimeEnvironment
func (in *types.RuntimeEnvironment) DeepCopy() *types.RuntimeEnvironment {
	if in == nil {
		return nil
	}
	out := new(types.RuntimeEnvironment)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *types.RuntimeEnvironment) DeepCopyInto(out *types.RuntimeEnvironment) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopyInto copies all properties of RuntimeEnvironmentSpec into another object of the same type
func (in *types.RuntimeEnvironmentSpec) DeepCopyInto(out *types.RuntimeEnvironmentSpec) {
	*out = *in
	if in.Tags != nil {
		in, out := &in.Tags, &out.Tags
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.PreInstalledPackages != nil {
		in, out := &in.PreInstalledPackages, &out.PreInstalledPackages
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.SecurityFeatures != nil {
		in, out := &in.SecurityFeatures, &out.SecurityFeatures
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.ResourceRequirements != nil {
		in, out := &in.ResourceRequirements, &out.ResourceRequirements
		*out = new(types.RuntimeResourceRequirements)
		**out = **in
	}
}

// DeepCopyInto copies all properties of RuntimeEnvironmentStatus into another object of the same type
func (in *types.RuntimeEnvironmentStatus) DeepCopyInto(out *types.RuntimeEnvironmentStatus) {
	*out = *in
	if in.LastValidated != nil {
		in, out := &in.LastValidated, &out.LastValidated
		*out = (*in).DeepCopy()
	}
}

// DeepCopyObject implements the runtime.Object interface.
func (in *types.RuntimeEnvironmentList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of RuntimeEnvironmentList
func (in *types.RuntimeEnvironmentList) DeepCopy() *types.RuntimeEnvironmentList {
	if in == nil {
		return nil
	}
	out := new(types.RuntimeEnvironmentList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *types.RuntimeEnvironmentList) DeepCopyInto(out *types.RuntimeEnvironmentList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]types.RuntimeEnvironment, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// SandboxProfile DeepCopy implementations

// DeepCopyObject implements the runtime.Object interface.
func (in *types.SandboxProfile) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of SandboxProfile
func (in *types.SandboxProfile) DeepCopy() *types.SandboxProfile {
	if in == nil {
		return nil
	}
	out := new(types.SandboxProfile)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *types.SandboxProfile) DeepCopyInto(out *types.SandboxProfile) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
}

// DeepCopyInto copies all properties of SandboxProfileSpec into another object of the same type
func (in *types.SandboxProfileSpec) DeepCopyInto(out *types.SandboxProfileSpec) {
	*out = *in
	if in.NetworkPolicies != nil {
		in, out := &in.NetworkPolicies, &out.NetworkPolicies
		*out = make([]types.NetworkPolicy, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.PreInstalledPackages != nil {
		in, out := &in.PreInstalledPackages, &out.PreInstalledPackages
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.ResourceDefaults != nil {
		in, out := &in.ResourceDefaults, &out.ResourceDefaults
		*out = new(types.ResourceDefaults)
		**out = **in
	}
	if in.FilesystemConfig != nil {
		in, out := &in.FilesystemConfig, &out.FilesystemConfig
		*out = new(types.ProfileFilesystemConfig)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopyInto copies all properties of NetworkPolicy into another object of the same type
func (in *types.NetworkPolicy) DeepCopyInto(out *types.NetworkPolicy) {
	*out = *in
	if in.Rules != nil {
		in, out := &in.Rules, &out.Rules
		*out = make([]types.NetworkRule, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopyInto copies all properties of NetworkRule into another object of the same type
func (in *types.NetworkRule) DeepCopyInto(out *types.NetworkRule) {
	*out = *in
	if in.Ports != nil {
		in, out := &in.Ports, &out.Ports
		*out = make([]types.PortRule, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopyInto copies all properties of ProfileFilesystemConfig into another object of the same type
func (in *types.ProfileFilesystemConfig) DeepCopyInto(out *types.ProfileFilesystemConfig) {
	*out = *in
	if in.ReadOnlyPaths != nil {
		in, out := &in.ReadOnlyPaths, &out.ReadOnlyPaths
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.WritablePaths != nil {
		in, out := &in.WritablePaths, &out.WritablePaths
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopyObject implements the runtime.Object interface.
func (in *types.SandboxProfileList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of SandboxProfileList
func (in *types.SandboxProfileList) DeepCopy() *types.SandboxProfileList {
	if in == nil {
		return nil
	}
	out := new(types.SandboxProfileList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *types.SandboxProfileList) DeepCopyInto(out *types.SandboxProfileList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]types.SandboxProfile, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}
