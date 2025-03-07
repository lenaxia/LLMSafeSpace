package v1

import (
	"k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyObject implements the runtime.Object interface.
func (in *Sandbox) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of Sandbox
func (in *Sandbox) DeepCopy() *Sandbox {
	if in == nil {
		return nil
	}
	out := new(Sandbox)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *Sandbox) DeepCopyInto(out *Sandbox) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopy().DeepCopyInto(&out.Spec)
	in.Status.DeepCopy().DeepCopyInto(&out.Status)
}

// DeepCopyInto copies all properties of SandboxSpec into another object of the same type
func (in *SandboxSpec) DeepCopyInto(out *SandboxSpec) {
	*out = *in
	if in.Resources != nil {
		in, out := &in.Resources, &out.Resources
		*out = new(ResourceRequirements)
		**out = **in
	}
	if in.NetworkAccess != nil {
		in, out := &in.NetworkAccess, &out.NetworkAccess
		*out = new(NetworkAccess)
		(*in).DeepCopyInto(*out)
	}
	if in.Filesystem != nil {
		in, out := &in.Filesystem, &out.Filesystem
		*out = new(FilesystemConfig)
		(*in).DeepCopyInto(*out)
	}
	if in.Storage != nil {
		in, out := &in.Storage, &out.Storage
		*out = new(StorageConfig)
		**out = **in
	}
	if in.SecurityContext != nil {
		in, out := &in.SecurityContext, &out.SecurityContext
		*out = new(SecurityContext)
		(*in).DeepCopyInto(*out)
	}
	if in.ProfileRef != nil {
		in, out := &in.ProfileRef, &out.ProfileRef
		*out = new(ProfileReference)
		**out = **in
	}
}

