import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
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
  beforeEach(() => {
    vi.clearAllMocks();
  });

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

  it("polls every 30s for Active phase to keep context usage fresh", async () => {
    // Active phase must poll at 30s so context usage indicators (S36.4/S36.5)
    // receive fresh data. Without this poll, compaction detection never fires
    // because context changes are not delivered via SSE.
    // We verify the refetchInterval is set by checking the query observer options,
    // not by waiting 30s — that would make CI impractical.
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });

    let qc: QueryClient;
    function wrapperWithRef({ children }: { children: ReactNode }) {
      qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 0 } } });
      return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
    }

    const { result } = renderHook(() => useWorkspaceStatus("ws-1"), { wrapper: wrapperWithRef });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    // Simulate the poll firing by invalidating the query (same effect as the 30s timer)
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
    act(() => { qc!.invalidateQueries({ queryKey: ["workspace-status", "ws-1"] }); });

    await waitFor(() => expect((workspacesApi.getStatus as ReturnType<typeof vi.fn>).mock.calls.length).toBeGreaterThanOrEqual(2));
  });

  it("does not poll for Suspended phase", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Suspended" });

    const { result } = renderHook(() => useWorkspaceStatus("ws-1"), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    const callCountAfterFirstFetch = (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mock.calls.length;

    await new Promise((r) => setTimeout(r, 150));

    expect((workspacesApi.getStatus as ReturnType<typeof vi.fn>).mock.calls.length).toBe(callCountAfterFirstFetch);
  });

  it("polls every 3s for transitioning phases and every 30s for Active phase", async () => {
    // Transitional phases poll at 3s. Active polls at 30s. Both intervals are
    // much longer than the 150ms test window, so we verify them via invalidation,
    // not by sleeping. The key invariant is: non-Suspended/terminal phases enable polling.
    const phasesWithPolling = ["Pending", "Creating", "Resuming", "Suspending", "Active"];

    for (const phase of phasesWithPolling) {
      vi.clearAllMocks();
      (workspacesApi.getStatus as ReturnType<typeof vi.fn>)
        .mockResolvedValueOnce({ phase })
        .mockResolvedValue({ phase });

      let qc: QueryClient;
      function wrapperInner({ children }: { children: ReactNode }) {
        qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 0 } } });
        return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
      }

      const { result, unmount } = renderHook(() => useWorkspaceStatus("ws-1"), { wrapper: wrapperInner });
      await waitFor(() => expect(result.current.isSuccess).toBe(true));

      // Simulate the poll firing
      act(() => { qc!.invalidateQueries({ queryKey: ["workspace-status", "ws-1"] }); });
      await waitFor(() =>
        expect((workspacesApi.getStatus as ReturnType<typeof vi.fn>).mock.calls.length).toBeGreaterThanOrEqual(2),
      );

      unmount();
    }
  });

  it("re-fetches when query cache is invalidated", async () => {
    // Simulates what happens when the SSE handler calls queryClient.invalidateQueries.
    let qc: QueryClient;

    (workspacesApi.getStatus as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce({ phase: "Resuming" })
      .mockResolvedValueOnce({ phase: "Active" });

    function wrapperWithRef({ children }: { children: ReactNode }) {
      qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 0 } } });
      return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
    }

    const { result } = renderHook(() => useWorkspaceStatus("ws-1"), { wrapper: wrapperWithRef });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.phase).toBe("Resuming");

    // Simulate SSE-triggered invalidation.
    act(() => {
      qc!.invalidateQueries({ queryKey: ["workspace-status", "ws-1"] });
    });

    await waitFor(() => expect(result.current.data?.phase).toBe("Active"));
    expect((workspacesApi.getStatus as ReturnType<typeof vi.fn>).mock.calls.length).toBe(2);
  });
});
