import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "../../test/utils";
import { RenameWorkspaceDialog } from "./RenameWorkspaceDialog";

describe("RenameWorkspaceDialog", () => {
  it("renders with current name", () => {
    render(<RenameWorkspaceDialog currentName="my-workspace" onRename={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByDisplayValue("my-workspace")).toBeInTheDocument();
    expect(screen.getByText("Rename Workspace")).toBeInTheDocument();
  });

  it("calls onRename with trimmed value on save", async () => {
    const user = userEvent.setup();
    const onRename = vi.fn();
    render(<RenameWorkspaceDialog currentName="old" onRename={onRename} onCancel={vi.fn()} />);
    const input = screen.getByDisplayValue("old");
    await user.clear(input);
    await user.type(input, "new-name");
    await user.click(screen.getByText("Save"));
    expect(onRename).toHaveBeenCalledWith("new-name");
  });

  it("calls onCancel when cancel clicked", async () => {
    const user = userEvent.setup();
    const onCancel = vi.fn();
    render(<RenameWorkspaceDialog currentName="old" onRename={vi.fn()} onCancel={onCancel} />);
    await user.click(screen.getByText("Cancel"));
    expect(onCancel).toHaveBeenCalled();
  });

  it("submits on Enter key", async () => {
    const user = userEvent.setup();
    const onRename = vi.fn();
    render(<RenameWorkspaceDialog currentName="old" onRename={onRename} onCancel={vi.fn()} />);
    const input = screen.getByDisplayValue("old");
    await user.clear(input);
    await user.type(input, "new-name{Enter}");
    expect(onRename).toHaveBeenCalledWith("new-name");
  });

  it("does not call onRename with empty name", async () => {
    const user = userEvent.setup();
    const onRename = vi.fn();
    render(<RenameWorkspaceDialog currentName="old" onRename={onRename} onCancel={vi.fn()} />);
    const input = screen.getByDisplayValue("old");
    await user.clear(input);
    await user.click(screen.getByText("Save"));
    expect(onRename).not.toHaveBeenCalled();
  });
});
