import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "../../test/utils";
import { SidebarToggleButton } from "./SidebarToggleButton";

describe("SidebarToggleButton", () => {
  it("renders a button", () => {
    render(<SidebarToggleButton open={false} onClick={() => {}} />);
    expect(screen.getByRole("button")).toBeInTheDocument();
  });

  it('uses "Open menu" aria-label when closed', () => {
    render(<SidebarToggleButton open={false} onClick={() => {}} />);
    expect(screen.getByRole("button", { name: "Open menu" })).toBeInTheDocument();
  });

  it('uses "Close menu" aria-label when open', () => {
    render(<SidebarToggleButton open={true} onClick={() => {}} />);
    expect(screen.getByRole("button", { name: "Close menu" })).toBeInTheDocument();
  });

  it("exposes aria-expanded reflecting the open state", () => {
    const { rerender } = render(<SidebarToggleButton open={false} onClick={() => {}} />);
    expect(screen.getByRole("button")).toHaveAttribute("aria-expanded", "false");
    rerender(<SidebarToggleButton open={true} onClick={() => {}} />);
    expect(screen.getByRole("button")).toHaveAttribute("aria-expanded", "true");
  });

  it("calls onClick when clicked", async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    render(<SidebarToggleButton open={false} onClick={onClick} />);
    await user.click(screen.getByRole("button"));
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});
