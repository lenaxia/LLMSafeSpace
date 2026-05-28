import { describe, expect, it } from "vitest";
import { sessionDisplayTitle, generateWorkspaceName } from "./names";

describe("sessionDisplayTitle", () => {
  it("returns title when provided", () => {
    expect(sessionDisplayTitle("Clone repo", undefined)).toBe("Clone repo");
  });

  it("returns title even when lastMessageAt is provided", () => {
    expect(sessionDisplayTitle("My Chat", "2026-05-28T01:00:00Z")).toBe("My Chat");
  });

  it("returns 'New chat' when title is undefined", () => {
    expect(sessionDisplayTitle(undefined, "2026-05-28T01:00:00Z")).toBe("New chat");
  });

  it("returns 'New chat' when title is empty string", () => {
    expect(sessionDisplayTitle("", undefined)).toBe("New chat");
  });

  it("returns 'New chat' when both are undefined", () => {
    expect(sessionDisplayTitle(undefined, undefined)).toBe("New chat");
  });
});

describe("generateWorkspaceName", () => {
  it("returns a string matching adjective-noun-number pattern", () => {
    const name = generateWorkspaceName();
    expect(name).toMatch(/^[a-z]+-[a-z]+-\d+$/);
  });

  it("generates different names on successive calls (probabilistic)", () => {
    const names = new Set(Array.from({ length: 20 }, () => generateWorkspaceName()));
    // With 20 attempts, we should get at least 2 unique names
    expect(names.size).toBeGreaterThan(1);
  });

  it("number part is 0-99", () => {
    for (let i = 0; i < 50; i++) {
      const name = generateWorkspaceName();
      const num = parseInt(name.split("-").pop()!, 10);
      expect(num).toBeGreaterThanOrEqual(0);
      expect(num).toBeLessThan(100);
    }
  });
});
