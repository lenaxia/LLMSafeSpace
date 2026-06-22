import { describe, it, expect, vi } from "vitest";
import { fireEvent } from "@testing-library/react";
import { render } from "../../test/utils";
import { AgentPrompt } from "./AgentPrompt";

describe("AgentPrompt", () => {
  it("renders with role=dialog and permission aria-label", () => {
    render(
      <AgentPrompt variant="permission">
        <span>body</span>
      </AgentPrompt>,
    );
    const dialog = document.querySelector("[role='dialog']");
    expect(dialog).not.toBeNull();
    expect(dialog).toHaveAttribute("aria-label", "Permission required");
  });

  it("renders with role=dialog and question aria-label", () => {
    render(
      <AgentPrompt variant="question">
        <span>body</span>
      </AgentPrompt>,
    );
    const dialog = document.querySelector("[role='dialog']");
    expect(dialog).not.toBeNull();
    expect(dialog).toHaveAttribute("aria-label", "Agent has a question");
  });

  it("renders default title for permission variant", () => {
    render(
      <AgentPrompt variant="permission">
        <span>body</span>
      </AgentPrompt>,
    );
    expect(document.querySelector("[role='dialog']")?.textContent).toContain("Permission required");
  });

  it("renders default title for question variant", () => {
    render(
      <AgentPrompt variant="question">
        <span>body</span>
      </AgentPrompt>,
    );
    expect(document.querySelector("[role='dialog']")?.textContent).toContain("Agent has a question");
  });

  it("renders custom title when provided", () => {
    render(
      <AgentPrompt variant="permission" title="Custom title here">
        <span>body</span>
      </AgentPrompt>,
    );
    expect(document.querySelector("[role='dialog']")?.textContent).toContain("Custom title here");
  });

  it("does not render dismiss button when onDismiss is not provided", () => {
    render(
      <AgentPrompt variant="permission">
        <span>body</span>
      </AgentPrompt>,
    );
    expect(document.querySelector("[role='dialog']")?.querySelector("button[aria-label='Dismiss']")).toBeNull();
  });

  it("renders dismiss button when onDismiss is provided", () => {
    render(
      <AgentPrompt variant="question" onDismiss={vi.fn()}>
        <span>body</span>
      </AgentPrompt>,
    );
    const dismissBtn = document.querySelector("[role='dialog']")?.querySelector("button[aria-label='Dismiss']");
    expect(dismissBtn).not.toBeNull();
  });

  it("dismiss button calls onDismiss when clicked", () => {
    const onDismiss = vi.fn();
    render(
      <AgentPrompt variant="question" onDismiss={onDismiss}>
        <span>body</span>
      </AgentPrompt>,
    );
    const dismissBtn = document.querySelector("[role='dialog']")?.querySelector("button[aria-label='Dismiss']") as HTMLElement;
    fireEvent.click(dismissBtn);
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it("dismiss button is disabled when dismissDisabled is true", () => {
    render(
      <AgentPrompt variant="question" onDismiss={vi.fn()} dismissDisabled>
        <span>body</span>
      </AgentPrompt>,
    );
    const dismissBtn = document.querySelector("[role='dialog']")?.querySelector("button[aria-label='Dismiss']") as HTMLButtonElement;
    expect(dismissBtn.disabled).toBe(true);
  });

  it("renders children inside the card", () => {
    render(
      <AgentPrompt variant="permission">
        <div data-testid="child-content">child text</div>
      </AgentPrompt>,
    );
    const child = document.querySelector("[role='dialog']")?.querySelector("[data-testid='child-content']");
    expect(child).not.toBeNull();
    expect(child?.textContent).toBe("child text");
  });

  it("permission variant applies amber theme classes", () => {
    render(
      <AgentPrompt variant="permission">
        <span>body</span>
      </AgentPrompt>,
    );
    const card = document.querySelector("[role='dialog']") as HTMLElement;
    expect(card.className).toContain("border-amber-300");
    expect(card.className).toContain("bg-amber-50");
  });

  it("question variant applies blue theme classes", () => {
    render(
      <AgentPrompt variant="question">
        <span>body</span>
      </AgentPrompt>,
    );
    const card = document.querySelector("[role='dialog']") as HTMLElement;
    expect(card.className).toContain("border-blue-300");
    expect(card.className).toContain("bg-blue-50");
  });
});
