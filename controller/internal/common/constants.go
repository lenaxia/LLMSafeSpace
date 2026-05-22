package common

// Controller-related constants
const (
	// Controller name
	ControllerName = "controller"

	// Finalizer names
	SandboxFinalizer = "sandbox.llmsafespace.dev/finalizer"

	// Annotation keys
	AnnotationCreatedBy = "llmsafespace.dev/created-by"
	AnnotationSandboxID = "llmsafespace.dev/sandbox-id"
	// AnnotationRuntimeEnv records which RuntimeEnvironment was matched
	// when resolving sandbox.spec.runtime → container image. Useful for
	// debugging mis-routed sandboxes (e.g. when multiple RuntimeEnvs share
	// the same language+version and the resolver picked an unexpected one).
	AnnotationRuntimeEnv = "llmsafespace.dev/runtime-env"

	// Label keys
	LabelApp       = "app"
	LabelComponent = "component"
	LabelSandboxID = "sandbox-id"
	LabelRuntime   = "runtime"
	LabelStatus    = "status"

	// Component values
	ComponentSandbox = "sandbox"

	// Condition types
	ConditionReady      = "Ready"
	ConditionPodCreated = "PodCreated"
	ConditionPodRunning = "PodRunning"

	// Condition reasons
	ReasonPodCreated        = "PodCreated"
	ReasonPodCreationFailed = "PodCreationFailed"
	ReasonPodRunning        = "PodRunning"
	ReasonPodNotRunning     = "PodNotRunning"

	// Phase values for Sandbox
	SandboxPhasePending     = "Pending"
	SandboxPhaseCreating    = "Creating"
	SandboxPhaseRunning     = "Running"
	SandboxPhaseSuspending  = "Suspending"
	SandboxPhaseSuspended   = "Suspended"
	SandboxPhaseResuming    = "Resuming"
	SandboxPhaseTerminating = "Terminating"
	SandboxPhaseTerminated  = "Terminated"
	SandboxPhaseFailed      = "Failed"
)
