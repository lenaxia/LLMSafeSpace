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
});
