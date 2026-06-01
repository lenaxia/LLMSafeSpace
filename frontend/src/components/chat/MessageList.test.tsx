import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { render } from "../../test/utils";
import { MessageList } from "./MessageList";
import type { Message } from "../../api/types";

const messages: Message[] = [
  { id: "1", role: "user", parts: [{ type: "text", text: "Hello" }] },
  { id: "2", role: "assistant", parts: [{ type: "text", text: "Hi!" }] },
  { id: "3", role: "user", parts: [{ type: "text", text: "How are you?" }] },
];

describe("MessageList", () => {
  it("renders empty state when no messages", () => {
    render(<MessageList messages={[]} />);
    expect(screen.getByText("Send a message to start the conversation")).toBeInTheDocument();
  });

  it("renders messages", () => {
    render(<MessageList messages={messages} />);
    expect(screen.getByText("Hello")).toBeInTheDocument();
    expect(screen.getByText("Hi!")).toBeInTheDocument();
    expect(screen.getByText("How are you?")).toBeInTheDocument();
  });

  it("has accessible log role", () => {
    render(<MessageList messages={messages} />);
    expect(screen.getByRole("log")).toBeInTheDocument();
  });

  it("has aria-live polite for screen readers", () => {
    render(<MessageList messages={messages} />);
    expect(screen.getByRole("log")).toHaveAttribute("aria-live", "polite");
  });

  it("does not show jump-to-bottom button when at bottom", () => {
    render(<MessageList messages={messages} />);
    expect(screen.queryByLabelText("Scroll to bottom")).not.toBeInTheDocument();
  });

  it("shows jump-to-bottom button when scrolled away from bottom", async () => {
    render(<MessageList messages={messages} />);
    const scrollContainer = screen.getByRole("log");
    Object.defineProperty(scrollContainer, "scrollHeight", { value: 1000, configurable: true });
    Object.defineProperty(scrollContainer, "clientHeight", { value: 200, configurable: true });
    Object.defineProperty(scrollContainer, "scrollTop", { value: 0, writable: true, configurable: true });
    scrollContainer.dispatchEvent(new Event("scroll"));
    await waitFor(() => {
      expect(screen.getByLabelText("Scroll to bottom")).toBeInTheDocument();
    });
  });

  it("button says 'Resume tailing' during streaming", async () => {
    render(<MessageList messages={messages} streaming={true} streamingBubble={<div>streaming...</div>} />);
    const scrollContainer = screen.getByRole("log");
    Object.defineProperty(scrollContainer, "scrollHeight", { value: 1000, configurable: true });
    Object.defineProperty(scrollContainer, "clientHeight", { value: 200, configurable: true });
    Object.defineProperty(scrollContainer, "scrollTop", { value: 0, writable: true, configurable: true });
    scrollContainer.dispatchEvent(new Event("scroll"));
    await waitFor(() => {
      expect(screen.getByText("Resume tailing")).toBeInTheDocument();
    });
  });

  it("button says 'Jump to bottom' when not streaming", async () => {
    render(<MessageList messages={messages} />);
    const scrollContainer = screen.getByRole("log");
    Object.defineProperty(scrollContainer, "scrollHeight", { value: 1000, configurable: true });
    Object.defineProperty(scrollContainer, "clientHeight", { value: 200, configurable: true });
    Object.defineProperty(scrollContainer, "scrollTop", { value: 0, writable: true, configurable: true });
    scrollContainer.dispatchEvent(new Event("scroll"));
    await waitFor(() => {
      expect(screen.getByText("Jump to bottom")).toBeInTheDocument();
    });
  });

  it("renders streaming bubble when provided", () => {
    render(<MessageList messages={messages} streaming={true} streamingBubble={<div data-testid="stream-bubble">streaming content</div>} />);
    expect(screen.getByTestId("stream-bubble")).toBeInTheDocument();
  });

  it("prevents horizontal scroll on the scroll container", () => {
    render(<MessageList messages={messages} />);
    const scrollContainer = screen.getByRole("log");
    expect(scrollContainer.className).toContain("overflow-x-hidden");
  });

  it("renders load earlier button when hasOlderMessages is true", () => {
    render(<MessageList messages={messages} hasOlderMessages={true} />);
    expect(screen.getByText("Load earlier messages")).toBeInTheDocument();
  });

  it("shows spinner when loading older messages", () => {
    render(<MessageList messages={messages} hasOlderMessages={true} loadingOlder={true} />);
    expect(screen.queryByText("Load earlier messages")).not.toBeInTheDocument();
    expect(document.querySelector(".animate-spin")).toBeInTheDocument();
  });
});
