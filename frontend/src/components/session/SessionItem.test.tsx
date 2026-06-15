import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { render } from "../../test/utils";
import { SessionItem } from "./SessionItem";
import type { SessionListItem } from "../../api/types";

const FIXED_NOW = new Date("2024-06-15T12:00:00Z").getTime();

vi.mock("../../hooks/useNow", () => ({
  useNow: () => FIXED_NOW,
}));

describe("SessionItem", () => {
  it("renders session title", () => {
    const session: SessionListItem = { id: "s1", title: "My chat", messageCount: 3, status: "idle", lastMessageAt: new Date(FIXED_NOW).toISOString(), hasUnread: false };
    render(<SessionItem session={session} selected={false} onSelect={vi.fn()} />);
    expect(screen.getByText("My chat")).toBeInTheDocument();
  });

  it("renders fallback title when title is empty", () => {
    const twoHoursAgo = new Date(FIXED_NOW - 120 * 60_000).toISOString();
    const session: SessionListItem = { id: "s1", messageCount: 3, status: "idle", lastMessageAt: twoHoursAgo, hasUnread: false };
    render(<SessionItem session={session} selected={false} onSelect={vi.fn()} />);
    expect(screen.getByText("New chat")).toBeInTheDocument();
  });

  it("shows active indicator for active sessions", () => {
    const session: SessionListItem = { id: "s1", title: "Active", messageCount: 1, status: "active", hasUnread: false };
    render(<SessionItem session={session} selected={false} onSelect={vi.fn()} />);
    expect(screen.getByLabelText("Active")).toBeInTheDocument();
  });

  it("does not show active indicator for idle sessions", () => {
    const session: SessionListItem = { id: "s1", title: "Idle", messageCount: 1, status: "idle", hasUnread: false };
    render(<SessionItem session={session} selected={false} onSelect={vi.fn()} />);
    expect(screen.queryByLabelText("Active")).not.toBeInTheDocument();
  });

  it("shows relative time for lastMessageAt", () => {
    const twoMinAgo = new Date(FIXED_NOW - 120_000).toISOString();
    const session: SessionListItem = { id: "s1", title: "Test", messageCount: 1, status: "idle", lastMessageAt: twoMinAgo, hasUnread: false };
    render(<SessionItem session={session} selected={false} onSelect={vi.fn()} />);
    expect(screen.getByText("2m")).toBeInTheDocument();
  });
});