// DeepCopy creates a deep copy of SandboxSpec
func (in *SandboxSpec) DeepCopy() *SandboxSpec {
	if in == nil {
		return nil
	}
	out := new(SandboxSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of NetworkAccess into another object of the same type
func (in *NetworkAccess) DeepCopyInto(out *NetworkAccess) {
	*out = *in
	if in.Egress != nil {
		in, out := &in.Egress, &out.Egress
		*out = make([]EgressRule, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopyInto copies all properties of EgressRule into another object of the same type
func (in *EgressRule) DeepCopyInto(out *EgressRule) {
	*out = *in
	if in.Ports != nil {
		in, out := &in.Ports, &out.Ports
		*out = make([]PortRule, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopyInto copies all properties of PortRule into another object of the same type
func (in *PortRule) DeepCopyInto(out *PortRule) {
	*out = *in
}

// DeepCopyInto copies all properties of FilesystemConfig into another object of the same type
func (in *FilesystemConfig) DeepCopyInto(out *FilesystemConfig) {
	*out = *in
	if in.WritablePaths != nil {
		in, out := &in.WritablePaths, &out.WritablePaths
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopyInto copies all properties of SecurityContext into another object of the same type
func (in *SecurityContext) DeepCopyInto(out *SecurityContext) {
	*out = *in
	if in.SeccompProfile != nil {
		in, out := &in.SeccompProfile, &out.SeccompProfile
		*out = new(SeccompProfile)
		**out = **in
	}
}

// DeepCopyInto copies all properties of SandboxStatus into another object of the same type
func (in *SandboxStatus) DeepCopyInto(out *SandboxStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]SandboxCondition, len(*in))
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
		*out = new(ResourceStatus)
		**out = **in
	}
	if in.WarmPodRef != nil {
		in, out := &in.WarmPodRef, &out.WarmPodRef
		*out = new(WarmPodReference)
		**out = **in
	}
}

// DeepCopy creates a deep copy of SandboxStatus
func (in *SandboxStatus) DeepCopy() *SandboxStatus {
	if in == nil {
		return nil
	}
	out := new(SandboxStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of SandboxCondition into another object of the same type
func (in *SandboxCondition) DeepCopyInto(out *SandboxCondition) {
	*out = *in
}

// DeepCopyObject implements the runtime.Object interface.
func (in *SandboxList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of SandboxList
func (in *SandboxList) DeepCopy() *SandboxList {
	if in == nil {
		return nil
	}
	out := new(SandboxList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *SandboxList) DeepCopyInto(out *SandboxList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]Sandbox, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// WarmPool DeepCopy implementations

// DeepCopyObject implements the runtime.Object interface.
func (in *WarmPool) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of WarmPool
func (in *WarmPool) DeepCopy() *WarmPool {
	if in == nil {
		return nil
	}
	out := new(WarmPool)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *WarmPool) DeepCopyInto(out *WarmPool) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopyInto copies all properties of WarmPoolSpec into another object of the same type
func (in *WarmPoolSpec) DeepCopyInto(out *WarmPoolSpec) {
	*out = *in
	if in.Resources != nil {
		in, out := &in.Resources, &out.Resources
		*out = new(ResourceRequirements)
		**out = **in
	}
	if in.ProfileRef != nil {
		in, out := &in.ProfileRef, &out.ProfileRef
		*out = new(ProfileReference)
		**out = **in
	}
	if in.PreloadPackages != nil {
		in, out := &in.PreloadPackages, &out.PreloadPackages
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.PreloadScripts != nil {
		in, out := &in.PreloadScripts, &out.PreloadScripts
		*out = make([]PreloadScript, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.AutoScaling != nil {
		in, out := &in.AutoScaling, &out.AutoScaling
		*out = new(AutoScalingConfig)
		**out = **in
	}
}

// DeepCopyInto copies all properties of PreloadScript into another object of the same type
func (in *PreloadScript) DeepCopyInto(out *PreloadScript) {
	*out = *in
}

// DeepCopyInto copies all properties of WarmPoolStatus into another object of the same type
func (in *WarmPoolStatus) DeepCopyInto(out *WarmPoolStatus) {
	*out = *in
	if in.LastScaleTime != nil {
		in, out := &in.LastScaleTime, &out.LastScaleTime
		*out = (*in).DeepCopy()
	}
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]WarmPoolCondition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopyInto copies all properties of WarmPoolCondition into another object of the same type
func (in *WarmPoolCondition) DeepCopyInto(out *WarmPoolCondition) {
	*out = *in
}

// DeepCopyObject implements the runtime.Object interface.
func (in *WarmPoolList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of WarmPoolList
func (in *WarmPoolList) DeepCopy() *WarmPoolList {
	if in == nil {
		return nil
	}
	out := new(WarmPoolList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *WarmPoolList) DeepCopyInto(out *WarmPoolList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]WarmPool, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// WarmPod DeepCopy implementations

// DeepCopyObject implements the runtime.Object interface.
func (in *WarmPod) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of WarmPod
func (in *WarmPod) DeepCopy() *WarmPod {
	if in == nil {
		return nil
	}
	out := new(WarmPod)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *WarmPod) DeepCopyInto(out *WarmPod) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopyInto copies all properties of WarmPodSpec into another object of the same type
func (in *WarmPodSpec) DeepCopyInto(out *WarmPodSpec) {
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
func (in *PoolReference) DeepCopyInto(out *PoolReference) {
	*out = *in
}

// DeepCopyInto copies all properties of WarmPodStatus into another object of the same type
func (in *WarmPodStatus) DeepCopyInto(out *WarmPodStatus) {
	*out = *in
	if in.AssignedAt != nil {
		in, out := &in.AssignedAt, &out.AssignedAt
		*out = (*in).DeepCopy()
	}
}

// DeepCopyObject implements the runtime.Object interface.
func (in *WarmPodList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of WarmPodList
func (in *WarmPodList) DeepCopy() *WarmPodList {
	if in == nil {
		return nil
	}
	out := new(WarmPodList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *WarmPodList) DeepCopyInto(out *WarmPodList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]WarmPod, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// RuntimeEnvironment DeepCopy implementations

// DeepCopyObject implements the runtime.Object interface.
func (in *RuntimeEnvironment) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of RuntimeEnvironment
func (in *RuntimeEnvironment) DeepCopy() *RuntimeEnvironment {
	if in == nil {
		return nil
	}
	out := new(RuntimeEnvironment)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *RuntimeEnvironment) DeepCopyInto(out *RuntimeEnvironment) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopyInto copies all properties of RuntimeEnvironmentSpec into another object of the same type
func (in *RuntimeEnvironmentSpec) DeepCopyInto(out *RuntimeEnvironmentSpec) {
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
		*out = new(RuntimeResourceRequirements)
		**out = **in
	}
}

// DeepCopyInto copies all properties of RuntimeEnvironmentStatus into another object of the same type
func (in *RuntimeEnvironmentStatus) DeepCopyInto(out *RuntimeEnvironmentStatus) {
	*out = *in
	if in.LastValidated != nil {
		in, out := &in.LastValidated, &out.LastValidated
		*out = (*in).DeepCopy()
	}
}

// DeepCopyObject implements the runtime.Object interface.
func (in *RuntimeEnvironmentList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of RuntimeEnvironmentList
func (in *RuntimeEnvironmentList) DeepCopy() *RuntimeEnvironmentList {
	if in == nil {
		return nil
	}
	out := new(RuntimeEnvironmentList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *RuntimeEnvironmentList) DeepCopyInto(out *RuntimeEnvironmentList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]RuntimeEnvironment, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// SandboxProfile DeepCopy implementations

// DeepCopyObject implements the runtime.Object interface.
func (in *SandboxProfile) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of SandboxProfile
func (in *SandboxProfile) DeepCopy() *SandboxProfile {
	if in == nil {
		return nil
	}
	out := new(SandboxProfile)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *SandboxProfile) DeepCopyInto(out *SandboxProfile) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
}

// DeepCopyInto copies all properties of SandboxProfileSpec into another object of the same type
func (in *SandboxProfileSpec) DeepCopyInto(out *SandboxProfileSpec) {
	*out = *in
	if in.NetworkPolicies != nil {
		in, out := &in.NetworkPolicies, &out.NetworkPolicies
		*out = make([]NetworkPolicy, len(*in))
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
		*out = new(ResourceDefaults)
		**out = **in
	}
	if in.FilesystemConfig != nil {
		in, out := &in.FilesystemConfig, &out.FilesystemConfig
		*out = new(ProfileFilesystemConfig)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopyInto copies all properties of NetworkPolicy into another object of the same type
func (in *NetworkPolicy) DeepCopyInto(out *NetworkPolicy) {
	*out = *in
	if in.Rules != nil {
		in, out := &in.Rules, &out.Rules
		*out = make([]NetworkRule, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopyInto copies all properties of NetworkRule into another object of the same type
func (in *NetworkRule) DeepCopyInto(out *NetworkRule) {
	*out = *in
	if in.Ports != nil {
		in, out := &in.Ports, &out.Ports
		*out = make([]PortRule, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopyInto copies all properties of ProfileFilesystemConfig into another object of the same type
func (in *ProfileFilesystemConfig) DeepCopyInto(out *ProfileFilesystemConfig) {
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
func (in *SandboxProfileList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of SandboxProfileList
func (in *SandboxProfileList) DeepCopy() *SandboxProfileList {
	if in == nil {
		return nil
	}
	out := new(SandboxProfileList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all properties of this object into another object of the same type
func (in *SandboxProfileList) DeepCopyInto(out *SandboxProfileList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]SandboxProfile, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}
