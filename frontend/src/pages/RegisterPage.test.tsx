import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { render } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { AuthProvider } from "../providers/AuthProvider";
import { RegisterPage } from "./RegisterPage";

vi.mock("../api/auth", () => ({
  authApi: {
    me: vi.fn().mockRejectedValue(new Error("401")),
    register: vi.fn(),
  },
}));

function renderRegisterPage() {
  return render(
    <AuthProvider>
      <MemoryRouter>
        <RegisterPage />
      </MemoryRouter>
    </AuthProvider>,
  );
}

describe("RegisterPage", () => {
  it("renders create account form", async () => {
    renderRegisterPage();
    await waitFor(() => expect(screen.getByRole("heading", { name: "Create account" })).toBeInTheDocument());
    expect(screen.getByPlaceholderText("Username")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Email")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Password")).toBeInTheDocument();
  });

  it("shows sign in link", async () => {
    renderRegisterPage();
    await waitFor(() => expect(screen.getByText(/already have an account/i)).toBeInTheDocument());
  });
});
