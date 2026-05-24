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
});
