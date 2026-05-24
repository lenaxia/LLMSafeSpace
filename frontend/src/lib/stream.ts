/**
 * Parses streaming response from the proxy.
 *
 * The opencode server uses Hono's stream() which sends Content-Type: text/plain.
 * The body is a JSON document streamed progressively. The proxy passes it through
 * as raw bytes with flush.
 *
 * Two possible formats:
 * 1. A single JSON object: {"info":{...},"parts":[{"type":"text","text":"..."}]}
 *    - Streamed progressively as chunks of the JSON string
 *    - We extract text content from parts on completion
 * 2. Raw text (if opencode changes format in future)
 *    - Display as-is
 *
 * During streaming, we attempt to extract partial text from the accumulated buffer.
 * On completion, we parse the full JSON to get the final message.
 */

export interface ParsedStreamResult {
  /** Text to display during streaming (best-effort extraction) */
  displayText: string;
  /** Whether the accumulated buffer looks like JSON (vs raw text) */
  isJSON: boolean;
}

/**
 * Extract displayable text from a partial or complete stream buffer.
 * Handles the opencode JSON format: {"info":...,"parts":[{"type":"text","text":"..."}]}
 */
export function extractStreamText(accumulated: string): ParsedStreamResult {
  const trimmed = accumulated.trim();

  // If it doesn't start with { or [, treat as raw text
  if (!trimmed.startsWith("{") && !trimmed.startsWith("[")) {
    return { displayText: accumulated, isJSON: false };
  }

  // It looks like JSON — try to extract text content from parts
  // Look for "text":"..." patterns and extract the text values
  const textParts: string[] = [];
  const regex = /"type"\s*:\s*"text"\s*,\s*"text"\s*:\s*"((?:[^"\\]|\\.)*)"/g;
  let match;
  while ((match = regex.exec(trimmed)) !== null) {
    try {
      // Unescape JSON string
      textParts.push(JSON.parse(`"${match[1]}"`));
    } catch {
      textParts.push(match[1]!);
    }
  }

  if (textParts.length > 0) {
    return { displayText: textParts.join(""), isJSON: true };
  }

  // Couldn't extract text parts — might be incomplete JSON
  // Try the reverse pattern: "text":"..." , "type":"text"
  const altRegex = /"text"\s*:\s*"((?:[^"\\]|\\.)*)"\s*,\s*"type"\s*:\s*"text"/g;
  while ((match = altRegex.exec(trimmed)) !== null) {
    try {
      textParts.push(JSON.parse(`"${match[1]}"`));
    } catch {
      textParts.push(match[1]!);
    }
  }

  if (textParts.length > 0) {
    return { displayText: textParts.join(""), isJSON: true };
  }

  // Can't parse yet — show nothing until we can extract text
  return { displayText: "", isJSON: true };
}

/**
 * Parse the complete stream response into a final message text.
 */
export function parseCompleteStream(accumulated: string): string {
  const trimmed = accumulated.trim();

  // Try to parse as JSON
  try {
    const parsed = JSON.parse(trimmed);

    // Shape: {"info":..., "parts":[...]}
    if (parsed.parts && Array.isArray(parsed.parts)) {
      return parsed.parts
        .filter((p: { type?: string }) => p.type === "text")
        .map((p: { text?: string }) => p.text ?? "")
        .join("");
    }

    // Shape: [{"info":..., "parts":[...]}, ...]  (array of messages)
    if (Array.isArray(parsed)) {
      const lastMsg = parsed[parsed.length - 1];
      if (lastMsg?.parts) {
        return lastMsg.parts
          .filter((p: { type?: string }) => p.type === "text")
          .map((p: { text?: string }) => p.text ?? "")
          .join("");
      }
    }
  } catch {
    // Not valid JSON — return as raw text
  }

  return accumulated;
}
