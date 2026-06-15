import { describe, expect, it } from "vitest";
import { formatRelativeTime } from "./time";

const NOW = new Date("2024-06-15T12:00:00Z").getTime();

describe("formatRelativeTime", () => {
  describe("main ranges", () => {
    it("returns 'now' for timestamps less than a minute ago", () => {
      expect(formatRelativeTime(new Date(NOW - 30_000).toISOString(), NOW)).toBe("now");
    });

    it("returns minutes for timestamps less than an hour ago", () => {
      expect(formatRelativeTime(new Date(NOW - 5 * 60_000).toISOString(), NOW)).toBe("5m");
    });

    it("returns hours for timestamps less than a day ago", () => {
      expect(formatRelativeTime(new Date(NOW - 2 * 60 * 60_000).toISOString(), NOW)).toBe("2h");
    });

    it("returns days for timestamps more than a day ago", () => {
      expect(formatRelativeTime(new Date(NOW - 3 * 24 * 60 * 60_000).toISOString(), NOW)).toBe("3d");
    });
  });

  describe("boundary conditions", () => {
    it("returns 'now' at 59 999ms", () => {
      expect(formatRelativeTime(new Date(NOW - 59_999).toISOString(), NOW)).toBe("now");
    });

    it("returns '1m' at exactly 60 000ms (not 'now')", () => {
      expect(formatRelativeTime(new Date(NOW - 60_000).toISOString(), NOW)).toBe("1m");
    });

    it("returns '59m' at 3 599 999ms", () => {
      expect(formatRelativeTime(new Date(NOW - 3_599_999).toISOString(), NOW)).toBe("59m");
    });

    it("returns '1h' at exactly 3 600 000ms (not '60m')", () => {
      expect(formatRelativeTime(new Date(NOW - 3_600_000).toISOString(), NOW)).toBe("1h");
    });

    it("returns '23h' at 86 399 999ms", () => {
      expect(formatRelativeTime(new Date(NOW - 86_399_999).toISOString(), NOW)).toBe("23h");
    });

    it("returns '1d' at exactly 86 400 000ms (not '24h')", () => {
      expect(formatRelativeTime(new Date(NOW - 86_400_000).toISOString(), NOW)).toBe("1d");
    });
  });

  describe("edge cases", () => {
    it("returns 'now' for a future timestamp (negative diff → mins < 1)", () => {
      expect(formatRelativeTime(new Date(NOW + 60_000).toISOString(), NOW)).toBe("now");
    });

    it("returns a string for an invalid ISO (documents NaN behavior)", () => {
      // new Date("invalid").getTime() === NaN; all comparisons fail; falls to days
      // Tested to catch silent regressions if behavior changes
      expect(typeof formatRelativeTime("not-a-date", NOW)).toBe("string");
    });
  });
});
