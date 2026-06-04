import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { ModelSelector } from "./ModelSelector";

const mockModels = {
  models: [
    { id: "anthropic/claude-sonnet-4-5", providerID: "anthropic", name: "Claude Sonnet", tier: "paid", freeTier: false, selected: true, enabled: true },
    { id: "openai/gpt-4o", providerID: "openai", name: "GPT-4o", tier: "paid", freeTier: false, selected: false, enabled: true },
    { id: "opencode/free-model", providerID: "opencode", name: "Free Model", tier: "free", freeTier: true, selected: false, enabled: true },
    { id: "disabled/model", providerID: "test", name: "Disabled", tier: "paid", freeTier: false, selected: false, enabled: false },
  ],
  currentModel: "anthropic/claude-sonnet-4-5",
};

vi.mock("../../api/workspaces", () => ({
  workspacesApi: {
    listModels: vi.fn(),
    setModel: vi.fn(),
  },
}));

import { workspacesApi } from "../../api/workspaces";

function wrapper({ children }: { children: React.ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

describe("ModelSelector", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders current model name when loaded", async () => {
    vi.mocked(workspacesApi.listModels).mockResolvedValue(mockModels);
    render(<ModelSelector workspaceId="ws-1" />, { wrapper });
    await waitFor(() => expect(screen.getByText("Claude Sonnet")).toBeInTheDocument());
  });

  it("shows dropdown with enabled models on click", async () => {
    vi.mocked(workspacesApi.listModels).mockResolvedValue(mockModels);
    render(<ModelSelector workspaceId="ws-1" />, { wrapper });
    await waitFor(() => screen.getByText("Claude Sonnet"));
    fireEvent.click(screen.getByText("Claude Sonnet"));
    expect(screen.getByText("GPT-4o")).toBeInTheDocument();
    expect(screen.getByText("Free Model")).toBeInTheDocument();
    // Disabled model should not appear
    expect(screen.queryByText("Disabled")).not.toBeInTheDocument();
  });

  it("calls setModel when a model is selected", async () => {
    vi.mocked(workspacesApi.listModels).mockResolvedValue(mockModels);
    vi.mocked(workspacesApi.setModel).mockResolvedValue({ model: "openai/gpt-4o", applied: true });
    render(<ModelSelector workspaceId="ws-1" />, { wrapper });
    await waitFor(() => screen.getByText("Claude Sonnet"));
    fireEvent.click(screen.getByText("Claude Sonnet"));
    fireEvent.click(screen.getByText("GPT-4o"));
    await waitFor(() => expect(workspacesApi.setModel).toHaveBeenCalledWith("ws-1", "openai/gpt-4o"));
  });

  it("renders nothing when no models available", async () => {
    vi.mocked(workspacesApi.listModels).mockResolvedValue({ models: [], currentModel: "" });
    const { container } = render(<ModelSelector workspaceId="ws-1" />, { wrapper });
    await waitFor(() => expect(container.querySelector("button")).toBeNull());
  });

  it("shows tier badges", async () => {
    vi.mocked(workspacesApi.listModels).mockResolvedValue(mockModels);
    render(<ModelSelector workspaceId="ws-1" />, { wrapper });
    await waitFor(() => screen.getByText("Claude Sonnet"));
    fireEvent.click(screen.getByText("Claude Sonnet"));
    const badges = screen.getAllByText("free");
    expect(badges.length).toBeGreaterThan(0);
  });

  it("shows error indicator when listModels fails", async () => {
    vi.mocked(workspacesApi.listModels).mockRejectedValue(new Error("503"));
    render(<ModelSelector workspaceId="ws-1" />, { wrapper });
    // retry:1 in component means it takes 2 rejections before error state
    await waitFor(() => expect(screen.getByTitle("Could not load models")).toBeInTheDocument(), { timeout: 3000 });
  });

  it("shows toast when model saved with applied:false", async () => {
    vi.mocked(workspacesApi.listModels).mockResolvedValue(mockModels);
    vi.mocked(workspacesApi.setModel).mockResolvedValue({ model: "openai/gpt-4o", applied: false });
    render(<ModelSelector workspaceId="ws-1" />, { wrapper });
    await waitFor(() => screen.getByText("Claude Sonnet"));
    fireEvent.click(screen.getByText("Claude Sonnet"));
    fireEvent.click(screen.getByText("GPT-4o"));
    await waitFor(() => expect(screen.getByText(/takes effect/)).toBeInTheDocument());
  });

  it("shows error toast when setModel fails", async () => {
    vi.mocked(workspacesApi.listModels).mockResolvedValue(mockModels);
    vi.mocked(workspacesApi.setModel).mockRejectedValue(new Error("500"));
    render(<ModelSelector workspaceId="ws-1" />, { wrapper });
    await waitFor(() => screen.getByText("Claude Sonnet"));
    fireEvent.click(screen.getByText("Claude Sonnet"));
    fireEvent.click(screen.getByText("GPT-4o"));
    await waitFor(() => expect(screen.getByText(/Failed to set model/)).toBeInTheDocument());
  });
});
