package kubernetes

import (
	"github.com/lenaxia/llmsafespace/api/internal/kubernetes/interfaces"
)

// For backward compatibility
type LLMSafespaceV1Interface = interfaces.LLMSafespaceV1Interface
type SandboxInterface = interfaces.SandboxInterface
type WarmPoolInterface = interfaces.WarmPoolInterface
type WarmPodInterface = interfaces.WarmPodInterface
type RuntimeEnvironmentInterface = interfaces.RuntimeEnvironmentInterface
type SandboxProfileInterface = interfaces.SandboxProfileInterface
