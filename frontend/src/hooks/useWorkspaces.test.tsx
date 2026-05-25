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

  it("does not poll after fetching Active phase", async () => {
    // After receiving Active status the hook must not automatically re-fetch.
    // We verify this by confirming getStatus is only called once.
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });

    const { result } = renderHook(() => useWorkspaceStatus("ws-1"), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    const callCountAfterFirstFetch = (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mock.calls.length;

    // Wait enough time that polling would have triggered if it were configured.
    await new Promise((r) => setTimeout(r, 150));

    expect((workspacesApi.getStatus as ReturnType<typeof vi.fn>).mock.calls.length).toBe(callCountAfterFirstFetch);
  });

  it("does not poll after fetching Suspended phase", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Suspended" });

    const { result } = renderHook(() => useWorkspaceStatus("ws-1"), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    const callCountAfterFirstFetch = (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mock.calls.length;

    await new Promise((r) => setTimeout(r, 150));

    expect((workspacesApi.getStatus as ReturnType<typeof vi.fn>).mock.calls.length).toBe(callCountAfterFirstFetch);
  });

  it("does not poll for any phase — relies entirely on SSE invalidation", async () => {
    // Previously the hook polled for transitional phases. Now it must not poll
    // for any phase. Polling is replaced by SSE-driven cache invalidation.
    const transitionalPhases = ["Pending", "Creating", "Resuming", "Suspending"];

    for (const phase of transitionalPhases) {
      vi.clearAllMocks();
      (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase });

      const { result, unmount } = renderHook(() => useWorkspaceStatus("ws-1"), { wrapper });
      await waitFor(() => expect(result.current.isSuccess).toBe(true));

      const callCount = (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mock.calls.length;

      await new Promise((r) => setTimeout(r, 150));

      const callsAfterWait = (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mock.calls.length;
      expect(callsAfterWait).toBe(callCount); // hook should not poll when phase is ${phase}

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
