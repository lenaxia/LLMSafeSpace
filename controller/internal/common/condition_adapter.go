package common

import (
	"time"

	"github.com/lenaxia/llmsafespace/controller/internal/resources"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func SetSandboxCondition(conditions *[]resources.SandboxCondition, conditionType string, status string, reason, message string) {
	metaConditions := ConvertToMetaV1ConditionArray(*conditions)
	SetCondition(&metaConditions, conditionType, metav1.ConditionStatus(status), reason, message)
	*conditions = ConvertFromMetaV1ConditionArray(metaConditions)
}
