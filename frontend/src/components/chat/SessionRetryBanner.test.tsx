import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { screen, act } from "@testing-library/react";
import { render } from "../../test/utils";
import { SessionRetryBanner } from "./SessionRetryBanner";
import type { RetryStatus } from "./SessionRetryBanner";

function makeStatus(overrides: Partial<RetryStatus> = {}): RetryStatus {
  return {
    attempt: 1,
    message: "Rate limited",
    next: Date.now() + 5000, // 5 seconds from now
    ...overrides,
  };
}

describe("SessionRetryBanner", () => {
  beforeEach(() => { vi.useFakeTimers(); });
  afterEach(() => { vi.useRealTimers(); });

  // ── Happy path ──────────────────────────────────────────────────────────

  it("renders the retry message", () => {
    render(<SessionRetryBanner status={makeStatus({ message: "Rate limited" })} />);
    expect(screen.getByText(/rate limited/i)).toBeInTheDocument();
  });

  it("shows attempt number when attempt > 1", () => {
    render(<SessionRetryBanner status={makeStatus({ attempt: 3 })} />);
    expect(screen.getByText(/attempt 3/i)).toBeInTheDocument();
  });

  it("does not show attempt number on first attempt", () => {
    render(<SessionRetryBanner status={makeStatus({ attempt: 1 })} />);
    expect(screen.queryByText(/attempt 1/i)).not.toBeInTheDocument();
  });

  it("shows countdown in seconds computed from epoch timestamp", () => {
    // next = now + 8000ms → should show 8s
    render(<SessionRetryBanner status={makeStatus({ next: Date.now() + 8000 })} />);
    expect(screen.getByText(/8s/)).toBeInTheDocument();
  });

  it("counts down over time", () => {
    render(<SessionRetryBanner status={makeStatus({ next: Date.now() + 5000 })} />);
    expect(screen.getByText(/5s/)).toBeInTheDocument();

    act(() => { vi.advanceTimersByTime(1000); });
    expect(screen.getByText(/4s/)).toBeInTheDocument();

    act(() => { vi.advanceTimersByTime(2000); });
    expect(screen.getByText(/2s/)).toBeInTheDocument();
  });

  it("stops showing countdown when it reaches zero", () => {
    render(<SessionRetryBanner status={makeStatus({ next: Date.now() + 1000 })} />);
    act(() => { vi.advanceTimersByTime(2000); });
    expect(screen.queryByText(/0s/)).not.toBeInTheDocument();
    expect(screen.queryByText(/retrying in/i)).not.toBeInTheDocument();
  });

  it("resets countdown when status.next changes (new attempt)", () => {
    const { rerender } = render(<SessionRetryBanner status={makeStatus({ attempt: 1, next: Date.now() + 3000 })} />);
    act(() => { vi.advanceTimersByTime(2000); });
    expect(screen.getByText(/1s/)).toBeInTheDocument();

    // New attempt arrives with a fresh next timestamp
    rerender(<SessionRetryBanner status={makeStatus({ attempt: 2, next: Date.now() + 5000 })} />);
    expect(screen.getByText(/5s/)).toBeInTheDocument();
  });

  it("renders action link when action is provided", () => {
    const status = makeStatus({
      action: {
        reason: "free_tier",
        provider: "opencode",
        title: "Upgrade",
        message: "You are on the free tier",
        label: "Upgrade plan",
        link: "https://example.com/upgrade",
      },
    });
    render(<SessionRetryBanner status={status} />);
    const link = screen.getByRole("link", { name: /upgrade plan/i });
    expect(link).toBeInTheDocument();
    expect(link).toHaveAttribute("href", "https://example.com/upgrade");
    expect(link).toHaveAttribute("target", "_blank");
  });

  it("does not render action link when action has no link", () => {
    const status = makeStatus({
      action: {
        reason: "rate_limit",
        provider: "anthropic",
        title: "Rate limited",
        message: "Too many requests",
        label: "Learn more",
        // no link field
      },
    });
    render(<SessionRetryBanner status={status} />);
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });

  it("does not render action link when no action provided", () => {
    render(<SessionRetryBanner status={makeStatus()} />);
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });

  // ── Unhappy / edge cases ─────────────────────────────────────────────────

  it("handles next timestamp in the past gracefully (shows no countdown)", () => {
    // next already passed — remaining should clamp to 0
    render(<SessionRetryBanner status={makeStatus({ next: Date.now() - 1000 })} />);
    expect(screen.queryByText(/retrying in/i)).not.toBeInTheDocument();
  });

  it("renders empty message without crashing", () => {
    const { container } = render(<SessionRetryBanner status={makeStatus({ message: "" })} />);
    // Banner div should still be present — no throw
    expect(container.firstChild).not.toBeNull();
  });
});
