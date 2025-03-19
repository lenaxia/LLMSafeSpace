package resources

import (
	"k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto copies all properties from this object into another object of the same type
func (in *WarmPod) DeepCopyInto(out *WarmPod) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy creates a new instance of the object and copies data from the original
func (in *WarmPod) DeepCopy() *WarmPod {
	if in == nil {
		return nil
	}
	out := new(WarmPod)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a generically typed copy of an object
func (in *WarmPod) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies all properties from this object into another object of the same type
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

// DeepCopy creates a new instance of the object and copies data from the original
func (in *WarmPodList) DeepCopy() *WarmPodList {
	if in == nil {
		return nil
	}
	out := new(WarmPodList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a generically typed copy of an object
func (in *WarmPodList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies all properties from this object into another object of the same type
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

// DeepCopyInto copies all properties from this object into another object of the same type
func (in *PoolReference) DeepCopyInto(out *PoolReference) {
	*out = *in
}

// DeepCopyInto copies all properties from this object into another object of the same type
func (in *WarmPodStatus) DeepCopyInto(out *WarmPodStatus) {
	*out = *in
	if in.AssignedAt != nil {
		in, out := &in.AssignedAt, &out.AssignedAt
		*out = (*in).DeepCopy()
	}
}
