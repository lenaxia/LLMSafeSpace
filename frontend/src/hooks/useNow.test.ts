import { describe, expect, it, vi, afterEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useNow } from "./useNow";

describe("useNow", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("returns a number close to Date.now() on mount", () => {
    const before = Date.now();
    const { result } = renderHook(() => useNow());
    const after = Date.now();
    expect(result.current).toBeGreaterThanOrEqual(before);
    expect(result.current).toBeLessThanOrEqual(after);
  });

  it("updates after the interval fires", () => {
    vi.useFakeTimers();
    const { result } = renderHook(() => useNow(1000));
    const initial = result.current;

    act(() => { vi.advanceTimersByTime(1000); });

    expect(result.current).toBeGreaterThan(initial);
  });

  it("updates multiple times as interval repeats", () => {
    vi.useFakeTimers();
    const { result } = renderHook(() => useNow(1000));

    act(() => { vi.advanceTimersByTime(3000); });

    // After 3 ticks the value should be ~3000ms ahead of mount
    expect(result.current).toBeGreaterThan(Date.now() - 100);
  });

  it("clears the interval on unmount (no leaked timer)", () => {
    vi.useFakeTimers();
    const clearSpy = vi.spyOn(globalThis, "clearInterval");
    const { unmount } = renderHook(() => useNow(1000));

    unmount();

    expect(clearSpy).toHaveBeenCalledTimes(1);
    clearSpy.mockRestore();
  });

  it("respects a custom interval", () => {
    vi.useFakeTimers();
    const { result } = renderHook(() => useNow(5000));
    const initial = result.current;

    // Not yet — 4999ms elapsed
    act(() => { vi.advanceTimersByTime(4999); });
    expect(result.current).toBe(initial);

    // Now it should fire
    act(() => { vi.advanceTimersByTime(1); });
    expect(result.current).toBeGreaterThan(initial);
  });
});
