import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "../../test/utils";
import { AbortSessionButton } from "./AbortSessionButton";

describe("AbortSessionButton", () => {
  it("renders a stop button", () => {
    render(<AbortSessionButton onAbort={vi.fn()} />);
    expect(screen.getByRole("button", { name: /stop/i })).toBeInTheDocument();
  });

  it("calls onAbort when clicked", async () => {
    const user = userEvent.setup();
    const onAbort = vi.fn();
    render(<AbortSessionButton onAbort={onAbort} />);
    await user.click(screen.getByRole("button", { name: /stop/i }));
    expect(onAbort).toHaveBeenCalled();
  });

  it("is disabled when disabled prop is true", () => {
    render(<AbortSessionButton onAbort={vi.fn()} disabled />);
    expect(screen.getByRole("button", { name: /stop/i })).toBeDisabled();
  });
});
