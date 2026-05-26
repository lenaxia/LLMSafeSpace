import { describe, expect, it } from "vitest";
import { extractStreamText, parseCompleteStream } from "./stream";

describe("stream parser — production edge cases", () => {
  it("handles chunk split mid-JSON-string (text value split across reads)", () => {
    const chunk1 = '{"info":{"id":"m1"},"parts":[{"type":"text","text":"Hello wor';
    const chunk2 = 'ld, how are you?"}]}';

    const partial = extractStreamText(chunk1);
    expect(partial.isJSON).toBe(true);

    const full = parseCompleteStream(chunk1 + chunk2);
    expect(Array.isArray(full)).toBe(true);
    expect(full).toEqual([{ type: "text", text: "Hello world, how are you?" }]);
  });

  it("handles chunk split mid-escape-sequence", () => {
    const complete = '{"info":{},"parts":[{"type":"text","text":"line1\\nline2"}]}';
    const result = parseCompleteStream(complete);
    expect(Array.isArray(result)).toBe(true);
    expect(result).toEqual([{ type: "text", text: "line1\nline2" }]);
  });

  it("handles unicode in text content", () => {
    const json = '{"info":{},"parts":[{"type":"text","text":"Hello 🌍 world"}]}';
    const result = parseCompleteStream(json);
    expect(Array.isArray(result)).toBe(true);
    expect(result).toEqual([{ type: "text", text: "Hello 🌍 world" }]);
  });

  it("handles very large text content (10KB+)", () => {
    const bigText = "x".repeat(10000);
    const json = `{"info":{},"parts":[{"type":"text","text":"${bigText}"}]}`;
    const result = parseCompleteStream(json);
    expect(Array.isArray(result)).toBe(true);
    expect(result).toEqual([{ type: "text", text: bigText }]);
  });

  it("handles multiple text parts with code blocks", () => {
    const json = '{"info":{},"parts":[{"type":"text","text":"Here is code:\\n```go\\nfunc main() {}\\n```\\nDone."}]}';
    const result = parseCompleteStream(json);
    expect(Array.isArray(result)).toBe(true);
    const parts = result as Array<{ type: string; text: string }>;
    expect(parts[0]!.text).toContain("```go");
    expect(parts[0]!.text).toContain("func main()");
  });

  it("handles empty response body", () => {
    expect(parseCompleteStream("")).toBe("");
  });

  it("handles response that is just whitespace", () => {
    expect(parseCompleteStream("   \n  ")).toBe("   \n  ");
  });

  it("handles opencode error response format", () => {
    const errorJson = '{"error":"session not found","code":"NOT_FOUND"}';
    expect(parseCompleteStream(errorJson)).toBe(errorJson);
  });

  it("handles parts with mixed types including tool_use", () => {
    const json = '{"info":{},"parts":[{"type":"tool_use","name":"read_file","input":{}},{"type":"text","text":"I read the file."}]}';
    const result = parseCompleteStream(json);
    expect(Array.isArray(result)).toBe(true);
    expect(result).toEqual([
      { type: "tool_use", text: "" },
      { type: "text", text: "I read the file." },
    ]);
  });

  it("extractStreamText handles progressive JSON accumulation", () => {
    const chunks = [
      '{"info":{"id":"m1","role":"assistant"},',
      '"parts":[{"type":"text","text":"The answer is ',
      '42."}]}',
    ];

    let accumulated = "";
    for (const chunk of chunks) {
      accumulated += chunk;
      const result = extractStreamText(accumulated);
      expect(result.isJSON).toBe(true);
    }

    const result = parseCompleteStream(accumulated);
    expect(Array.isArray(result)).toBe(true);
    expect(result).toEqual([{ type: "text", text: "The answer is 42." }]);
  });
});
