import { AlertTriangle, Info, Wifi } from "lucide-react";
import type { CredentialState, AgentHealth } from "../../api/types";

interface Props {
  credentialState?: CredentialState;
  agentHealth?: AgentHealth;
}

function credentialLabel(state?: CredentialState) {
  if (!state || state.available) return null;
  if (state.reason === "NotChecked") return null;
  if (state.reason === "CredentialSecretNotFound") {
    return {
      icon: Info,
      node: (
        <>
          No providers configured, using Opencode Zen free models.{" "}
          <a
            href="https://opencode.ai"
            target="_blank"
            rel="noopener noreferrer"
            className="underline hover:text-yellow-800 dark:hover:text-yellow-300"
          >
            Click here to learn more
          </a>
        </>
      ),
    };
  }
  const reasons: Record<string, string> = {
    CredentialEmpty: "Credentials are empty",
    CredentialInvalid: "Credentials are invalid",
    CredentialCheckError: "Credential check failed",
    CredentialValidationError: "Credential validation failed",
  };
  const label = reasons[state.reason ?? ""] ?? state.message;
  if (!label) return null;
  return { icon: AlertTriangle, node: label };
}

function agentLabel(health?: AgentHealth) {
  if (!health) return null;
  if (health.status === "Healthy") return null;
  const labels: Record<string, string> = {
    Degraded: health.message || "Agent degraded — no providers connected",
    Unhealthy: health.message || "Agent is unhealthy",
    Unknown: "Agent health unknown",
  };
  return labels[health.status] ?? health.message ?? null;
}

export function HealthBanner({ credentialState, agentHealth }: Props) {
  const credIssue = credentialLabel(credentialState);
  const agentIssue = agentLabel(agentHealth);

  if (!credIssue && !agentIssue) return null;

  return (
    <div className="flex flex-col gap-1 border-b border-border bg-yellow-500/5 px-4 py-2 text-sm">
      {credIssue && (
        <div className="flex items-center gap-2 text-yellow-600 dark:text-yellow-400">
          <credIssue.icon className="h-3.5 w-3.5 flex-shrink-0" />
          {credIssue.node}
        </div>
      )}
      {agentIssue && (
        <div className="flex items-center gap-2 text-yellow-600 dark:text-yellow-400">
          {agentHealth?.status === "Degraded" ? (
            <Wifi className="h-3.5 w-3.5 flex-shrink-0" />
          ) : (
            <AlertTriangle className="h-3.5 w-3.5 flex-shrink-0" />
          )}
          <span>{agentIssue}</span>
        </div>
      )}
    </div>
  );
}
