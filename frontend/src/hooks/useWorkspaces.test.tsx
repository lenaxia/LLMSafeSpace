import { describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useWorkspaces, useWorkspaceStatus } from "./useWorkspaces";

vi.mock("../api/workspaces", () => ({
  workspacesApi: {
    list: vi.fn(),
    getStatus: vi.fn(),
  },
}));

import { workspacesApi } from "../api/workspaces";

function wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

describe("useWorkspaces", () => {
  it("fetches workspace list", async () => {
    (workspacesApi.list as ReturnType<typeof vi.fn>).mockResolvedValue({
      items: [{ id: "ws-1", name: "alpha" }],
      pagination: { limit: 20, offset: 0, total: 1 },
    });

    const { result } = renderHook(() => useWorkspaces(), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.items).toHaveLength(1);
    expect(result.current.data?.items[0]!.name).toBe("alpha");
  });
});

describe("useWorkspaceStatus", () => {
  it("does not fetch when workspaceId is undefined", () => {
    const { result } = renderHook(() => useWorkspaceStatus(undefined), { wrapper });
    expect(result.current.isFetching).toBe(false);
  });

  it("fetches status for given workspace", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
    const { result } = renderHook(() => useWorkspaceStatus("ws-1"), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.phase).toBe("Active");
  });
});

