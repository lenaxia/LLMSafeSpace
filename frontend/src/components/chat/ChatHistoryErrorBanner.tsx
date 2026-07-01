import { ApiClientError } from "../../api/client";
import { extractOpencodeRef } from "../../api/opencodeRef";

interface ChatHistoryErrorBannerProps {
  error: unknown;
  onRetry: () => void;
}

/**
 * ChatHistoryErrorBanner (LLMSafeSpaces#490) — inline diagnostic banner
 * shown when the message-history query fails. Replaces the pre-#490
 * silent-empty-chat state that made session-history failures
 * indistinguishable from "no messages yet."
 *
 * Design decisions:
 *   - Inline banner (not toast) — a user staring at a blank chat needs
 *     the error visible in the chat area, not dismissed in a corner.
 *   - Retry action — the underlying react-query hook supports refetch;
 *     wire it here so the user can self-service the transient case
 *     (pod-restart, rate-limit, etc.) without a page reload.
 *   - Opencode ref — pulled from the API error body via
 *     extractOpencodeRef, shown in a `<details>` block. Not front-and-
 *     center (users don't need it) but discoverable for operators
 *     debugging their own or a support-ticketed session.
 *   - Visual language reused from the sibling chatError + streamTimedOut
 *     banners in ChatPage.tsx:971-982 (destructive/10 background,
 *     destructive/50 border, text-destructive foreground).
 *
 * The companion server-side observability (#488) records the same ref
 * as a metric label + log line. An operator debugging an incident goes:
 * user reports "chat blank" → operator opens the DevTools banner →
 * copies the ref → greps opencode logs by that ref → stack trace.
 */
export function ChatHistoryErrorBanner({
  error,
  onRetry,
}: ChatHistoryErrorBannerProps) {
  const status = error instanceof ApiClientError ? error.status : undefined;
  const ref = error instanceof ApiClientError ? extractOpencodeRef(error.body) : undefined;
  const message =
    error instanceof ApiClientError && error.body?.error
      ? error.body.error
      : error instanceof Error
        ? error.message
        : "Unknown error";

  return (
    <div
      role="alert"
      className="flex flex-col gap-2 border-b border-destructive/50 bg-destructive/10 px-4 py-3 text-sm text-destructive"
    >
      <div className="flex items-center justify-between gap-2">
        <span className="font-medium">Chat history unavailable</span>
        <button
          type="button"
          onClick={onRetry}
          className="underline hover:no-underline"
        >
          Retry
        </button>
      </div>
      <details className="text-xs">
        <summary className="cursor-pointer">Details</summary>
        <div className="mt-1 space-y-0.5 font-mono">
          {status !== undefined && <div>HTTP {status}</div>}
          <div>{message}</div>
          {ref && <div>Ref: {ref}</div>}
        </div>
      </details>
    </div>
  );
}
