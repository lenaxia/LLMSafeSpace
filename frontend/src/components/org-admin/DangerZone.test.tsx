import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { DangerZone } from "./DangerZone";
import { ApiClientError } from "../../api/client";

vi.mock("../../api/orgs", () => ({
  orgsApi: {
    delete: vi.fn(),
  },
}));

import { orgsApi } from "../../api/orgs";

function renderZone(orgName = "Acme Corp") {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <DangerZone orgId="org-1" orgName={orgName} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("DangerZone", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders the Danger Zone header", () => {
    renderZone();
    expect(screen.getByText("Danger Zone")).toBeInTheDocument();
  });

  it("disables the delete button until org name is typed", () => {
    renderZone("Acme Corp");
    const btn = screen.getByRole("button", { name: /delete organisation/i });
    expect(btn).toBeDisabled();
  });

  it("enables the delete button when org name matches", async () => {
    const user = userEvent.setup();
    renderZone("Acme Corp");
    await user.type(screen.getByPlaceholderText("Acme Corp"), "Acme Corp");
    expect(screen.getByRole("button", { name: /delete organisation/i })).toBeEnabled();
  });

  it("keeps the button disabled for a partial match", async () => {
    const user = userEvent.setup();
    renderZone("Acme Corp");
    await user.type(screen.getByPlaceholderText("Acme Corp"), "Acme");
    expect(screen.getByRole("button", { name: /delete organisation/i })).toBeDisabled();
  });

  it("calls orgsApi.delete on confirm", async () => {
    const user = userEvent.setup();
    vi.mocked(orgsApi.delete).mockResolvedValue(undefined);
    renderZone("Acme");
    await user.type(screen.getByPlaceholderText("Acme"), "Acme");
    await user.click(screen.getByRole("button", { name: /delete organisation/i }));
    await waitFor(() => {
      expect(orgsApi.delete).toHaveBeenCalledWith("org-1");
    });
  });

  it("shows an error message on delete failure", async () => {
    const user = userEvent.setup();
    vi.mocked(orgsApi.delete).mockRejectedValue(
      new ApiClientError(409, { error: "has workspaces" }),
    );
    renderZone("Acme");
    await user.type(screen.getByPlaceholderText("Acme"), "Acme");
    await user.click(screen.getByRole("button", { name: /delete organisation/i }));
    await waitFor(() => {
      expect(screen.getByText(/has workspaces/i)).toBeInTheDocument();
    });
  });
});
