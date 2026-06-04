import { useState, useCallback } from "react";
import { workspacesApi } from "../../api/workspaces";

interface AgentReloadBannerProps {
  workspaceId: string;
  workspaceName: string;
  credentialsPendingSince?: string;
  onReloaded?: () => void;
}

export function AgentReloadBanner({
  workspaceId,
  workspaceName,
  credentialsPendingSince,
  onReloaded,
}: AgentReloadBannerProps) {
  const [dismissed, setDismissed] = useState(() => {
    const key = `agent-reload-dismissed:${workspaceId}:${credentialsPendingSince}`;
    return sessionStorage.getItem(key) === "true";
  });
  const [showModal, setShowModal] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const dismiss = useCallback(() => {
    const key = `agent-reload-dismissed:${workspaceId}:${credentialsPendingSince}`;
    sessionStorage.setItem(key, "true");
    setDismissed(true);
  }, [workspaceId, credentialsPendingSince]);

  const handleReload = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await workspacesApi.reloadAgent(workspaceId);
      if (resp.warning) {
        setError(resp.warning);
        // Keep modal open so user sees the warning
      } else {
        setShowModal(false);
        onReloaded?.();
      }
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Reload failed");
    } finally {
      setLoading(false);
    }
  }, [workspaceId, onReloaded]);

  if (dismissed) return null;

  const pendingTime = credentialsPendingSince
    ? new Date(credentialsPendingSince).toLocaleTimeString()
    : "recently";

  return (
    <>
      <div className="rounded-md border border-blue-200 bg-blue-50 p-3 mb-4 dark:border-blue-800 dark:bg-blue-950">
        <div className="flex items-start gap-2">
          <span className="text-blue-600 dark:text-blue-400">ⓘ</span>
          <div className="flex-1">
            <p className="text-sm text-blue-800 dark:text-blue-200">
              <strong>New credentials available.</strong> You added or changed
              credentials at {pendingTime}. Reload the agent to start using them.
            </p>
            <div className="mt-2 flex gap-2">
              <button
                onClick={() => setShowModal(true)}
                className="rounded bg-blue-600 px-3 py-1 text-xs text-white hover:bg-blue-700"
              >
                Reload agent
              </button>
              <button
                onClick={dismiss}
                className="rounded px-3 py-1 text-xs text-blue-600 hover:bg-blue-100 dark:text-blue-400 dark:hover:bg-blue-900"
              >
                Dismiss
              </button>
            </div>
          </div>
        </div>
      </div>

      {showModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
          <div className="w-full max-w-md rounded-lg bg-white p-6 shadow-xl dark:bg-gray-800">
            <h3 className="text-lg font-medium text-gray-900 dark:text-gray-100">
              Reload agent for {workspaceName}?
            </h3>
            <p className="mt-2 text-sm text-gray-600 dark:text-gray-400">
              ⚠ This will abort any LLM call currently in progress in this
              workspace. Your sessions and conversation history are preserved.
            </p>
            {error && (
              <p className="mt-2 text-sm text-red-600 dark:text-red-400">{error}</p>
            )}
            <div className="mt-4 flex justify-end gap-2">
              <button
                onClick={() => setShowModal(false)}
                disabled={loading}
                className="rounded px-4 py-2 text-sm text-gray-600 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-700"
              >
                Cancel
              </button>
              <button
                onClick={handleReload}
                disabled={loading}
                className="rounded bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-700 disabled:opacity-50"
              >
                {loading ? "Reloading…" : "Reload"}
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
