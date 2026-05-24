import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
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

  it("renders all messages", () => {
    render(<MessageList messages={messages} />);
    expect(screen.getByText("Hello")).toBeInTheDocument();
    expect(screen.getByText("Hi!")).toBeInTheDocument();
    expect(screen.getByText("How are you?")).toBeInTheDocument();
  });

  it("renders messages in order", () => {
    render(<MessageList messages={messages} />);
    const texts = screen.getAllByText(/Hello|Hi!|How are you\?/).map((el) => el.textContent);
    expect(texts).toEqual(["Hello", "Hi!", "How are you?"]);
  });
});
