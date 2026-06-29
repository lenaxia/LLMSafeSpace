import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "../../test/utils";
import { KebabMenu } from "./KebabMenu";

describe("KebabMenu", () => {
  it("renders trigger button", () => {
    render(<KebabMenu items={[{ label: "Action", onClick: vi.fn() }]} />);
    expect(screen.getByLabelText("Actions")).toBeInTheDocument();
  });

  it("shows menu items when clicked", async () => {
    const user = userEvent.setup();
    render(<KebabMenu items={[{ label: "Action", onClick: vi.fn() }]} />);
    await user.click(screen.getByLabelText("Actions"));
    expect(screen.getByRole("menuitem", { name: "Action" })).toBeInTheDocument();
  });

  it("calls onClick when menu item is clicked", async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    render(<KebabMenu items={[{ label: "Action", onClick }]} />);
    await user.click(screen.getByLabelText("Actions"));
    await user.click(screen.getByRole("menuitem", { name: "Action" }));
    expect(onClick).toHaveBeenCalled();
  });

  it("closes menu after item is clicked", async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    render(<KebabMenu items={[{ label: "Action", onClick }]} />);
    await user.click(screen.getByLabelText("Actions"));
    await user.click(screen.getByRole("menuitem", { name: "Action" }));
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("applies destructive style", async () => {
    const user = userEvent.setup();
    render(<KebabMenu items={[{ label: "Delete", onClick: vi.fn(), destructive: true }]} />);
    await user.click(screen.getByLabelText("Actions"));
    const item = screen.getByRole("menuitem", { name: "Delete" });
    expect(item.className).toContain("text-destructive");
  });

  it("disables menu item", async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    render(<KebabMenu items={[{ label: "Action", onClick, disabled: true }]} />);
    await user.click(screen.getByLabelText("Actions"));
    const item = screen.getByRole("menuitem", { name: "Action" });
    expect(item).toBeDisabled();
  });

  it("closes menu when clicking outside", async () => {
    const user = userEvent.setup();
    render(<KebabMenu items={[{ label: "Action", onClick: vi.fn() }]} />);
    await user.click(screen.getByLabelText("Actions"));
    expect(screen.getByRole("menu")).toBeInTheDocument();
    await user.click(document.body);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  // --- Section grouping ---

  it("renders a labelled divider for a section header", async () => {
    const user = userEvent.setup();
    render(
      <KebabMenu
        items={[
          { label: "Rename", onClick: vi.fn() },
          { label: "Suspend", onClick: vi.fn(), section: "Lifecycle" },
        ]}
      />,
    );
    await user.click(screen.getByLabelText("Actions"));
    expect(screen.getByText("Lifecycle")).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Suspend" })).toBeInTheDocument();
  });

  it("does not render a section header when no item declares a section", async () => {
    const user = userEvent.setup();
    render(
      <KebabMenu
        items={[
          { label: "Rename", onClick: vi.fn() },
          { label: "Delete", onClick: vi.fn(), destructive: true },
        ]}
      />,
    );
    await user.click(screen.getByLabelText("Actions"));
    // Legacy two-phase layout: no section header text present.
    expect(screen.queryByText("Lifecycle")).not.toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Rename" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Delete" })).toBeInTheDocument();
  });

  it("keeps destructive styling within a section", async () => {
    const user = userEvent.setup();
    render(
      <KebabMenu
        items={[
          { label: "Refresh compute", onClick: vi.fn(), section: "Lifecycle" },
          { label: "Delete", onClick: vi.fn(), section: "Lifecycle", destructive: true },
        ]}
      />,
    );
    await user.click(screen.getByLabelText("Actions"));
    const deleteItem = screen.getByRole("menuitem", { name: "Delete" });
    expect(deleteItem.className).toContain("text-destructive");
    expect(screen.getByRole("menuitem", { name: "Refresh compute" }).className).not.toContain(
      "text-destructive",
    );
  });

  it("renders a header on each section change (multi-section)", async () => {
    const user = userEvent.setup();
    render(
      <KebabMenu
        items={[
          { label: "Rename", onClick: vi.fn() },
          { label: "Suspend", onClick: vi.fn(), section: "Lifecycle" },
          { label: "Delete", onClick: vi.fn(), section: "Lifecycle", destructive: true },
          { label: "Export", onClick: vi.fn(), section: "Advanced" },
        ]}
      />,
    );
    await user.click(screen.getByLabelText("Actions"));
    // One header per named section; the unsectioned "Rename" has no header.
    const lifecycleHeaders = screen.getAllByText("Lifecycle");
    const advancedHeaders = screen.getAllByText("Advanced");
    expect(lifecycleHeaders).toHaveLength(1);
    expect(advancedHeaders).toHaveLength(1);
  });

  it("calls onClick and closes the menu for an item in sectioned mode", async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    render(
      <KebabMenu
        items={[
          { label: "Rename", onClick: vi.fn() },
          { label: "Suspend", onClick, section: "Lifecycle" },
        ]}
      />,
    );
    await user.click(screen.getByLabelText("Actions"));
    await user.click(screen.getByRole("menuitem", { name: "Suspend" }));
    expect(onClick).toHaveBeenCalledOnce();
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });
});
