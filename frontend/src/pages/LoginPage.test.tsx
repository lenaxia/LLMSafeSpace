import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { render } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { AuthProvider } from "../providers/AuthProvider";
import { LoginPage } from "./LoginPage";

vi.mock("../api/auth", () => ({
  authApi: {
    me: vi.fn().mockRejectedValue(new Error("401")),
    getConfig: vi.fn().mockResolvedValue({ registrationEnabled: true, oidcEnabled: false, instanceName: "TestSpace" }),
    login: vi.fn(),
  },
}));

function renderLoginPage() {
  return render(
    <AuthProvider>
      <MemoryRouter>
        <LoginPage />
      </MemoryRouter>
    </AuthProvider>,
  );
}

describe("LoginPage", () => {
  it("renders sign in form", async () => {
    renderLoginPage();
    await waitFor(() => expect(screen.getByText("Welcome to TestSpace")).toBeInTheDocument());
    expect(screen.getByPlaceholderText("Email")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Password")).toBeInTheDocument();
  });

  it("shows register link when registration is enabled", async () => {
    renderLoginPage();
    await waitFor(() => expect(screen.getByText("Create an account")).toBeInTheDocument());
  });
});
