package resources

import (
	"k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto copies all properties from this object into another object of the same type.
func (in *Workspace) DeepCopyInto(out *Workspace) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy creates a new instance of the object and copies data from the original.
func (in *Workspace) DeepCopy() *Workspace {
	if in == nil {
		return nil
	}
	out := new(Workspace)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a generically typed copy of an object.
func (in *Workspace) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies all properties from this object into another object of the same type.
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

// DeepCopy creates a new instance of the object and copies data from the original.
func (in *WorkspaceList) DeepCopy() *WorkspaceList {
	if in == nil {
		return nil
	}
	out := new(WorkspaceList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a generically typed copy of an object.
func (in *WorkspaceList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies all properties from this object into another object of the same type.
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

// DeepCopyInto copies all properties from this object into another object of the same type.
func (in *WorkspaceNetworkAccess) DeepCopyInto(out *WorkspaceNetworkAccess) {
	*out = *in
	if in.Egress != nil {
		in, out := &in.Egress, &out.Egress
		*out = make([]WorkspaceEgressRule, len(*in))
		copy(*out, *in)
	}
}

// DeepCopyInto copies all properties from this object into another object of the same type.
func (in *WorkspacePackageSet) DeepCopyInto(out *WorkspacePackageSet) {
	*out = *in
	if in.Requirements != nil {
		in, out := &in.Requirements, &out.Requirements
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopyInto copies all properties from this object into another object of the same type.
func (in *WorkspaceStatus) DeepCopyInto(out *WorkspaceStatus) {
	*out = *in
	if in.LastActivityAt != nil {
		in, out := &in.LastActivityAt, &out.LastActivityAt
		*out = (*in).DeepCopy()
	}
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]WorkspaceCondition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopyInto copies all properties from this object into another object of the same type.
func (in *WorkspaceCondition) DeepCopyInto(out *WorkspaceCondition) {
	*out = *in
	in.LastTransitionTime.DeepCopyInto(&out.LastTransitionTime)
}
