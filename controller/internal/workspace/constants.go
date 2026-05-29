package workspace

import "time"

const WorkspaceFinalizer = "workspace.llmsafespace.dev/finalizer"

const AnnotationSuspendOnCredLoss = "llmsafespace.dev/suspend-on-cred-loss"

const CredentialSecretDataKey = "provider-config"

// Pod naming: {workspaceName}-{uid[:8]}
const podNameSuffix = 8

// Requeue intervals.
const (
	requeueCreating = 5 * time.Second
	requeueActive   = 15 * time.Second
)

// pendingPhaseTimeout is how long a workspace can stay in Pending before
// being marked Failed.
const pendingPhaseTimeout = 5 * time.Minute

// MaxTransientFailures is the maximum consecutive transient pod-loss events
// the controller will self-heal before declaring the workspace Failed.
const MaxTransientFailures = 3

// TransientFailureResetWindow is how long (seconds) the workspace must remain
// Active before TransientFailureCount resets to 0.
const TransientFailureResetWindow = 5 * 60

// Labels applied to workspace pods.
const (
	LabelApp       = "app"
	LabelComponent = "component"
	LabelWorkspace = "llmsafespace.dev/workspace"
	LabelRuntime   = "runtime"

	AppName            = "llmsafespace"
	ComponentWorkspace = "workspace"
)

// Password secret naming.
func passwordSecretName(workspaceName string) string {
	return "workspace-pw-" + workspaceName
}

// Pod name from workspace name and UID.
func podName(workspaceName string, uid string) string {
	suffix := uid
	if len(suffix) > podNameSuffix {
		suffix = suffix[:podNameSuffix]
	}
	return workspaceName + "-" + suffix
}
