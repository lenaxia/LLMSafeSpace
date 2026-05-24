import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "../../test/utils";
import { LoginForm } from "./LoginForm";

describe("LoginForm", () => {
  it("renders username and password fields", () => {
    render(<LoginForm onSubmit={vi.fn()} />);
    expect(screen.getByPlaceholderText("Username")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Password")).toBeInTheDocument();
  });

  it("renders submit button", () => {
    render(<LoginForm onSubmit={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Sign in" })).toBeInTheDocument();
  });

  it("calls onSubmit with username and password", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<LoginForm onSubmit={onSubmit} />);

    await user.type(screen.getByPlaceholderText("Username"), "alice");
    await user.type(screen.getByPlaceholderText("Password"), "secret123");
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    expect(onSubmit).toHaveBeenCalledWith("alice", "secret123");
  });

  it("shows loading state during submission", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn((): Promise<void> => new Promise(() => {})); // never resolves
    render(<LoginForm onSubmit={onSubmit} />);

    await user.type(screen.getByPlaceholderText("Username"), "alice");
    await user.type(screen.getByPlaceholderText("Password"), "pass");
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    expect(screen.getByRole("button", { name: "Signing in..." })).toBeDisabled();
  });

  it("shows error message on failure", async () => {
    const user = userEvent.setup();
    const { ApiClientError } = await import("../../api/client");
    const onSubmit = vi.fn().mockRejectedValue(
      new ApiClientError(401, { error: "unauthorized" }),
    );
    render(<LoginForm onSubmit={onSubmit} />);

    await user.type(screen.getByPlaceholderText("Username"), "alice");
    await user.type(screen.getByPlaceholderText("Password"), "wrong");
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    await waitFor(() => {
      expect(screen.getByText("Invalid username or password")).toBeInTheDocument();
    });
  });

  it("does not submit with empty fields (HTML validation)", async () => {
    const onSubmit = vi.fn();
    render(<LoginForm onSubmit={onSubmit} />);
    // Fields are required, browser prevents submission
    expect(screen.getByPlaceholderText("Username")).toBeRequired();
    expect(screen.getByPlaceholderText("Password")).toBeRequired();
  });
});
