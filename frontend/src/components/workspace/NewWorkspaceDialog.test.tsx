import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "../../test/utils";
import { NewWorkspaceDialog } from "./NewWorkspaceDialog";

describe("NewWorkspaceDialog", () => {
  it("renders create and cancel buttons", () => {
    render(<NewWorkspaceDialog onCreate={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByRole("button", { name: /create/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /cancel/i })).toBeInTheDocument();
  });

  it("calls onCreate with auto-generated name on create click", async () => {
    const user = userEvent.setup();
    const onCreate = vi.fn();
    render(<NewWorkspaceDialog onCreate={onCreate} onCancel={vi.fn()} />);

    await user.click(screen.getByRole("button", { name: /create/i }));

    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ name: expect.any(String) }));
    expect(onCreate.mock.calls[0]![0].name.length).toBeGreaterThan(5);
  });

  it("calls onCancel when cancel clicked", async () => {
    const user = userEvent.setup();
    const onCancel = vi.fn();
    render(<NewWorkspaceDialog onCreate={vi.fn()} onCancel={onCancel} />);

    await user.click(screen.getByRole("button", { name: /cancel/i }));
    expect(onCancel).toHaveBeenCalled();
  });

  it("disables create button when loading", () => {
    render(<NewWorkspaceDialog onCreate={vi.fn()} onCancel={vi.fn()} loading />);
    expect(screen.getByRole("button", { name: /creating/i })).toBeDisabled();
  });
});
