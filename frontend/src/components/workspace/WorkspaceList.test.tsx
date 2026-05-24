import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "../../test/utils";
import { WorkspaceList } from "./WorkspaceList";
import type { WorkspaceListItem } from "../../api/types";

const mockWorkspaces: WorkspaceListItem[] = [
  { id: "ws-1", name: "alpha", userId: "u1", runtime: "python:3.11", storageSize: "5Gi", createdAt: "", updatedAt: "", phase: "Active" },
  { id: "ws-2", name: "beta", userId: "u1", runtime: "node:20", storageSize: "5Gi", createdAt: "", updatedAt: "", phase: "Suspended" },
];

describe("WorkspaceList", () => {
  it("renders empty state when no workspaces", () => {
    render(<WorkspaceList workspaces={[]} onSelect={vi.fn()} />);
    expect(screen.getByText("No workspaces yet")).toBeInTheDocument();
  });

  it("renders all workspaces", () => {
    render(<WorkspaceList workspaces={mockWorkspaces} onSelect={vi.fn()} />);
    expect(screen.getByText("alpha")).toBeInTheDocument();
    expect(screen.getByText("beta")).toBeInTheDocument();
  });

  it("calls onSelect with workspace id when clicked", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<WorkspaceList workspaces={mockWorkspaces} onSelect={onSelect} />);
    await user.click(screen.getByText("alpha"));
    expect(onSelect).toHaveBeenCalledWith("ws-1");
  });

  it("highlights selected workspace", () => {
    render(<WorkspaceList workspaces={mockWorkspaces} selectedId="ws-1" onSelect={vi.fn()} />);
    const btn = screen.getByText("alpha").closest("button");
    expect(btn?.className).toContain("bg-accent");
  });

  it("shows phase badge for non-active workspaces", () => {
    render(<WorkspaceList workspaces={mockWorkspaces} onSelect={vi.fn()} />);
    expect(screen.getByText("Suspended")).toBeInTheDocument();
  });

  it("has accessible navigation landmark", () => {
    render(<WorkspaceList workspaces={mockWorkspaces} onSelect={vi.fn()} />);
    expect(screen.getByRole("navigation", { name: "Workspaces" })).toBeInTheDocument();
  });
});
