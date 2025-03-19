package resources

import (
	"k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto copies all properties from this object into another object of the same type
func (in *RuntimeEnvironment) DeepCopyInto(out *RuntimeEnvironment) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy creates a new instance of the object and copies data from the original
func (in *RuntimeEnvironment) DeepCopy() *RuntimeEnvironment {
	if in == nil {
		return nil
	}
	out := new(RuntimeEnvironment)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a generically typed copy of an object
func (in *RuntimeEnvironment) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies all properties from this object into another object of the same type
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

// DeepCopy creates a new instance of the object and copies data from the original
func (in *RuntimeEnvironmentList) DeepCopy() *RuntimeEnvironmentList {
	if in == nil {
		return nil
	}
	out := new(RuntimeEnvironmentList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a generically typed copy of an object
func (in *RuntimeEnvironmentList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies all properties from this object into another object of the same type
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

// DeepCopyInto copies all properties from this object into another object of the same type
func (in *RuntimeEnvironmentStatus) DeepCopyInto(out *RuntimeEnvironmentStatus) {
	*out = *in
	if in.LastValidated != nil {
		in, out := &in.LastValidated, &out.LastValidated
		*out = (*in).DeepCopy()
	}
}
