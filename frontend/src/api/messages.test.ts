import { describe, expect, it } from "vitest";
import { transformHistory } from "./messages";

describe("transformHistory", () => {
  it("extracts createdAt from info.time.created (epoch millis)", () => {
    const epochMillis = 1717948800123;
    const raw = [
      {
        info: { role: "user", id: "msg_1", time: { created: epochMillis } },
        parts: [{ type: "text", text: "hello" }],
      },
    ];
    const result = transformHistory(raw);
    expect(result).toHaveLength(1);
    expect(result[0]!.createdAt).toEqual(new Date(epochMillis).toISOString());
  });

  it("extracts modelID from info.modelID on assistant messages", () => {
    const raw = [
      {
        info: {
          role: "assistant",
          id: "msg_2",
          time: { created: 1717948800123 },
          modelID: "gpt-4o",
          providerID: "openai",
        },
        parts: [{ type: "text", text: "hi there" }],
      },
    ];
    const result = transformHistory(raw);
    expect(result).toHaveLength(1);
    expect(result[0]!.modelID).toBe("gpt-4o");
  });

  it("omits modelID on user messages", () => {
    const raw = [
      {
        info: { role: "user", id: "msg_1", time: { created: 1717948800123 } },
        parts: [{ type: "text", text: "hello" }],
      },
    ];
    const result = transformHistory(raw);
    expect(result[0]!.modelID).toBeUndefined();
  });

  it("omits createdAt when info.time.created is absent", () => {
    const raw = [
      {
        info: { role: "user", id: "msg_1" },
        parts: [{ type: "text", text: "hello" }],
      },
    ];
    const result = transformHistory(raw);
    expect(result[0]!.createdAt).toBeUndefined();
  });

  it("omits modelID when info.modelID is absent", () => {
    const raw = [
      {
        info: { role: "assistant", id: "msg_2", time: { created: 1717948800123 } },
        parts: [{ type: "text", text: "response" }],
      },
    ];
    const result = transformHistory(raw);
    expect(result[0]!.modelID).toBeUndefined();
  });

  it("extracts both createdAt and modelID on assistant messages with full metadata", () => {
    const epochMillis = 1717948800123;
    const raw = [
      {
        info: {
          role: "assistant",
          id: "msg_3",
          time: { created: epochMillis, completed: epochMillis + 5000 },
          modelID: "claude-3.5-sonnet",
          providerID: "anthropic",
        },
        parts: [{ type: "text", text: "world" }],
      },
    ];
    const result = transformHistory(raw);
    expect(result[0]!.createdAt).toEqual(new Date(epochMillis).toISOString());
    expect(result[0]!.modelID).toBe("claude-3.5-sonnet");
  });

  it("handles multiple messages with mixed metadata presence", () => {
    const raw = [
      {
        info: { role: "user", id: "msg_1", time: { created: 1000 } },
        parts: [{ type: "text", text: "hi" }],
      },
      {
        info: {
          role: "assistant",
          id: "msg_2",
          time: { created: 2000 },
          modelID: "gpt-4o",
        },
        parts: [{ type: "text", text: "hello" }],
      },
      {
        info: { role: "user", id: "msg_3" },
        parts: [{ type: "text", text: "bye" }],
      },
    ];
    const result = transformHistory(raw);
    expect(result).toHaveLength(3);
    expect(result[0]!.createdAt).toEqual(new Date(1000).toISOString());
    expect(result[0]!.modelID).toBeUndefined();
    expect(result[1]!.createdAt).toEqual(new Date(2000).toISOString());
    expect(result[1]!.modelID).toBe("gpt-4o");
    expect(result[2]!.createdAt).toBeUndefined();
    expect(result[2]!.modelID).toBeUndefined();
  });
});
