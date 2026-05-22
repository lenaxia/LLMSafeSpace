package v1

import (
	"k8s.io/apimachinery/pkg/runtime"
)

func (in *Sandbox) DeepCopyInto(out *Sandbox) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *Sandbox) DeepCopy() *Sandbox {
	if in == nil {
		return nil
	}
	out := new(Sandbox)
	in.DeepCopyInto(out)
	return out
}

func (in *Sandbox) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

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

func (in *SandboxList) DeepCopy() *SandboxList {
	if in == nil {
		return nil
	}
	out := new(SandboxList)
	in.DeepCopyInto(out)
	return out
}

func (in *SandboxList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

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
	if in.SecurityCtx != nil {
		in, out := &in.SecurityCtx, &out.SecurityCtx
		*out = new(SecurityContext)
		(*in).DeepCopyInto(*out)
	}
	if in.ProfileRef != nil {
		in, out := &in.ProfileRef, &out.ProfileRef
		*out = new(ProfileReference)
		**out = **in
	}
}

func (in *SandboxStatus) DeepCopyInto(out *SandboxStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]SandboxCondition, len(*in))
		copy(*out, *in)
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
	if in.LastActivityAt != nil {
		in, out := &in.LastActivityAt, &out.LastActivityAt
		*out = (*in).DeepCopy()
	}
}

func (in *SandboxCondition) DeepCopyInto(out *SandboxCondition) {
	*out = *in
}

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

func (in *EgressRule) DeepCopyInto(out *EgressRule) {
	*out = *in
	if in.Ports != nil {
		in, out := &in.Ports, &out.Ports
		*out = make([]PortRule, len(*in))
		copy(*out, *in)
	}
}

func (in *FilesystemConfig) DeepCopyInto(out *FilesystemConfig) {
	*out = *in
	if in.WritablePaths != nil {
		in, out := &in.WritablePaths, &out.WritablePaths
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

func (in *SecurityContext) DeepCopyInto(out *SecurityContext) {
	*out = *in
	if in.SeccompProfile != nil {
		in, out := &in.SeccompProfile, &out.SeccompProfile
		*out = new(SeccompProfile)
		**out = **in
	}
}

func (in *SandboxProfile) DeepCopyInto(out *SandboxProfile) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
}

func (in *SandboxProfile) DeepCopy() *SandboxProfile {
	if in == nil {
		return nil
	}
	out := new(SandboxProfile)
	in.DeepCopyInto(out)
	return out
}

func (in *SandboxProfile) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *SandboxProfileSpec) DeepCopyInto(out *SandboxProfileSpec) {
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
	if in.SecurityCtx != nil {
		in, out := &in.SecurityCtx, &out.SecurityCtx
		*out = new(SecurityContext)
		(*in).DeepCopyInto(*out)
	}
}

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

func (in *SandboxProfileList) DeepCopy() *SandboxProfileList {
	if in == nil {
		return nil
	}
	out := new(SandboxProfileList)
	in.DeepCopyInto(out)
	return out
}

func (in *SandboxProfileList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *RuntimeEnvironment) DeepCopyInto(out *RuntimeEnvironment) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *RuntimeEnvironment) DeepCopy() *RuntimeEnvironment {
	if in == nil {
		return nil
	}
	out := new(RuntimeEnvironment)
	in.DeepCopyInto(out)
	return out
}

func (in *RuntimeEnvironment) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *RuntimeEnvironmentSpec) DeepCopyInto(out *RuntimeEnvironmentSpec) {
	*out = *in
	if in.Packages != nil {
		in, out := &in.Packages, &out.Packages
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

func (in *RuntimeEnvironmentStatus) DeepCopyInto(out *RuntimeEnvironmentStatus) {
	*out = *in
	if in.LastUpdateTime != nil {
		in, out := &in.LastUpdateTime, &out.LastUpdateTime
		*out = (*in).DeepCopy()
	}
}

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

func (in *RuntimeEnvironmentList) DeepCopy() *RuntimeEnvironmentList {
	if in == nil {
		return nil
	}
	out := new(RuntimeEnvironmentList)
	in.DeepCopyInto(out)
	return out
}

func (in *RuntimeEnvironmentList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *WorkspaceSpec) DeepCopyInto(out *WorkspaceSpec) {
	*out = *in
	out.Owner = in.Owner
	out.Storage = in.Storage
	if in.NetworkAccess != nil {
		in, out := &in.NetworkAccess, &out.NetworkAccess
		*out = new(WorkspaceNetworkAccess)
		(*in).DeepCopyInto(*out)
	}
	if in.AutoSuspend != nil {
		in, out := &in.AutoSuspend, &out.AutoSuspend
		*out = new(WorkspaceAutoSuspend)
		**out = **in
	}
	if in.Packages != nil {
		in, out := &in.Packages, &out.Packages
		*out = make([]WorkspacePackageSet, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.Credentials != nil {
		in, out := &in.Credentials, &out.Credentials
		*out = new(WorkspaceCredentialRef)
		**out = **in
	}
}

func (in *WorkspaceNetworkAccess) DeepCopyInto(out *WorkspaceNetworkAccess) {
	*out = *in
	if in.Egress != nil {
		in, out := &in.Egress, &out.Egress
		*out = make([]WorkspaceEgressRule, len(*in))
		copy(*out, *in)
	}
}

func (in *WorkspacePackageSet) DeepCopyInto(out *WorkspacePackageSet) {
	*out = *in
	if in.Requirements != nil {
		in, out := &in.Requirements, &out.Requirements
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

func (in *WorkspaceStatus) DeepCopyInto(out *WorkspaceStatus) {
	*out = *in
	if in.LastActivityAt != nil {
		in, out := &in.LastActivityAt, &out.LastActivityAt
		*out = (*in).DeepCopy()
	}
	if in.SuspendedAt != nil {
		in, out := &in.SuspendedAt, &out.SuspendedAt
		*out = (*in).DeepCopy()
	}
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]WorkspaceCondition, len(*in))
		copy(*out, *in)
	}
}

func (in *Workspace) DeepCopyInto(out *Workspace) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *Workspace) DeepCopy() *Workspace {
	if in == nil {
		return nil
	}
	out := new(Workspace)
	in.DeepCopyInto(out)
	return out
}

func (in *Workspace) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *WorkspaceList) DeepCopyInto(out *WorkspaceList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]Workspace, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

func (in *WorkspaceList) DeepCopy() *WorkspaceList {
	if in == nil {
		return nil
	}
	out := new(WorkspaceList)
	in.DeepCopyInto(out)
	return out
}

func (in *WorkspaceList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}
