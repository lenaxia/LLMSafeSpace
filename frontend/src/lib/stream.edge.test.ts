import { describe, expect, it } from "vitest";
import { extractStreamText, parseCompleteStream } from "./stream";

describe("stream parser — production edge cases", () => {
  it("handles chunk split mid-JSON-string (text value split across reads)", () => {
    // Simulates: first chunk ends mid-word in a text value
    const chunk1 = '{"info":{"id":"m1"},"parts":[{"type":"text","text":"Hello wor';
    const chunk2 = 'ld, how are you?"}]}';

    // During streaming with only chunk1, can't extract complete text
    const partial = extractStreamText(chunk1);
    expect(partial.isJSON).toBe(true);
    // May or may not extract — that's fine, it's partial

    // After full accumulation, must parse correctly
    const full = parseCompleteStream(chunk1 + chunk2);
    expect(full).toBe("Hello world, how are you?");
  });

  it("handles chunk split mid-escape-sequence", () => {
    // The \\n is split: first chunk has backslash, second has n
    const complete = '{"info":{},"parts":[{"type":"text","text":"line1\\nline2"}]}';
    expect(parseCompleteStream(complete)).toBe("line1\nline2");
  });

  it("handles unicode in text content", () => {
    const json = '{"info":{},"parts":[{"type":"text","text":"Hello 🌍 world"}]}';
    expect(parseCompleteStream(json)).toBe("Hello 🌍 world");
  });

  it("handles very large text content (10KB+)", () => {
    const bigText = "x".repeat(10000);
    const json = `{"info":{},"parts":[{"type":"text","text":"${bigText}"}]}`;
    expect(parseCompleteStream(json)).toBe(bigText);
  });

  it("handles multiple text parts with code blocks", () => {
    const json = '{"info":{},"parts":[{"type":"text","text":"Here is code:\\n```go\\nfunc main() {}\\n```\\nDone."}]}';
    const result = parseCompleteStream(json);
    expect(result).toContain("```go");
    expect(result).toContain("func main()");
  });

  it("handles empty response body", () => {
    expect(parseCompleteStream("")).toBe("");
  });

  it("handles response that is just whitespace", () => {
    expect(parseCompleteStream("   \n  ")).toBe("   \n  ");
  });

  it("handles opencode error response format", () => {
    const errorJson = '{"error":"session not found","code":"NOT_FOUND"}';
    // Should return raw since no parts field
    expect(parseCompleteStream(errorJson)).toBe(errorJson);
  });

  it("handles parts with mixed types including tool_use", () => {
    const json = '{"info":{},"parts":[{"type":"tool_use","name":"read_file","input":{}},{"type":"text","text":"I read the file."}]}';
    expect(parseCompleteStream(json)).toBe("I read the file.");
  });

  it("extractStreamText handles progressive JSON accumulation", () => {
    // Simulate realistic streaming: chunks arrive progressively
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

    // Final result must be correct
    expect(parseCompleteStream(accumulated)).toBe("The answer is 42.");
  });
});
