import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "../../test/utils";
import { NewWorkspaceDialog } from "./NewWorkspaceDialog";

describe("NewWorkspaceDialog", () => {
  it("renders name input and runtime select", () => {
    render(<NewWorkspaceDialog onCreate={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByPlaceholderText("Workspace name")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /create/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /cancel/i })).toBeInTheDocument();
  });

  it("calls onCreate with name and runtime", async () => {
    const user = userEvent.setup();
    const onCreate = vi.fn();
    render(<NewWorkspaceDialog onCreate={onCreate} onCancel={vi.fn()} />);

    await user.type(screen.getByPlaceholderText("Workspace name"), "my-project");
    await user.click(screen.getByRole("button", { name: /create/i }));

    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ name: "my-project" }));
  });

  it("does not submit with empty name", async () => {
    const user = userEvent.setup();
    const onCreate = vi.fn();
    render(<NewWorkspaceDialog onCreate={onCreate} onCancel={vi.fn()} />);

    await user.click(screen.getByRole("button", { name: /create/i }));
    expect(onCreate).not.toHaveBeenCalled();
  });

  it("calls onCancel when cancel clicked", async () => {
    const user = userEvent.setup();
    const onCancel = vi.fn();
    render(<NewWorkspaceDialog onCreate={vi.fn()} onCancel={onCancel} />);

    await user.click(screen.getByRole("button", { name: /cancel/i }));
    expect(onCancel).toHaveBeenCalled();
  });
});
