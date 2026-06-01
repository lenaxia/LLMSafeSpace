import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { render } from "../../test/utils";
import { MessageBubble } from "./MessageBubble";
import type { Message } from "../../api/types";

describe("MessageBubble", () => {
  it("renders user message with primary background", () => {
    const msg: Message = { id: "1", role: "user", parts: [{ type: "text", text: "Hello" }] };
    render(<MessageBubble message={msg} />);
    expect(screen.getByText("Hello")).toBeInTheDocument();
    const bubble = screen.getByText("Hello").closest("div[class*='bg-']");
    expect(bubble?.className).toContain("bg-primary");
  });

  it("renders assistant message with muted background", () => {
    const msg: Message = { id: "2", role: "assistant", parts: [{ type: "text", text: "Hi there" }] };
    render(<MessageBubble message={msg} />);
    expect(screen.getByText("Hi there")).toBeInTheDocument();
    const bubble = screen.getByText("Hi there").closest("div[class*='bg-']");
    expect(bubble?.className).toContain("bg-muted");
  });

  it("aligns user messages to the right", () => {
    const msg: Message = { id: "1", role: "user", parts: [{ type: "text", text: "Right" }] };
    render(<MessageBubble message={msg} />);
    const wrapper = screen.getByText("Right").closest("div[class*='justify-']");
    expect(wrapper?.className).toContain("justify-end");
  });

  it("aligns assistant messages to the left", () => {
    const msg: Message = { id: "2", role: "assistant", parts: [{ type: "text", text: "Left" }] };
    render(<MessageBubble message={msg} />);
    const wrapper = screen.getByText("Left").closest("div[class*='justify-']");
    expect(wrapper?.className).toContain("justify-start");
  });

  it("renders multiple parts", () => {
    const msg: Message = { id: "3", role: "assistant", parts: [{ type: "text", text: "Part 1" }, { type: "text", text: "Part 2" }] };
    render(<MessageBubble message={msg} />);
    expect(screen.getByText("Part 1")).toBeInTheDocument();
    expect(screen.getByText("Part 2")).toBeInTheDocument();
  });

  describe("overflow containment", () => {
    it("has overflow-hidden to prevent content from escaping bubble", () => {
      const msg: Message = { id: "4", role: "assistant", parts: [{ type: "text", text: "content" }] };
      render(<MessageBubble message={msg} />);
      const bubble = screen.getByText("content").closest("div[class*='overflow-hidden']");
      expect(bubble).not.toBeNull();
    });

    it("has min-w-0 to allow flex shrinking below content width", () => {
      const msg: Message = { id: "5", role: "assistant", parts: [{ type: "text", text: "content" }] };
      render(<MessageBubble message={msg} />);
      const bubble = screen.getByText("content").closest("div[class*='min-w-0']");
      expect(bubble).not.toBeNull();
    });

    it("has break-words to wrap long unbroken strings", () => {
      const msg: Message = { id: "6", role: "user", parts: [{ type: "text", text: "content" }] };
      render(<MessageBubble message={msg} />);
      const bubble = screen.getByText("content").closest("div[class*='break-words']");
      expect(bubble).not.toBeNull();
    });

    it("uses responsive max-width (90% mobile, 80% desktop)", () => {
      const msg: Message = { id: "7", role: "assistant", parts: [{ type: "text", text: "content" }] };
      const { container } = render(<MessageBubble message={msg} />);
      const bubble = container.querySelector("div[class*='max-w-\\[90\\%\\]']");
      expect(bubble).not.toBeNull();
      expect(bubble?.className).toContain("sm:max-w-[80%]");
    });
  });
});
