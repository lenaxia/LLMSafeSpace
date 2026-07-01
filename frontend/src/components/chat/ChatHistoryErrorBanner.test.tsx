import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { ChatHistoryErrorBanner } from "./ChatHistoryErrorBanner";
import { ApiClientError } from "../../api/client";

// LLMSafeSpaces#490: banner renders when useMessageHistory returns
// isError. These tests target the banner in isolation (integration with
// ChatPage.tsx is covered separately by ChatPage.historyError.test.tsx).

describe("ChatHistoryErrorBanner", () => {
  it("renders the user-facing message and Retry action", () => {
    const onRetry = vi.fn();
    render(<ChatHistoryErrorBanner error={new Error("net")} onRetry={onRetry} />);

    expect(screen.getByText("Chat history unavailable")).toBeInTheDocument();
    const retryBtn = screen.getByRole("button", { name: /retry/i });
    fireEvent.click(retryBtn);
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("has role=alert so screen readers announce the failure", () => {
    render(<ChatHistoryErrorBanner error={new Error("x")} onRetry={vi.fn()} />);
    expect(screen.getByRole("alert")).toBeInTheDocument();
  });

  it("shows HTTP status + opencode ref when the error is an ApiClientError with a nested opencode body (GET history shape)", () => {
    // This is the exact shape #486 hit — opencode's raw envelope passed
    // through by the API on GET /message.
    const err = new ApiClientError(500, {
      // ApiError type declares `error` string; opencode's body is
      // structurally different but the type still fits (we typecast).
      error: "Unexpected server error. Check server logs for details.",
      // extra fields injected on the same object for the test — the
      // banner extracts them via extractOpencodeRef.
      // @ts-expect-error — augmenting the typed ApiError body with
      // opencode's nested envelope fields for test fidelity.
      name: "UnknownError",
      data: { ref: "err_b8d02ae9" },
    });
    render(<ChatHistoryErrorBanner error={err} onRetry={vi.fn()} />);

    // Details are collapsed by default; the summary must be interactive.
    const details = screen.getByText("Details");
    fireEvent.click(details);

    expect(screen.getByText("HTTP 500")).toBeInTheDocument();
    expect(screen.getByText(/Unexpected server error/)).toBeInTheDocument();
    expect(screen.getByText("Ref: err_b8d02ae9")).toBeInTheDocument();
  });

  it("shows HTTP status + opencode ref when the error carries the flat allowlisted shape (POST prompt path)", () => {
    // After the API's EnrichChatErrorBody allowlist runs, `ref` sits at
    // the top level of the body.
    const err = new ApiClientError(502, {
      error: "boom",
      // @ts-expect-error — augmenting for test.
      ref: "err_topLevel",
    });
    render(<ChatHistoryErrorBanner error={err} onRetry={vi.fn()} />);
    fireEvent.click(screen.getByText("Details"));

    expect(screen.getByText("HTTP 502")).toBeInTheDocument();
    expect(screen.getByText("Ref: err_topLevel")).toBeInTheDocument();
  });

  it("shows only the message when there is no ref", () => {
    const err = new ApiClientError(503, { error: "workspace connection failed" });
    render(<ChatHistoryErrorBanner error={err} onRetry={vi.fn()} />);
    fireEvent.click(screen.getByText("Details"));

    expect(screen.getByText("HTTP 503")).toBeInTheDocument();
    expect(screen.getByText(/workspace connection failed/)).toBeInTheDocument();
    expect(screen.queryByText(/^Ref:/)).not.toBeInTheDocument();
  });

  it("handles non-ApiClientError errors gracefully (network exception, etc.)", () => {
    // e.g. fetch() itself threw — no response, no body.
    render(<ChatHistoryErrorBanner error={new Error("Failed to fetch")} onRetry={vi.fn()} />);
    fireEvent.click(screen.getByText("Details"));

    expect(screen.getByText(/Failed to fetch/)).toBeInTheDocument();
    // No HTTP status when we don't have one.
    expect(screen.queryByText(/^HTTP /)).not.toBeInTheDocument();
  });

  it("handles a null error message (unknown shape) with a placeholder", () => {
    // Pathological case — react-query surfaced something that isn't an
    // Error at all. Banner still renders (alert role stays, retry still
    // works) so users aren't stuck with a blank chat.
    render(<ChatHistoryErrorBanner error={undefined as unknown} onRetry={vi.fn()} />);
    expect(screen.getByRole("alert")).toBeInTheDocument();
  });
});
