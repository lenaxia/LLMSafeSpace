import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "../../test/utils";
import { WorkspaceItem } from "./WorkspaceItem";
import type { WorkspaceListItem } from "../../api/types";

const activeWs: WorkspaceListItem = {
  id: "ws-1", name: "alpha", userId: "u1", runtime: "python:3.11",
  storageSize: "5Gi", createdAt: "", updatedAt: "", phase: "Active",
};

const suspendedWs: WorkspaceListItem = {
  id: "ws-2", name: "beta", userId: "u1", runtime: "node:20",
  storageSize: "5Gi", createdAt: "", updatedAt: "", phase: "Suspended",
};

describe("WorkspaceItem", () => {
  it("renders workspace name", () => {
    render(<WorkspaceItem workspace={activeWs} selected={false} onSelect={vi.fn()} />);
    expect(screen.getByText("alpha")).toBeInTheDocument();
  });

  it("shows green indicator for active workspace", () => {
    render(<WorkspaceItem workspace={activeWs} selected={false} onSelect={vi.fn()} />);
    const svg = screen.getByText("alpha").parentElement?.querySelector("svg");
    expect(svg?.getAttribute("class")).toContain("fill-green-500");
  });

  it("shows muted indicator for suspended workspace", () => {
    render(<WorkspaceItem workspace={suspendedWs} selected={false} onSelect={vi.fn()} />);
    const svg = screen.getByText("beta").parentElement?.querySelector("svg");
    expect(svg?.getAttribute("class")).toContain("fill-muted-foreground/40");
  });

  it("shows phase text for non-active workspace", () => {
    render(<WorkspaceItem workspace={suspendedWs} selected={false} onSelect={vi.fn()} />);
    expect(screen.getByText("Suspended")).toBeInTheDocument();
  });

  it("does not show phase text for active workspace", () => {
    render(<WorkspaceItem workspace={activeWs} selected={false} onSelect={vi.fn()} />);
    expect(screen.queryByText("Active")).not.toBeInTheDocument();
  });

  it("applies selected styles", () => {
    render(<WorkspaceItem workspace={activeWs} selected={true} onSelect={vi.fn()} />);
    const btn = screen.getByRole("button");
    expect(btn.className).toContain("bg-accent");
    expect(btn).toHaveAttribute("aria-current", "page");
  });

  it("calls onSelect when clicked", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<WorkspaceItem workspace={activeWs} selected={false} onSelect={onSelect} />);
    await user.click(screen.getByRole("button"));
    expect(onSelect).toHaveBeenCalled();
  });
});
