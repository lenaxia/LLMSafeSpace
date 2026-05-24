import { describe, expect, it, vi, beforeEach } from "vitest";
import { screen, waitFor, act } from "@testing-library/react";
import { render } from "@testing-library/react";
import { AuthProvider, useAuth } from "./AuthProvider";

vi.mock("../api/auth", () => ({
  authApi: {
    me: vi.fn(),
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
  },
}));

import { authApi } from "../api/auth";

function TestConsumer() {
  const { user, loading, login, logout } = useAuth();
  return (
    <div>
      <span data-testid="loading">{String(loading)}</span>
      <span data-testid="user">{user?.username ?? "null"}</span>
      <button onClick={() => login("alice", "pass")}>login</button>
      <button onClick={() => logout()}>logout</button>
    </div>
  );
}

describe("AuthProvider", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("starts in loading state", () => {
    (authApi.me as ReturnType<typeof vi.fn>).mockReturnValue(new Promise(() => {}));
    render(<AuthProvider><TestConsumer /></AuthProvider>);
    expect(screen.getByTestId("loading").textContent).toBe("true");
  });

  it("sets user after successful /me call", async () => {
    (authApi.me as ReturnType<typeof vi.fn>).mockResolvedValue({ id: "u1", username: "alice", email: "a@b.com", role: "user", active: true });
    render(<AuthProvider><TestConsumer /></AuthProvider>);
    await waitFor(() => expect(screen.getByTestId("user").textContent).toBe("alice"));
    expect(screen.getByTestId("loading").textContent).toBe("false");
  });

  it("sets user to null after failed /me call", async () => {
    (authApi.me as ReturnType<typeof vi.fn>).mockRejectedValue(new Error("401"));
    render(<AuthProvider><TestConsumer /></AuthProvider>);
    await waitFor(() => expect(screen.getByTestId("loading").textContent).toBe("false"));
    expect(screen.getByTestId("user").textContent).toBe("null");
  });

  it("login sets user on success", async () => {
    (authApi.me as ReturnType<typeof vi.fn>).mockRejectedValue(new Error("401"));
    (authApi.login as ReturnType<typeof vi.fn>).mockResolvedValue({ token: "t", user: { id: "u1", username: "bob" } });
    render(<AuthProvider><TestConsumer /></AuthProvider>);
    await waitFor(() => expect(screen.getByTestId("loading").textContent).toBe("false"));

    await act(async () => { screen.getByText("login").click(); });
    expect(screen.getByTestId("user").textContent).toBe("bob");
  });

  it("logout clears user", async () => {
    (authApi.me as ReturnType<typeof vi.fn>).mockResolvedValue({ id: "u1", username: "alice" });
    (authApi.logout as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
    render(<AuthProvider><TestConsumer /></AuthProvider>);
    await waitFor(() => expect(screen.getByTestId("user").textContent).toBe("alice"));

    await act(async () => { screen.getByText("logout").click(); });
    expect(screen.getByTestId("user").textContent).toBe("null");
  });

  it("throws when useAuth is used outside provider", () => {
    expect(() => render(<TestConsumer />)).toThrow("useAuth must be used within AuthProvider");
  });
});
