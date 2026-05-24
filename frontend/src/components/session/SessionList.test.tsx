import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "../../test/utils";
import { SessionList } from "./SessionList";
import type { SessionListItem } from "../../api/types";

const mockSessions: SessionListItem[] = [
  { id: "s1", title: "Refactor auth", lastMessageAt: new Date().toISOString(), messageCount: 5, status: "active" },
  { id: "s2", title: "Fix bug", lastMessageAt: new Date(Date.now() - 3600_000).toISOString(), messageCount: 2, status: "idle" },
];

describe("SessionList", () => {
  it("renders empty state when no sessions", () => {
    render(<SessionList sessions={[]} onSelect={vi.fn()} />);
    expect(screen.getByText("No sessions yet")).toBeInTheDocument();
  });

  it("renders all sessions", () => {
    render(<SessionList sessions={mockSessions} onSelect={vi.fn()} />);
    expect(screen.getByText("Refactor auth")).toBeInTheDocument();
    expect(screen.getByText("Fix bug")).toBeInTheDocument();
  });

  it("calls onSelect with session id", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<SessionList sessions={mockSessions} onSelect={onSelect} />);
    await user.click(screen.getByText("Refactor auth"));
    expect(onSelect).toHaveBeenCalledWith("s1");
  });

  it("highlights selected session", () => {
    render(<SessionList sessions={mockSessions} selectedId="s1" onSelect={vi.fn()} />);
    const btn = screen.getByText("Refactor auth").closest("button");
    expect(btn?.className).toContain("bg-accent");
  });
});
