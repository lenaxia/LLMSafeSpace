import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "../../test/utils";
import { ChatView } from "./ChatView";
import type { Message } from "../../api/types";

describe("ChatView", () => {
  const defaultProps = {
    messages: [] as Message[],
    streaming: false,
    streamParts: [] as Array<{ type: "thinking" | "text" | "tool"; text: string }>,
    disabled: false,
    onSend: vi.fn(),
    onAbort: vi.fn(),
  };

  it("renders message list and composer", () => {
    render(<ChatView {...defaultProps} />);
    expect(screen.getByPlaceholderText("Type a message...")).toBeInTheDocument();
  });

  it("renders messages", () => {
    const messages: Message[] = [
      { id: "1", role: "user", parts: [{ type: "text", text: "Hello" }] },
      { id: "2", role: "assistant", parts: [{ type: "text", text: "Hi!" }] },
    ];
    render(<ChatView {...defaultProps} messages={messages} />);
    expect(screen.getByText("Hello")).toBeInTheDocument();
    expect(screen.getByText("Hi!")).toBeInTheDocument();
  });

  it("shows streaming indicator when streaming", () => {
    render(<ChatView {...defaultProps} streaming={true} />);
    const dots = document.querySelectorAll(".animate-bounce");
    expect(dots.length).toBe(3);
  });

  it("shows abort button when streaming", () => {
    render(<ChatView {...defaultProps} streaming={true} />);
    expect(screen.getByRole("button", { name: /stop/i })).toBeInTheDocument();
  });

  it("does not show abort button when not streaming", () => {
    render(<ChatView {...defaultProps} streaming={false} />);
    expect(screen.queryByRole("button", { name: /stop/i })).not.toBeInTheDocument();
  });

  it("calls onAbort when abort button clicked", async () => {
    const user = userEvent.setup();
    const onAbort = vi.fn();
    render(<ChatView {...defaultProps} streaming={true} onAbort={onAbort} />);
    await user.click(screen.getByRole("button", { name: /stop/i }));
    expect(onAbort).toHaveBeenCalled();
  });

  it("calls onSend when message submitted", async () => {
    const user = userEvent.setup();
    const onSend = vi.fn();
    render(<ChatView {...defaultProps} onSend={onSend} />);
    await user.type(screen.getByPlaceholderText("Type a message..."), "test{Enter}");
    expect(onSend).toHaveBeenCalledWith("test");
  });

  it("disables composer when disabled", () => {
    render(<ChatView {...defaultProps} disabled={true} />);
    expect(screen.getByPlaceholderText("Type a message...")).toBeDisabled();
  });

  it("shows streamed text parts", () => {
    render(<ChatView {...defaultProps} streaming={true} streamParts={[{ type: "text", text: "Partial response..." }]} />);
    expect(screen.getByText("Partial response...")).toBeInTheDocument();
  });

  it("shows streamed thinking parts", () => {
    render(<ChatView {...defaultProps} streaming={true} streamParts={[{ type: "thinking", text: "Thinking deeply..." }]} />);
    expect(screen.getByText("Thinking deeply...")).toBeInTheDocument();
  });

  it("does not show streaming bubble when no parts", () => {
    render(<ChatView {...defaultProps} streaming={true} streamParts={[]} />);
    expect(screen.queryByText("Thinking")).not.toBeInTheDocument();
  });
});
