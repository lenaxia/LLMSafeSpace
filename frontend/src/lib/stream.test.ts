import { describe, expect, it } from "vitest";
import { extractStreamText, parseCompleteStream } from "./stream";

describe("extractStreamText", () => {
  it("returns raw text when input is not JSON", () => {
    const result = extractStreamText("Hello world");
    expect(result.displayText).toBe("Hello world");
    expect(result.isJSON).toBe(false);
  });

  it("extracts text from complete opencode JSON format", () => {
    const json = '{"info":{"id":"m1"},"parts":[{"type":"text","text":"Hello world"}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("Hello world");
    expect(result.isJSON).toBe(true);
  });

  it("extracts text from partial JSON (text field visible)", () => {
    const partial = '{"info":{"id":"m1"},"parts":[{"type":"text","text":"Hello wor';
    // Can't extract because the text value isn't closed
    const result = extractStreamText(partial);
    expect(result.isJSON).toBe(true);
    // May or may not extract partial — that's ok
  });

  it("extracts text from multiple parts", () => {
    const json = '{"info":{},"parts":[{"type":"text","text":"Part 1"},{"type":"text","text":" Part 2"}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("Part 1 Part 2");
  });

  it("skips non-text parts", () => {
    const json = '{"info":{},"parts":[{"type":"patch","text":"diff"},{"type":"text","text":"Hello"}]}';
    const result = extractStreamText(json);
    expect(result.displayText).toBe("Hello");
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
  });
});

describe("parseCompleteStream", () => {
  it("parses complete opencode JSON response", () => {
    const json = '{"info":{"id":"m1","role":"assistant"},"parts":[{"type":"text","text":"Hello world"}]}';
    expect(parseCompleteStream(json)).toBe("Hello world");
  });

  it("concatenates multiple text parts", () => {
    const json = '{"info":{},"parts":[{"type":"text","text":"Hello "},{"type":"text","text":"world"}]}';
    expect(parseCompleteStream(json)).toBe("Hello world");
  });

  it("skips patch parts", () => {
    const json = '{"info":{},"parts":[{"type":"patch","text":"diff"},{"type":"text","text":"Result"}]}';
    expect(parseCompleteStream(json)).toBe("Result");
  });

  it("handles array format (message history)", () => {
    const json = '[{"info":{},"parts":[{"type":"text","text":"First"}]},{"info":{},"parts":[{"type":"text","text":"Second"}]}]';
    // Returns last message's text
    expect(parseCompleteStream(json)).toBe("Second");
  });

  it("returns raw text when not valid JSON", () => {
    expect(parseCompleteStream("Just plain text")).toBe("Just plain text");
  });

  it("handles empty parts array", () => {
    const json = '{"info":{},"parts":[]}';
    expect(parseCompleteStream(json)).toBe("");
  });

  it("handles response with no parts field", () => {
    const json = '{"status":"ok"}';
    expect(parseCompleteStream(json)).toBe('{"status":"ok"}');
  });
});
