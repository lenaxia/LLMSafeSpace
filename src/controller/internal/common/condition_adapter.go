package common

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"github.com/lenaxia/llmsafespace/controller/internal/resources"
)

// ConvertToMetaV1Condition converts a SandboxCondition to metav1.Condition
func ConvertToMetaV1Condition(condition resources.SandboxCondition) metav1.Condition {
	return metav1.Condition{
		Type:               condition.Type,
		Status:             metav1.ConditionStatus(condition.Status),
		Reason:             condition.Reason,
		Message:            condition.Message,
		LastTransitionTime: metav1.NewTime(time.Now()),
	}
}

// ConvertFromMetaV1Condition converts a metav1.Condition to SandboxCondition
func ConvertFromMetaV1Condition(condition metav1.Condition) resources.SandboxCondition {
	return resources.SandboxCondition{
		Type:    condition.Type,
		Status:  string(condition.Status),
		Reason:  condition.Reason,
		Message: condition.Message,
	}
}

// ConvertToMetaV1ConditionArray converts an array of SandboxCondition to metav1.Condition
func ConvertToMetaV1ConditionArray(conditions []resources.SandboxCondition) []metav1.Condition {
	result := make([]metav1.Condition, len(conditions))
	for i, condition := range conditions {
		result[i] = ConvertToMetaV1Condition(condition)
	}
	return result
}

// ConvertFromMetaV1ConditionArray converts an array of metav1.Condition to SandboxCondition
func ConvertFromMetaV1ConditionArray(conditions []metav1.Condition) []resources.SandboxCondition {
	result := make([]resources.SandboxCondition, len(conditions))
	for i, condition := range conditions {
		result[i] = ConvertFromMetaV1Condition(condition)
	}
	return result
}

// ConvertWarmPoolToMetaV1Condition converts a WarmPoolCondition to metav1.Condition
func ConvertWarmPoolToMetaV1Condition(condition resources.WarmPoolCondition) metav1.Condition {
	return metav1.Condition{
		Type:               condition.Type,
		Status:             metav1.ConditionStatus(condition.Status),
		Reason:             condition.Reason,
		Message:            condition.Message,
		LastTransitionTime: metav1.NewTime(time.Now()),
	}
}

// ConvertFromMetaV1ToWarmPoolCondition converts a metav1.Condition to WarmPoolCondition
func ConvertFromMetaV1ToWarmPoolCondition(condition metav1.Condition) resources.WarmPoolCondition {
	return resources.WarmPoolCondition{
		Type:    condition.Type,
		Status:  string(condition.Status),
		Reason:  condition.Reason,
		Message: condition.Message,
	}
}

// ConvertWarmPoolToMetaV1ConditionArray converts an array of WarmPoolCondition to metav1.Condition
func ConvertWarmPoolToMetaV1ConditionArray(conditions []resources.WarmPoolCondition) []metav1.Condition {
	result := make([]metav1.Condition, len(conditions))
	for i, condition := range conditions {
		result[i] = ConvertWarmPoolToMetaV1Condition(condition)
	}
	return result
}

// ConvertFromMetaV1ToWarmPoolConditionArray converts an array of metav1.Condition to WarmPoolCondition
func ConvertFromMetaV1ToWarmPoolConditionArray(conditions []metav1.Condition) []resources.WarmPoolCondition {
	result := make([]resources.WarmPoolCondition, len(conditions))
	for i, condition := range conditions {
		result[i] = ConvertFromMetaV1ToWarmPoolCondition(condition)
	}
	return result
}

// SetSandboxCondition sets a condition on a Sandbox resource
func SetSandboxCondition(conditions *[]resources.SandboxCondition, conditionType string, status string, reason, message string) {
	metaConditions := ConvertToMetaV1ConditionArray(*conditions)
	SetCondition(&metaConditions, conditionType, metav1.ConditionStatus(status), reason, message)
	*conditions = ConvertFromMetaV1ConditionArray(metaConditions)
}

// SetWarmPoolCondition sets a condition on a WarmPool resource
func SetWarmPoolCondition(conditions *[]resources.WarmPoolCondition, conditionType string, status string, reason, message string) {
	metaConditions := ConvertWarmPoolToMetaV1ConditionArray(*conditions)
	SetCondition(&metaConditions, conditionType, metav1.ConditionStatus(status), reason, message)
	*conditions = ConvertFromMetaV1ToWarmPoolConditionArray(metaConditions)
}
