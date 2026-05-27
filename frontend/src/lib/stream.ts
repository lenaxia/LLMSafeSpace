/**
 * Parses streaming response from the proxy.
 *
 * The opencode server uses Hono's stream() which sends Content-Type: text/plain.
 * The body is a JSON document streamed progressively. The proxy passes it through
 * as raw bytes with flush.
 *
 * Two possible formats:
 * 1. A single JSON object: {"info":{...},"parts":[{"type":"text","text":"..."}, ...]}
 *    - Streamed progressively as chunks of the JSON string
 *    - We extract parts content from the buffer during streaming and parse fully on completion
 * 2. Raw text (if opencode changes format in future)
 *    - Display as-is
 *
 * During streaming, we attempt to extract partial part content from the accumulated buffer.
 * On completion, we parse the full JSON to get the final message with all parts.
 */

import type { MessagePart } from "../api/types";

export interface ParsedStreamResult {
  /** Text parts to display during streaming (best-effort extraction) */
  displayText: string;
  /** Thinking/reasoning text extracted during streaming */
  thinkingText: string;
  /** Whether the accumulated buffer looks like JSON (vs raw text) */
  isJSON: boolean;
}

/**
 * Extract displayable text from a partial or complete stream buffer.
 * Handles the opencode JSON format: {"info":...,"parts":[{"type":"text","text":"..."},{"type":"thinking","text":"..."}]}
 * Returns both regular text and thinking text separately.
 */
export function extractStreamText(accumulated: string): ParsedStreamResult {
  const trimmed = accumulated.trim();

  if (!trimmed.startsWith("{") && !trimmed.startsWith("[")) {
    return { displayText: accumulated, thinkingText: "", isJSON: false };
  }

  const displayParts: string[] = [];
  const thinkingParts: string[] = [];

  const typeFirst = /"type"\s*:\s*"(text|thinking)"[^}]*"text"\s*:\s*"((?:[^"\\]|\\.)*)"/g;
  const textFirst = /"text"\s*:\s*"((?:[^"\\]|\\.)*)"[^}]*"type"\s*:\s*"(text|thinking)"/g;

  interface PartMatch {
    type: string;
    text: string;
    index: number;
  }
  const matches: PartMatch[] = [];

  let match;
  while ((match = typeFirst.exec(trimmed)) !== null) {
    matches.push({ type: match[1]!, text: match[2]!, index: match.index });
  }
  while ((match = textFirst.exec(trimmed)) !== null) {
    matches.push({ type: match[2]!, text: match[1]!, index: match.index });
  }

  matches.sort((a, b) => a.index - b.index);

  for (const m of matches) {
    try {
      const decoded = JSON.parse(`"${m.text}"`);
      if (m.type === "thinking") {
        thinkingParts.push(decoded);
      } else {
        displayParts.push(decoded);
      }
    } catch {
      if (m.type === "thinking") {
        thinkingParts.push(m.text ?? "");
      } else {
        displayParts.push(m.text ?? "");
      }
    }
  }

  return {
    displayText: displayParts.join(""),
    thinkingText: thinkingParts.join(""),
    isJSON: true,
  };
}

/**
 * Parse the complete stream response into an array of message parts.
 * Returns an array of part objects, or a raw string if not valid JSON.
 */
export function parseCompleteStream(accumulated: string): MessagePart[] | string {
  const trimmed = accumulated.trim();

  try {
    const parsed = JSON.parse(trimmed);

    if (parsed.parts && Array.isArray(parsed.parts)) {
      return parsed.parts.map((p: Record<string, unknown>) => ({
        type: (p.type as string) ?? "text",
        text: p.text as string | undefined,
        name: p.name as string | undefined,
        input: p.input,
        id: p.id as string | undefined,
        files: p.files as string[] | undefined,
        hash: p.hash as string | undefined,
      }));
    }

    if (Array.isArray(parsed)) {
      const lastMsg = parsed[parsed.length - 1];
      if (lastMsg?.parts) {
        return lastMsg.parts.map((p: Record<string, unknown>) => ({
          type: (p.type as string) ?? "text",
          text: p.text as string | undefined,
          name: p.name as string | undefined,
          input: p.input,
          id: p.id as string | undefined,
          files: p.files as string[] | undefined,
          hash: p.hash as string | undefined,
        }));
      }
    }
  } catch {
    // Not valid JSON — return as raw text
  }

  return accumulated;
}
