import { describe, expect, it, vi } from "vitest";
import { screen, act } from "@testing-library/react";
import { render } from "../../test/utils";
import { AtCapBanner } from "./AtCapBanner";

describe("AtCapBanner", () => {
  it("renders the cap message", () => {
    render(<AtCapBanner retryAfter={10} onRetry={vi.fn()} />);
    expect(screen.getByText(/session limit reached/i)).toBeInTheDocument();
  });

  it("shows countdown seconds", () => {
    render(<AtCapBanner retryAfter={15} onRetry={vi.fn()} />);
    expect(screen.getByText(/15s/)).toBeInTheDocument();
  });

  it("counts down over time", () => {
    vi.useFakeTimers();
    render(<AtCapBanner retryAfter={5} onRetry={vi.fn()} />);

    act(() => { vi.advanceTimersByTime(1000); });
    expect(screen.getByText(/4s/)).toBeInTheDocument();

    act(() => { vi.advanceTimersByTime(1000); });
    expect(screen.getByText(/3s/)).toBeInTheDocument();

    vi.useRealTimers();
  });

  it("calls onRetry when countdown reaches zero", () => {
    vi.useFakeTimers();
    const onRetry = vi.fn();
    render(<AtCapBanner retryAfter={2} onRetry={onRetry} />);

    act(() => { vi.advanceTimersByTime(2000); });
    expect(onRetry).toHaveBeenCalled();

    vi.useRealTimers();
  });

  it("shows retry button", () => {
    render(<AtCapBanner retryAfter={0} onRetry={vi.fn()} />);
    expect(screen.getByRole("button", { name: /retry/i })).toBeInTheDocument();
  });
});
