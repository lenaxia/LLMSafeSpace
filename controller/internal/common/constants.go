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
