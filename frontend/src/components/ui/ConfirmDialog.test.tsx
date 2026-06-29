import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "../../test/utils";
import { ConfirmDialog } from "./ConfirmDialog";

describe("ConfirmDialog", () => {
  it("renders nothing when closed", () => {
    render(
      <ConfirmDialog
        open={false}
        onOpenChange={() => {}}
        title="Refresh compute?"
        description="d"
        confirmLabel="Refresh"
        onConfirm={() => {}}
      />,
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("renders the dialog when open", () => {
    render(
      <ConfirmDialog
        open
        onOpenChange={() => {}}
        title="Refresh compute?"
        description="d"
        confirmLabel="Refresh"
        onConfirm={() => {}}
      />,
    );
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("renders title, description, and buttons when open", () => {
    render(
      <ConfirmDialog
        open
        onOpenChange={() => {}}
        title="Refresh compute?"
        description="This will halt all current work."
        note="Your files are preserved."
        confirmLabel="Refresh compute"
        onConfirm={() => {}}
      />,
    );
    expect(screen.getByText("Refresh compute?")).toBeInTheDocument();
    expect(screen.getByText("This will halt all current work.")).toBeInTheDocument();
    expect(screen.getByText("Your files are preserved.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Refresh compute" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeInTheDocument();
  });

  it("calls onConfirm when confirm is clicked", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    render(
      <ConfirmDialog
        open
        onOpenChange={() => {}}
        title="t"
        description="d"
        confirmLabel="Confirm"
        onConfirm={onConfirm}
      />,
    );
    await user.click(screen.getByRole("button", { name: "Confirm" }));
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it("calls onOpenChange(false) when cancel is clicked", async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    render(
      <ConfirmDialog
        open
        onOpenChange={onOpenChange}
        title="t"
        description="d"
        confirmLabel="Confirm"
        onConfirm={() => {}}
      />,
    );
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("renders the confirm button in the destructive style when destructive", () => {
    render(
      <ConfirmDialog
        open
        onOpenChange={() => {}}
        title="t"
        description="d"
        confirmLabel="Delete"
        destructive
        onConfirm={() => {}}
      />,
    );
    const confirm = screen.getByRole("button", { name: "Delete" });
    expect(confirm.className).toContain("bg-destructive");
  });

  it("renders the confirm button in the default style when not destructive", () => {
    render(
      <ConfirmDialog
        open
        onOpenChange={() => {}}
        title="t"
        description="d"
        confirmLabel="Refresh"
        onConfirm={() => {}}
      />,
    );
    const confirm = screen.getByRole("button", { name: "Refresh" });
    expect(confirm.className).toContain("bg-primary");
  });
});
