import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueueSection } from "./QueueSection";
import type { QueuedMessage } from "../../hooks/useMessageQueue";

function makeMsg(overrides: Partial<QueuedMessage> = {}): QueuedMessage {
  return {
    id: "msg_test",
    text: "hello",
    sentAt: Date.now(),
    status: "pending",
    ...overrides,
  };
}

describe("QueueSection", () => {
  it("renders nothing when messages array is empty", () => {
    const { container } = render(<QueueSection messages={[]} onRetry={vi.fn()} onDismiss={vi.fn()} />);
    expect(container.innerHTML).toBe("");
  });

  it("renders pending pills with text", () => {
    render(
      <QueueSection
        messages={[makeMsg({ text: "fix the bug" }), makeMsg({ id: "msg_2", text: "add tests" })]}
        onRetry={vi.fn()}
        onDismiss={vi.fn()}
      />,
    );
    expect(screen.getByText("fix the bug")).toBeInTheDocument();
    expect(screen.getByText("add tests")).toBeInTheDocument();
  });

  it("renders error pills with line-through text", () => {
    render(
      <QueueSection
        messages={[makeMsg({ status: "error", error: "failed" })]}
        onRetry={vi.fn()}
        onDismiss={vi.fn()}
      />,
    );
    const el = screen.getByText("hello");
    expect(el.closest("s, [class*=line]")).toBeTruthy();
  });

  it("shows retry button on error pills", () => {
    render(
      <QueueSection
        messages={[makeMsg({ status: "error", error: "failed" })]}
        onRetry={vi.fn()}
        onDismiss={vi.fn()}
      />,
    );
    expect(screen.getByLabelText("Retry")).toBeInTheDocument();
  });

  it("shows dismiss button on error pills", () => {
    render(
      <QueueSection
        messages={[makeMsg({ status: "error", error: "failed" })]}
        onRetry={vi.fn()}
        onDismiss={vi.fn()}
      />,
    );
    expect(screen.getByLabelText("Dismiss")).toBeInTheDocument();
  });

  it("does not show retry/dismiss on pending pills", () => {
    render(
      <QueueSection
        messages={[makeMsg()]}
        onRetry={vi.fn()}
        onDismiss={vi.fn()}
      />,
    );
    expect(screen.queryByLabelText("Retry")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Dismiss")).not.toBeInTheDocument();
  });

  it("retry click calls onRetry with message id", async () => {
    const user = userEvent.setup();
    const onRetry = vi.fn();
    render(
      <QueueSection
        messages={[makeMsg({ status: "error", error: "failed" })]}
        onRetry={onRetry}
        onDismiss={vi.fn()}
      />,
    );
    await user.click(screen.getByLabelText("Retry"));
    expect(onRetry).toHaveBeenCalledWith("msg_test");
  });

  it("dismiss click calls onDismiss with message id", async () => {
    const user = userEvent.setup();
    const onDismiss = vi.fn();
    render(
      <QueueSection
        messages={[makeMsg({ status: "error", error: "failed" })]}
        onRetry={vi.fn()}
        onDismiss={onDismiss}
      />,
    );
    await user.click(screen.getByLabelText("Dismiss"));
    expect(onDismiss).toHaveBeenCalledWith("msg_test");
  });

  it("truncates long text with ellipsis", () => {
    const longText = "a".repeat(200);
    render(
      <QueueSection
        messages={[makeMsg({ text: longText })]}
        onRetry={vi.fn()}
        onDismiss={vi.fn()}
      />,
    );
    const el = screen.getByText(longText);
    expect(el.className).toContain("truncate");
  });
});
