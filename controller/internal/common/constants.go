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
	// LabelWorkspace tags a sandbox pod with its parent Workspace's name
	// so that the workspace controller (which selects by this label in
	// deleteWorkspacePods) can find and delete the pod on suspend.
	// Matches the selector hardcoded in workspace controller.go.
	LabelWorkspace = "llmsafespace.dev/workspace"

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

// Transient-failure recovery thresholds (fix #2).
//
// MaxTransientFailures is the maximum consecutive transient pod-loss events
// the controller will self-heal by reverting to Pending before declaring the
// sandbox Failed. The Nth occurrence (N == MaxTransientFailures) marks the
// sandbox Failed; recovery requires explicit POST /sandboxes/:id/retry (fix #5).
//
// 3 is chosen as a balance: enough to absorb realistic transient causes
// (graceful pod-delete during a node drain, kubelet GC), but small enough
// that a stuck pod doesn't cycle indefinitely.
const MaxTransientFailures = 3

// TransientFailureResetWindow is how long the sandbox must remain in Running
// before TransientFailureCount is reset to 0. Pods that recover and stay
// healthy for this duration are considered fully recovered.
//
// 5 minutes is chosen to be longer than typical kubelet pod-startup spikes
// (image pull + init containers can take 1-2 min on cold nodes), so a
// "transient" event followed by a quick recurrence still counts as the
// same incident.
const TransientFailureResetWindow = 5 * 60 // seconds

// Reason codes for transient-failure events (fix #2). These appear in
// SandboxCondition.Reason fields and structured log entries.
const (
	ReasonPodTransientLoss  = "PodTransientLoss"  // pod absent; reverting to Pending for self-heal
	ReasonPodPersistentLoss = "PodPersistentLoss" // pod absent; transient counter exhausted, marking Failed
)
