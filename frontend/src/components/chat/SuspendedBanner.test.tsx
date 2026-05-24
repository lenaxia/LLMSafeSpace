import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "../../test/utils";
import { SuspendedBanner } from "./SuspendedBanner";

describe("SuspendedBanner", () => {
  it("renders workspace name", () => {
    render(<SuspendedBanner workspaceName="alpha" onActivate={vi.fn()} />);
    expect(screen.getByText("alpha")).toBeInTheDocument();
  });

  it("renders resume button", () => {
    render(<SuspendedBanner workspaceName="alpha" onActivate={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Resume to chat" })).toBeInTheDocument();
  });

  it("calls onActivate when button clicked", async () => {
    const user = userEvent.setup();
    const onActivate = vi.fn();
    render(<SuspendedBanner workspaceName="alpha" onActivate={onActivate} />);
    await user.click(screen.getByRole("button", { name: "Resume to chat" }));
    expect(onActivate).toHaveBeenCalled();
  });

  it("shows loading state when activating", () => {
    render(<SuspendedBanner workspaceName="alpha" onActivate={vi.fn()} activating />);
    expect(screen.getByRole("button", { name: "Resuming..." })).toBeDisabled();
  });
});
