import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "../../test/utils";
import { RenameSessionDialog } from "./RenameSessionDialog";

describe("RenameSessionDialog", () => {
  it("renders input with current title", () => {
    render(<RenameSessionDialog currentTitle="Old title" onRename={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByDisplayValue("Old title")).toBeInTheDocument();
  });

  it("renders save and cancel buttons", () => {
    render(<RenameSessionDialog currentTitle="" onRename={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByRole("button", { name: /save/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /cancel/i })).toBeInTheDocument();
  });

  it("calls onRename with new title on save", async () => {
    const user = userEvent.setup();
    const onRename = vi.fn();
    render(<RenameSessionDialog currentTitle="Old" onRename={onRename} onCancel={vi.fn()} />);

    const input = screen.getByDisplayValue("Old");
    await user.clear(input);
    await user.type(input, "New title");
    await user.click(screen.getByRole("button", { name: /save/i }));

    expect(onRename).toHaveBeenCalledWith("New title");
  });

  it("calls onCancel when cancel is clicked", async () => {
    const user = userEvent.setup();
    const onCancel = vi.fn();
    render(<RenameSessionDialog currentTitle="X" onRename={vi.fn()} onCancel={onCancel} />);

    await user.click(screen.getByRole("button", { name: /cancel/i }));
    expect(onCancel).toHaveBeenCalled();
  });

  it("does not call onRename with empty title", async () => {
    const user = userEvent.setup();
    const onRename = vi.fn();
    render(<RenameSessionDialog currentTitle="X" onRename={onRename} onCancel={vi.fn()} />);

    const input = screen.getByDisplayValue("X");
    await user.clear(input);
    await user.click(screen.getByRole("button", { name: /save/i }));

    expect(onRename).not.toHaveBeenCalled();
  });

  it("submits on Enter key", async () => {
    const user = userEvent.setup();
    const onRename = vi.fn();
    render(<RenameSessionDialog currentTitle="Old" onRename={onRename} onCancel={vi.fn()} />);

    const input = screen.getByDisplayValue("Old");
    await user.clear(input);
    await user.type(input, "Enter title{Enter}");

    expect(onRename).toHaveBeenCalledWith("Enter title");
  });
});
