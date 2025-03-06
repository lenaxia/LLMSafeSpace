package common

// Controller-related constants
const (
	// Controller name
	ControllerName = "sandbox-controller"
	
	// Finalizer names
	SandboxFinalizer = "sandbox.llmsafespace.dev/finalizer"
	WarmPoolFinalizer = "warmpool.llmsafespace.dev/finalizer"
	WarmPodFinalizer = "warmpod.llmsafespace.dev/finalizer"
	
	// Annotation keys
	AnnotationCreatedBy = "llmsafespace.dev/created-by"
	AnnotationWarmPodID = "llmsafespace.dev/warm-pod-id"
	AnnotationSandboxID = "llmsafespace.dev/sandbox-id"
	AnnotationPoolName = "llmsafespace.dev/pool-name"
	AnnotationRecyclable = "llmsafespace.dev/recyclable"
	
	// Label keys
	LabelApp = "app"
	LabelComponent = "component"
	LabelSandboxID = "sandbox-id"
	LabelPoolName = "pool-name"
	LabelWarmPodID = "warm-pod-id"
	LabelRuntime = "runtime"
	
	// Component values
	ComponentSandbox = "sandbox"
	ComponentWarmPool = "warmpool"
	ComponentWarmPod = "warmpod"
	
	// Condition types
	ConditionReady = "Ready"
	ConditionPodCreated = "PodCreated"
	ConditionPodRunning = "PodRunning"
	ConditionPoolReady = "PoolReady"
	ConditionScalingUp = "ScalingUp"
	ConditionScalingDown = "ScalingDown"
	
	// Condition reasons
	ReasonPodCreated = "PodCreated"
	ReasonPodCreationFailed = "PodCreationFailed"
	ReasonPodRunning = "PodRunning"
	ReasonPodNotRunning = "PodNotRunning"
	ReasonPoolReady = "PoolReady"
	ReasonPoolNotReady = "PoolNotReady"
	ReasonScalingUp = "ScalingUp"
	ReasonScalingDown = "ScalingDown"
	
	// Phase values for Sandbox
	SandboxPhasePending = "Pending"
	SandboxPhaseCreating = "Creating"
	SandboxPhaseRunning = "Running"
	SandboxPhaseTerminating = "Terminating"
	SandboxPhaseTerminated = "Terminated"
	SandboxPhaseFailed = "Failed"
	
	// Phase values for WarmPod
	WarmPodPhasePending = "Pending"
	WarmPodPhaseReady = "Ready"
	WarmPodPhaseAssigned = "Assigned"
	WarmPodPhaseTerminating = "Terminating"
)
