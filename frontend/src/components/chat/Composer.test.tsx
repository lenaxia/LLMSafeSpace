import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "../../test/utils";
import { Composer } from "./Composer";

describe("Composer", () => {
  it("renders textarea with placeholder", () => {
    render(<Composer onSend={vi.fn()} />);
    expect(screen.getByPlaceholderText("Type a message...")).toBeInTheDocument();
  });

  it("renders custom placeholder", () => {
    render(<Composer onSend={vi.fn()} placeholder="Custom..." />);
    expect(screen.getByPlaceholderText("Custom...")).toBeInTheDocument();
  });

  it("renders send button", () => {
    render(<Composer onSend={vi.fn()} />);
    expect(screen.getByRole("button")).toBeInTheDocument();
  });

  it("send button is disabled when textarea is empty", () => {
    render(<Composer onSend={vi.fn()} />);
    expect(screen.getByRole("button")).toBeDisabled();
  });

  it("send button is enabled when textarea has text", async () => {
    const user = userEvent.setup();
    render(<Composer onSend={vi.fn()} />);
    await user.type(screen.getByPlaceholderText("Type a message..."), "hello");
    expect(screen.getByRole("button")).not.toBeDisabled();
  });

  it("calls onSend with trimmed text on submit", async () => {
    const user = userEvent.setup();
    const onSend = vi.fn();
    render(<Composer onSend={onSend} />);
    await user.type(screen.getByPlaceholderText("Type a message..."), "  hello world  ");
    await user.click(screen.getByRole("button"));
    expect(onSend).toHaveBeenCalledWith("hello world");
  });

  it("clears textarea after send", async () => {
    const user = userEvent.setup();
    render(<Composer onSend={vi.fn()} />);
    const textarea = screen.getByPlaceholderText("Type a message...");
    await user.type(textarea, "hello");
    await user.click(screen.getByRole("button"));
    expect(textarea).toHaveValue("");
  });

  it("sends on Enter key (without shift)", async () => {
    const user = userEvent.setup();
    const onSend = vi.fn();
    render(<Composer onSend={onSend} />);
    await user.type(screen.getByPlaceholderText("Type a message..."), "hi{Enter}");
    expect(onSend).toHaveBeenCalledWith("hi");
  });

  it("does not send on Shift+Enter (allows newline)", async () => {
    const user = userEvent.setup();
    const onSend = vi.fn();
    render(<Composer onSend={onSend} />);
    await user.type(screen.getByPlaceholderText("Type a message..."), "line1{Shift>}{Enter}{/Shift}line2");
    expect(onSend).not.toHaveBeenCalled();
  });

  it("is disabled when disabled prop is true", () => {
    render(<Composer onSend={vi.fn()} disabled />);
    expect(screen.getByPlaceholderText("Type a message...")).toBeDisabled();
  });

  it("does not send whitespace-only messages", async () => {
    const user = userEvent.setup();
    const onSend = vi.fn();
    render(<Composer onSend={onSend} />);
    await user.type(screen.getByPlaceholderText("Type a message..."), "   ");
    await user.click(screen.getByRole("button"));
    expect(onSend).not.toHaveBeenCalled();
  });

  // ── Streaming / Queue behavior ─────────────────────────────

  it("textarea is NOT disabled when streaming is true", () => {
    render(<Composer onSend={vi.fn()} streaming />);
    expect(screen.getByPlaceholderText("Type a message...")).not.toBeDisabled();
  });

  it("shows send button (not stop) during streaming", () => {
    render(<Composer onSend={vi.fn()} streaming />);
    expect(screen.getByRole("button", { name: "" })).toBeInTheDocument();
  });

  it("shows stop button during streaming when onAbort is provided", () => {
    render(<Composer onSend={vi.fn()} onAbort={vi.fn()} streaming />);
    expect(screen.getByLabelText("Stop generating")).toBeInTheDocument();
  });

  it("clicking send during streaming calls onSend (queues the message)", async () => {
    const user = userEvent.setup();
    const onSend = vi.fn();
    render(<Composer onSend={onSend} onAbort={vi.fn()} streaming />);
    await user.type(screen.getByPlaceholderText("Type a message..."), "queued msg");
    await user.click(screen.getByRole("button", { name: "" }));
    expect(onSend).toHaveBeenCalledWith("queued msg");
  });

  it("clicking stop during streaming calls onAbort", async () => {
    const user = userEvent.setup();
    const onAbort = vi.fn();
    render(<Composer onSend={vi.fn()} onAbort={onAbort} streaming />);
    await user.click(screen.getByLabelText("Stop generating"));
    expect(onAbort).toHaveBeenCalled();
  });

  it("Enter key works during streaming (does not early-return)", async () => {
    const user = userEvent.setup();
    const onSend = vi.fn();
    render(<Composer onSend={onSend} onAbort={vi.fn()} streaming />);
    await user.type(screen.getByPlaceholderText("Type a message..."), "hello{Enter}");
    expect(onSend).toHaveBeenCalledWith("hello");
  });

  it("does not show queued indicator by default", () => {
    render(<Composer onSend={vi.fn()} />);
    expect(screen.queryByText(/queued/)).not.toBeInTheDocument();
  });
});
