import { describe, expect, it } from "vitest";
import { formatRelativeTime } from "./time";

describe("formatRelativeTime", () => {
  it("returns 'now' for timestamps less than a minute ago", () => {
    const recent = new Date(Date.now() - 30_000).toISOString();
    expect(formatRelativeTime(recent)).toBe("now");
  });

  it("returns minutes for timestamps less than an hour ago", () => {
    const fiveMin = new Date(Date.now() - 5 * 60_000).toISOString();
    expect(formatRelativeTime(fiveMin)).toBe("5m");
  });

  it("returns hours for timestamps less than a day ago", () => {
    const twoHours = new Date(Date.now() - 2 * 60 * 60_000).toISOString();
    expect(formatRelativeTime(twoHours)).toBe("2h");
  });

  it("returns days for timestamps more than a day ago", () => {
    const threeDays = new Date(Date.now() - 3 * 24 * 60 * 60_000).toISOString();
    expect(formatRelativeTime(threeDays)).toBe("3d");
  });
});
