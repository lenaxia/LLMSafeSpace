/**
 * extractOpencodeRef pulls the opencode error reference (err_XXXXXXXX)
 * from either of the two shapes it appears in on API responses:
 *
 *   1. `{ ref: "err_abcdef12", ...allowlisted fields }` — the shape after
 *      the API's EnrichChatErrorBody allowlist runs (POST /prompt path,
 *      backend proxy_chat_enrichment.go promotes `ref` to top level).
 *
 *   2. `{ name: "UnknownError", data: { ref: "err_abcdef12", ... } }` —
 *      raw opencode envelope, passed through verbatim on GET history
 *      (proxy_handlers.go:155 skips the allowlist for reads).
 *
 * Returns undefined when the ref is absent OR when the input is not an
 * object-shaped body. Never throws.
 *
 * See LLMSafeSpaces#488 for the server-side companion (metric + log line
 * carry the same ref). Together an operator can go: banner shows the ref
 * → grep opencode logs → root cause.
 */
export function extractOpencodeRef(body: unknown): string | undefined {
  if (body === null || body === undefined || typeof body !== "object") {
    return undefined;
  }
  const record = body as Record<string, unknown>;

  const topRef = record.ref;
  if (typeof topRef === "string" && topRef.length > 0) {
    return topRef;
  }

  const data = record.data;
  if (data !== null && data !== undefined && typeof data === "object") {
    const nested = (data as Record<string, unknown>).ref;
    if (typeof nested === "string" && nested.length > 0) {
      return nested;
    }
  }

  return undefined;
}
