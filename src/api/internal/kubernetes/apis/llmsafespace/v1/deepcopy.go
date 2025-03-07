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
