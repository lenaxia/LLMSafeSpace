import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "../../test/utils";
import { UpdateAvailableToast } from "./UpdateAvailableToast";

describe("UpdateAvailableToast", () => {
  it("renders update message", () => {
    render(<UpdateAvailableToast onUpdate={vi.fn()} onDismiss={vi.fn()} />);
    expect(screen.getByText(/update available/i)).toBeInTheDocument();
  });

  it("renders update button", () => {
    render(<UpdateAvailableToast onUpdate={vi.fn()} onDismiss={vi.fn()} />);
    expect(screen.getByRole("button", { name: /reload/i })).toBeInTheDocument();
  });

  it("calls onUpdate when reload clicked", async () => {
    const user = userEvent.setup();
    const onUpdate = vi.fn();
    render(<UpdateAvailableToast onUpdate={onUpdate} onDismiss={vi.fn()} />);
    await user.click(screen.getByRole("button", { name: /reload/i }));
    expect(onUpdate).toHaveBeenCalled();
  });

  it("renders dismiss button", () => {
    render(<UpdateAvailableToast onUpdate={vi.fn()} onDismiss={vi.fn()} />);
    expect(screen.getByRole("button", { name: /dismiss/i })).toBeInTheDocument();
  });

  it("calls onDismiss when dismiss clicked", async () => {
    const user = userEvent.setup();
    const onDismiss = vi.fn();
    render(<UpdateAvailableToast onUpdate={vi.fn()} onDismiss={onDismiss} />);
    await user.click(screen.getByRole("button", { name: /dismiss/i }));
    expect(onDismiss).toHaveBeenCalled();
  });
});
