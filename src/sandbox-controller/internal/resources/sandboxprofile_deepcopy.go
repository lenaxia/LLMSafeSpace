package resources

import (
	"k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto copies all properties from this object into another object of the same type
func (in *SandboxProfile) DeepCopyInto(out *SandboxProfile) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
}

// DeepCopy creates a new instance of the object and copies data from the original
func (in *SandboxProfile) DeepCopy() *SandboxProfile {
	if in == nil {
		return nil
	}
	out := new(SandboxProfile)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a generically typed copy of an object
func (in *SandboxProfile) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies all properties from this object into another object of the same type
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

// DeepCopy creates a new instance of the object and copies data from the original
func (in *SandboxProfileList) DeepCopy() *SandboxProfileList {
	if in == nil {
		return nil
	}
	out := new(SandboxProfileList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a generically typed copy of an object
func (in *SandboxProfileList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies all properties from this object into another object of the same type
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

// DeepCopyInto copies all properties from this object into another object of the same type
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

// DeepCopyInto copies all properties from this object into another object of the same type
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

// DeepCopyInto copies all properties from this object into another object of the same type
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
