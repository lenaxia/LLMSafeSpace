import { describe, expect, it } from "vitest";
import { extractStreamText, parseCompleteStream } from "./stream";

describe("extractStreamText", () => {
  it("returns raw text when input is not JSON", () => {
    const result = extractStreamText("Hello world");
    expect(result.displayText).toBe("Hello world");
    expect(result.thinkingText).toBe("");
    expect(result.isJSON).toBe(false);
  });

  it("returns raw text for any non-JSON input", () => {
    const result = extractStreamText("<html>not json</html>");
    expect(result.displayText).toBe("<html>not json</html>");
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

  // --- Field ordering (the bug we fixed) ---

  it("handles reversed field order: text before type", () => {
    const json = '{"info":{},"parts":[{"text":"Hello","type":"text"}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("Hello");
    expect(result.isJSON).toBe(true);
  });

  it("handles reversed field order: thinking with text before type", () => {
    const json = '{"info":{},"parts":[{"text":"hmm","type":"thinking"},{"text":"Answer","type":"text"}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("Answer");
    expect(result.thinkingText).toBe("hmm");
  });

  it("handles mixed field orderings across multiple parts", () => {
    const json = '{"info":{},"parts":[{"text":"First","type":"text"},{"type":"text","text":" Second"}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("First Second");
  });

  it("handles all parts with reversed field order", () => {
    const json = '{"info":{},"parts":[{"text":"A","type":"text"},{"text":"B","type":"text"},{"text":"C","type":"text"}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("ABC");
  });

  it("handles reverse order thinking before text", () => {
    const json = '{"info":{},"parts":[{"text":"Let me think...","type":"thinking"},{"text":"Here is my answer","type":"text"}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("Here is my answer");
    expect(result.thinkingText).toBe("Let me think...");
  });

  // --- `}` in text values (edge case for [^}]* pattern) ---

  it("handles text containing closing brace character", () => {
    const json = '{"info":{},"parts":[{"type":"text","text":"Hello } World"}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("Hello } World");
  });

  it("handles text containing multiple braces", () => {
    const json = '{"info":{},"parts":[{"type":"text","text":"{nested} brackets {here}"}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("{nested} brackets {here}");
  });

  it("handles text with braces in reversed field order", () => {
    const json = '{"info":{},"parts":[{"text":"count: {1, 2, 3}","type":"text"}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("count: {1, 2, 3}");
  });

  // --- Edge cases ---

  it("handles text with escaped quotes", () => {
    const json = '{"info":{},"parts":[{"type":"text","text":"she said \\"hello\\""}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe('she said "hello"');
  });

  it("handles unicode and emoji in text", () => {
    const json = '{"info":{},"parts":[{"type":"text","text":"Hello 世界 🌍"}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("Hello 世界 🌍");
  });

  it("handles text with newlines and special characters", () => {
    const json = '{"info":{},"parts":[{"type":"text","text":"line1\\nline2\\ttabbed"}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("line1\nline2\ttabbed");
  });

  it("returns empty for parts array with only non-text types", () => {
    const json = '{"info":{},"parts":[{"type":"patch","text":"diff"},{"type":"tool_result","text":"output"}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("");
    expect(result.thinkingText).toBe("");
  });

  it("ignores type text but empty text field", () => {
    const json = '{"info":{},"parts":[{"type":"text","text":""}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("");
  });

  it("handles large text content without crashing", () => {
    const longText = "a".repeat(10000);
    const json = `{"info":{},"parts":[{"type":"text","text":"${longText}"}]}`;
    const result = extractStreamText(json);
    expect(result.displayText.length).toBe(10000);
  });

  // --- Partial / streaming chunks ---

  it("extracts from partial buffer with incomplete open objects", () => {
    const partial = '{"info":{},"parts":[{"type":"text","text":"Hel';
    const result = extractStreamText(partial);
    expect(result.displayText).toBe("");
    expect(result.isJSON).toBe(true);
  });

  it("extracts from buffer that has a complete part followed by incomplete", () => {
    const partial = '{"info":{},"parts":[{"type":"text","text":"Hello"},{"type":"text","text":"Wo';
    const result = extractStreamText(partial);
    expect(result.displayText).toBe("Hello");
    expect(result.isJSON).toBe(true);
  });

  it("extracts from buffer with complete part in reverse order + incomplete", () => {
    const partial = '{"info":{},"parts":[{"text":"Hello","type":"text"},{"text":"Wo';
    const result = extractStreamText(partial);
    expect(result.displayText).toBe("Hello");
  });

  it("extracts thinking when only thinking part is complete", () => {
    const partial = '{"info":{},"parts":[{"type":"thinking","text":"reasoning..."},{"type":"text","text":"An';
    const result = extractStreamText(partial);
    expect(result.displayText).toBe("");
    expect(result.thinkingText).toBe("reasoning...");
  });

  it("extracts nothing from empty parts array", () => {
    const json = '{"info":{},"parts":[]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("");
    expect(result.thinkingText).toBe("");
  });

  it("handles array format by scanning all messages", () => {
    const json = '[{"info":{},"parts":[{"type":"text","text":"First"}]},{"info":{},"parts":[{"type":"text","text":"Second"}]}]';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("FirstSecond");
  });

  it("handles empty input gracefully", () => {
    const result = extractStreamText("");
    expect(result.displayText).toBe("");
    expect(result.thinkingText).toBe("");
    expect(result.isJSON).toBe(false);
  });

  it("handles whitespace-only input", () => {
    const result = extractStreamText("   \n  ");
    expect(result.isJSON).toBe(false);
  });
});

describe("parseCompleteStream", () => {
  it("parses complete opencode JSON response into parts", () => {
    const json = '{"info":{"id":"m1","role":"assistant"},"parts":[{"type":"text","text":"Hello world"}]}';
    const result = parseCompleteStream(json);
    expect(Array.isArray(result)).toBe(true);
    expect(result).toMatchObject([{ type: "text", text: "Hello world" }]);
  });

  it("preserves multiple part types including tool_use fields", () => {
    const json = '{"info":{},"parts":[{"type":"thinking","text":"Let me think"},{"type":"text","text":"Answer"},{"type":"tool_use","name":"read_file","input":{"path":"/foo"}}]}';
    const result = parseCompleteStream(json);
    expect(Array.isArray(result)).toBe(true);
    expect(result).toMatchObject([
      { type: "thinking", text: "Let me think" },
      { type: "text", text: "Answer" },
      { type: "tool_use", name: "read_file", input: { path: "/foo" } },
    ]);
  });

  it("preserves all tool_use fields", () => {
    const parts = [
      { type: "tool_use", name: "bash", input: { cmd: "ls" }, id: "call-1" },
    ];
    const json = JSON.stringify({ info: {}, parts });
    const result = parseCompleteStream(json);
    expect(Array.isArray(result)).toBe(true);
    expect(result).toMatchObject([
      { type: "tool_use", name: "bash", input: { cmd: "ls" }, id: "call-1" },
    ]);
  });

  it("handles array format (message history) by returning last message parts", () => {
    const json = '[{"info":{},"parts":[{"type":"text","text":"First"}]},{"info":{},"parts":[{"type":"text","text":"Second"}]}]';
    expect(parseCompleteStream(json)).toMatchObject([{ type: "text", text: "Second" }]);
  });

  it("handles array format with single element", () => {
    const json = '[{"info":{},"parts":[{"type":"text","text":"Only"}]}]';
    expect(parseCompleteStream(json)).toMatchObject([{ type: "text", text: "Only" }]);
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

  it("ignores non-array parts field", () => {
    const json = '{"info":{},"parts":"not-an-array"}';
    expect(parseCompleteStream(json)).toBe(json);
  });

  it("handles malformed JSON gracefully", () => {
    expect(parseCompleteStream("{broken json")).toBe("{broken json");
  });

  it("handles empty string", () => {
    expect(parseCompleteStream("")).toBe("");
  });
});
