import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { render } from "../../test/utils";
import { MessagePart } from "./MessagePart";

describe("MessagePart", () => {
  it("renders user text as plain paragraph", () => {
    render(<MessagePart part={{ type: "text", text: "Hello world" }} isUser={true} />);
    const p = screen.getByText("Hello world");
    expect(p.tagName).toBe("P");
  });

  it("renders assistant text as markdown", () => {
    render(<MessagePart part={{ type: "text", text: "**bold**" }} isUser={false} />);
    expect(screen.getByText("bold")).toBeInTheDocument();
    expect(screen.getByText("bold").tagName).toBe("STRONG");
  });

  it("renders nothing for unknown part type", () => {
    const { container } = render(<MessagePart part={{ type: "image" }} isUser={false} />);
    expect(container.innerHTML).toBe("");
  });

  it("renders nothing when text is empty", () => {
    const { container } = render(<MessagePart part={{ type: "text", text: "" }} isUser={true} />);
    expect(container.innerHTML).toBe("");
  });

  it("sanitizes dangerous HTML in assistant messages", () => {
    render(<MessagePart part={{ type: "text", text: "<script>alert('xss')</script>\n\nsafe text" }} isUser={false} />);
    expect(screen.queryByText("alert('xss')")).not.toBeInTheDocument();
  });

  it("renders GFM tables", () => {
    const table = "| Col A | Col B |\n|-------|-------|\n| 1     | 2     |\n| 3     | 4     |";
    render(<MessagePart part={{ type: "text", text: table }} isUser={false} />);
    expect(screen.getByRole("table")).toBeInTheDocument();
    expect(screen.getByText("Col A")).toBeInTheDocument();
    expect(screen.getByText("4")).toBeInTheDocument();
  });

  it("renders fenced code blocks", () => {
    const code = "```js\nconst x = 1;\n```";
    render(<MessagePart part={{ type: "text", text: code }} isUser={false} />);
    expect(screen.getByText("const x = 1;")).toBeInTheDocument();
    const codeEl = screen.getByText("const x = 1;").closest("code");
    expect(codeEl).toBeInTheDocument();
  });

  it("renders inline code", () => {
    render(<MessagePart part={{ type: "text", text: "Use `npm install` to install" }} isUser={false} />);
    const codeEl = screen.getByText("npm install");
    expect(codeEl.tagName).toBe("CODE");
  });

  it("renders strikethrough (GFM)", () => {
    render(<MessagePart part={{ type: "text", text: "~~deleted~~" }} isUser={false} />);
    const del = screen.getByText("deleted");
    expect(del.tagName).toBe("DEL");
  });

  it("renders thinking part with collapsible details", () => {
    render(<MessagePart part={{ type: "thinking", text: "Let me reason about this" }} isUser={false} />);
    expect(screen.getByText("Thinking")).toBeInTheDocument();
    expect(screen.getByText("Let me reason about this")).toBeInTheDocument();
  });

  it("renders tool_call part", () => {
    render(<MessagePart part={{ type: "tool_call", text: "search(query: \"hello\")" }} isUser={false} />);
    expect(screen.getByText("Tool call: search")).toBeInTheDocument();
    expect(screen.getByText('(query: "hello")')).toBeInTheDocument();
  });

  it("renders tool_use part with name and input", () => {
    render(<MessagePart part={{ type: "tool_use", name: "read_file", input: { path: "/foo" } }} isUser={false} />);
    expect(screen.getByText("Tool call: read_file")).toBeInTheDocument();
    expect(screen.getByText((content) => content.includes('"path"') && content.includes("/foo"))).toBeInTheDocument();
  });

  it("renders tool_result part", () => {
    render(<MessagePart part={{ type: "tool_result", text: "Found 3 results" }} isUser={false} />);
    expect(screen.getByText("Tool result")).toBeInTheDocument();
    expect(screen.getByText("Found 3 results")).toBeInTheDocument();
  });

  it("renders tool_use part with empty text during streaming", () => {
    render(<MessagePart part={{ type: "tool_use", text: "" }} isUser={false} isStreaming={true} />);
    expect(screen.getByText("Tool call: tool")).toBeInTheDocument();
  });

  it("renders tool_use part with empty text when not streaming", () => {
    render(<MessagePart part={{ type: "tool_use", text: "" }} isUser={false} isStreaming={false} />);
    expect(screen.getByText("Tool call: tool")).toBeInTheDocument();
  });
});
