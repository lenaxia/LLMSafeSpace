import { describe, expect, it } from "vitest";
import { extractStreamText, parseCompleteStream } from "./stream";

describe("extractStreamText", () => {
  it("returns raw text when input is not JSON", () => {
    const result = extractStreamText("Hello world");
    expect(result.displayText).toBe("Hello world");
    expect(result.thinkingText).toBe("");
    expect(result.isJSON).toBe(false);
  });

  it("extracts text from complete opencode JSON format", () => {
    const json = '{"info":{"id":"m1"},"parts":[{"type":"text","text":"Hello world"}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("Hello world");
    expect(result.thinkingText).toBe("");
    expect(result.isJSON).toBe(true);
  });

  it("extracts thinking text from parts", () => {
    const json = '{"info":{},"parts":[{"type":"thinking","text":"Let me think..."},{"type":"text","text":"Answer"}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("Answer");
    expect(result.thinkingText).toBe("Let me think...");
  });

  it("extracts text from multiple parts", () => {
    const json = '{"info":{},"parts":[{"type":"text","text":"Part 1"},{"type":"text","text":" Part 2"}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("Part 1 Part 2");
  });

  it("skips non-text non-thinking parts", () => {
    const json = '{"info":{},"parts":[{"type":"patch","text":"diff"},{"type":"text","text":"Hello"}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("Hello");
    expect(result.thinkingText).toBe("");
  });

  it("handles escaped characters in text", () => {
    const json = '{"info":{},"parts":[{"type":"text","text":"line1\\nline2"}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("line1\nline2");
  });

  it("returns empty for JSON that has no extractable text yet", () => {
    const partial = '{"info":{"id":"m1"},"par';
    const result = extractStreamText(partial);
    expect(result.isJSON).toBe(true);
    expect(result.displayText).toBe("");
    expect(result.thinkingText).toBe("");
  });
});

describe("parseCompleteStream", () => {
  it("parses complete opencode JSON response into parts", () => {
    const json = '{"info":{"id":"m1","role":"assistant"},"parts":[{"type":"text","text":"Hello world"}]}';
    const result = parseCompleteStream(json);
    expect(Array.isArray(result)).toBe(true);
    expect(result).toEqual([{ type: "text", text: "Hello world" }]);
  });

  it("preserves multiple part types", () => {
    const json = '{"info":{},"parts":[{"type":"thinking","text":"Let me think"},{"type":"text","text":"Answer"},{"type":"tool_call","text":"search()"}]}';
    const result = parseCompleteStream(json);
    expect(Array.isArray(result)).toBe(true);
    expect(result).toEqual([
      { type: "thinking", text: "Let me think" },
      { type: "text", text: "Answer" },
      { type: "tool_call", text: "search()" },
    ]);
  });

  it("handles array format (message history) by returning last message parts", () => {
    const json = '[{"info":{},"parts":[{"type":"text","text":"First"}]},{"info":{},"parts":[{"type":"text","text":"Second"}]}]';
    expect(parseCompleteStream(json)).toEqual([{ type: "text", text: "Second" }]);
  });

  it("returns raw text when not valid JSON", () => {
    expect(parseCompleteStream("Just plain text")).toBe("Just plain text");
  });

  it("handles empty parts array", () => {
    const json = '{"info":{},"parts":[]}';
    expect(parseCompleteStream(json)).toEqual([]);
  });

  it("handles response with no parts field", () => {
    const json = '{"status":"ok"}';
    expect(parseCompleteStream(json)).toBe(json);
  });
});
