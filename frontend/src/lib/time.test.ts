import { describe, expect, it } from "vitest";
import { formatRelativeTime } from "./time";

const NOW = new Date("2024-06-15T12:00:00Z").getTime();

describe("formatRelativeTime", () => {
  it("returns 'now' for timestamps less than a minute ago", () => {
    const recent = new Date(NOW - 30_000).toISOString();
    expect(formatRelativeTime(recent, NOW)).toBe("now");
  });

  it("returns minutes for timestamps less than an hour ago", () => {
    const fiveMin = new Date(NOW - 5 * 60_000).toISOString();
    expect(formatRelativeTime(fiveMin, NOW)).toBe("5m");
  });

  it("returns hours for timestamps less than a day ago", () => {
    const twoHours = new Date(NOW - 2 * 60 * 60_000).toISOString();
    expect(formatRelativeTime(twoHours, NOW)).toBe("2h");
  });

  it("returns days for timestamps more than a day ago", () => {
    const threeDays = new Date(NOW - 3 * 24 * 60 * 60_000).toISOString();
    expect(formatRelativeTime(threeDays, NOW)).toBe("3d");
  });
});
