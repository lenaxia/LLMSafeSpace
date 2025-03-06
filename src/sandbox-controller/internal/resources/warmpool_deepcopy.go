package resources

import (
	"k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto copies all properties from this object into another object of the same type
func (in *WarmPool) DeepCopyInto(out *WarmPool) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy creates a new instance of the object and copies data from the original
func (in *WarmPool) DeepCopy() *WarmPool {
	if in == nil {
		return nil
	}
	out := new(WarmPool)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a generically typed copy of an object
func (in *WarmPool) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies all properties from this object into another object of the same type
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

// DeepCopy creates a new instance of the object and copies data from the original
func (in *WarmPoolList) DeepCopy() *WarmPoolList {
	if in == nil {
		return nil
	}
	out := new(WarmPoolList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a generically typed copy of an object
func (in *WarmPoolList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies all properties from this object into another object of the same type
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

// DeepCopyInto copies all properties from this object into another object of the same type
func (in *PreloadScript) DeepCopyInto(out *PreloadScript) {
	*out = *in
}

// DeepCopyInto copies all properties from this object into another object of the same type
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

// DeepCopyInto copies all properties from this object into another object of the same type
func (in *WarmPoolCondition) DeepCopyInto(out *WarmPoolCondition) {
	*out = *in
	in.LastTransitionTime.DeepCopyInto(&out.LastTransitionTime)
}
