import { describe, expect, it } from "vitest";
import { renderHook } from "@testing-library/react";
import { useMediaQuery, useIsMobile } from "./useMediaQuery";

describe("useMediaQuery", () => {
  it("returns false when matchMedia returns false", () => {
    const { result } = renderHook(() => useMediaQuery("(min-width: 768px)"));
    // jsdom mock returns matches: false by default
    expect(result.current).toBe(false);
  });
});

describe("useIsMobile", () => {
  it("returns true when screen is below 768px (matchMedia false)", () => {
    const { result } = renderHook(() => useIsMobile());
    // matchMedia("(min-width: 768px)") returns false in jsdom → isMobile = true
    expect(result.current).toBe(true);
  });
});
