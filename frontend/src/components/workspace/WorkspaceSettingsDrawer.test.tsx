import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { WorkspaceSettingsDrawer } from "./WorkspaceSettingsDrawer";
import type { WorkspaceListItem } from "../../api/types";

const mockWorkspace: WorkspaceListItem = {
  id: "ws-1",
  name: "Test Workspace",
  userId: "user-1",
  runtime: "base",
  storageSize: "5Gi",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  phase: "Active",
  maxActiveSessions: 5,
};

describe("WorkspaceSettingsDrawer", () => {
  it("renders when open", () => {
    render(
      <WorkspaceSettingsDrawer
        workspace={mockWorkspace}
        open={true}
        onOpenChange={vi.fn()}
        onSave={vi.fn()}
      />,
    );
    expect(screen.getByText("Workspace Settings")).toBeInTheDocument();
    expect(screen.getByText("Test Workspace")).toBeInTheDocument();
  });

  it("does not render when closed", () => {
    render(
      <WorkspaceSettingsDrawer
        workspace={mockWorkspace}
        open={false}
        onOpenChange={vi.fn()}
        onSave={vi.fn()}
      />,
    );
    expect(screen.queryByText("Workspace Settings")).not.toBeInTheDocument();
  });

  it("shows auto-suspend toggle defaulting to on", () => {
    render(
      <WorkspaceSettingsDrawer
        workspace={mockWorkspace}
        open={true}
        onOpenChange={vi.fn()}
        onSave={vi.fn()}
      />,
    );
    expect(screen.getByRole("switch")).toBeInTheDocument();
  });

  it("shows idle timeout when auto-suspend is enabled", () => {
    render(
      <WorkspaceSettingsDrawer
        workspace={mockWorkspace}
        open={true}
        onOpenChange={vi.fn()}
        onSave={vi.fn()}
      />,
    );
    expect(screen.getByLabelText("Idle Timeout (min)")).toBeInTheDocument();
  });

  it("calls onSave with settings when Save is clicked", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <WorkspaceSettingsDrawer
        workspace={mockWorkspace}
        open={true}
        onOpenChange={vi.fn()}
        onSave={onSave}
      />,
    );

    fireEvent.click(screen.getByText("Save"));

    await waitFor(() => {
      expect(onSave).toHaveBeenCalledWith({
        autoSuspendEnabled: true,
        autoSuspendIdleMinutes: 60,
      });
    });
  });

  it("shows error when onSave fails", async () => {
    const onSave = vi.fn().mockRejectedValue(new Error("Network error"));
    render(
      <WorkspaceSettingsDrawer
        workspace={mockWorkspace}
        open={true}
        onOpenChange={vi.fn()}
        onSave={onSave}
      />,
    );

    fireEvent.click(screen.getByText("Save"));

    await waitFor(() => {
      expect(screen.getByText("Network error")).toBeInTheDocument();
    });
  });

  it("closes drawer on successful save", async () => {
    const onOpenChange = vi.fn();
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <WorkspaceSettingsDrawer
        workspace={mockWorkspace}
        open={true}
        onOpenChange={onOpenChange}
        onSave={onSave}
      />,
    );

    fireEvent.click(screen.getByText("Save"));

    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false);
    });
  });

  it("disables save button while saving", async () => {
    const onSave = vi.fn().mockImplementation(() => new Promise(() => {})); // never resolves
    render(
      <WorkspaceSettingsDrawer
        workspace={mockWorkspace}
        open={true}
        onOpenChange={vi.fn()}
        onSave={onSave}
      />,
    );

    fireEvent.click(screen.getByText("Save"));
    expect(screen.getByText("Saving...")).toBeInTheDocument();
  });

  it("drawer content has max-w-full to prevent overflow on narrow screens", () => {
    render(
      <WorkspaceSettingsDrawer
        workspace={mockWorkspace}
        open={true}
        onOpenChange={vi.fn()}
        onSave={vi.fn()}
      />,
    );
    const content = screen.getByText("Workspace Settings").closest("[class*='max-w-full']");
    expect(content).not.toBeNull();
  });
});
