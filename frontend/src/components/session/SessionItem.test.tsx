import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { render } from "../../test/utils";
import { SessionItem } from "./SessionItem";
import type { SessionListItem } from "../../api/types";

describe("SessionItem", () => {
  it("renders session title", () => {
    const session: SessionListItem = { id: "s1", title: "My chat", messageCount: 3, status: "idle", lastMessageAt: new Date().toISOString() };
    render(<SessionItem session={session} selected={false} onSelect={vi.fn()} />);
    expect(screen.getByText("My chat")).toBeInTheDocument();
  });

  it("renders fallback title when title is empty", () => {
    const session: SessionListItem = { id: "s1", messageCount: 3, status: "idle", lastMessageAt: "2026-05-24T10:30:00Z" };
    render(<SessionItem session={session} selected={false} onSelect={vi.fn()} />);
    // Should show "Session at HH:MM" format
    expect(screen.getByText(/Session at/)).toBeInTheDocument();
  });

  it("shows active indicator for active sessions", () => {
    const session: SessionListItem = { id: "s1", title: "Active", messageCount: 1, status: "active" };
    render(<SessionItem session={session} selected={false} onSelect={vi.fn()} />);
    expect(screen.getByLabelText("Active")).toBeInTheDocument();
  });

  it("does not show active indicator for idle sessions", () => {
    const session: SessionListItem = { id: "s1", title: "Idle", messageCount: 1, status: "idle" };
    render(<SessionItem session={session} selected={false} onSelect={vi.fn()} />);
    expect(screen.queryByLabelText("Active")).not.toBeInTheDocument();
  });

  it("shows relative time for lastMessageAt", () => {
    const session: SessionListItem = { id: "s1", title: "Test", messageCount: 1, status: "idle", lastMessageAt: new Date(Date.now() - 120_000).toISOString() };
    render(<SessionItem session={session} selected={false} onSelect={vi.fn()} />);
    expect(screen.getByText("2m")).toBeInTheDocument();
  });
});
