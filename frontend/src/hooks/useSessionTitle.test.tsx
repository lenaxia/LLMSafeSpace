import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

vi.mock("../api/workspaces", () => ({
  workspacesApi: {
    getSession: vi.fn(),
    renameSession: vi.fn().mockResolvedValue(undefined),
  },
}));

import { workspacesApi } from "../api/workspaces";
import { useSessionTitle } from "./useSessionTitle";
import type { SessionListItem } from "../api/types";

let qc: QueryClient;

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

beforeEach(() => {
  vi.clearAllMocks();
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
});

describe("useSessionTitle", () => {
  // --- Disabled/guard tests ---

  it("returns undefined when workspaceId is not provided", () => {
    const { result } = renderHook(
      () => useSessionTitle(undefined, "sess-1", true, false),
      { wrapper },
    );
    expect(result.current).toBeUndefined();
    expect(workspacesApi.getSession).not.toHaveBeenCalled();
  });

  it("returns undefined when sessionId is not provided", () => {
    const { result } = renderHook(
      () => useSessionTitle("ws-1", undefined, true, false),
      { wrapper },
    );
    expect(result.current).toBeUndefined();
    expect(workspacesApi.getSession).not.toHaveBeenCalled();
  });

  it("returns undefined when workspace is not active", () => {
    const { result } = renderHook(
      () => useSessionTitle("ws-1", "sess-1", false, false),
      { wrapper },
    );
    expect(result.current).toBeUndefined();
    expect(workspacesApi.getSession).not.toHaveBeenCalled();
  });

  // --- Happy path ---

  it("fetches and returns session title when all params are valid", async () => {
    (workspacesApi.getSession as ReturnType<typeof vi.fn>).mockResolvedValue({
      id: "sess-1",
      title: "Clone repo",
    });

    const { result } = renderHook(
      () => useSessionTitle("ws-1", "sess-1", true, false),
      { wrapper },
    );

    await waitFor(() => expect(result.current).toBe("Clone repo"));
    expect(workspacesApi.getSession).toHaveBeenCalledWith("ws-1", "sess-1");
  });

  it("returns undefined when session has no title", async () => {
    (workspacesApi.getSession as ReturnType<typeof vi.fn>).mockResolvedValue({
      id: "sess-1",
      title: undefined,
    });

    const { result } = renderHook(
      () => useSessionTitle("ws-1", "sess-1", true, false),
      { wrapper },
    );

    await waitFor(() => expect(workspacesApi.getSession).toHaveBeenCalled());
    expect(result.current).toBeUndefined();
  });

  it("returns undefined when session title is empty string", async () => {
    (workspacesApi.getSession as ReturnType<typeof vi.fn>).mockResolvedValue({
      id: "sess-1",
      title: "",
    });

    const { result } = renderHook(
      () => useSessionTitle("ws-1", "sess-1", true, false),
      { wrapper },
    );

    await waitFor(() => expect(workspacesApi.getSession).toHaveBeenCalled());
    expect(result.current).toBeUndefined();
  });

  // --- Cache update tests (Bug 1 fix) ---

  it("updates sessions cache directly when title is received", async () => {
    const sessions: SessionListItem[] = [
      { id: "sess-1", messageCount: 3, status: "idle", hasUnread: false },
      { id: "sess-2", title: "Existing", messageCount: 1, status: "idle", hasUnread: false },
    ];
    qc.setQueryData(["sessions", "ws-1"], sessions);
    // Keep the cache alive by setting a long gcTime for this specific key
    qc.setQueryDefaults(["sessions", "ws-1"], { gcTime: Infinity });

    (workspacesApi.getSession as ReturnType<typeof vi.fn>).mockResolvedValue({
      id: "sess-1",
      title: "New Title From Opencode",
    });

    renderHook(
      () => useSessionTitle("ws-1", "sess-1", true, false),
      { wrapper },
    );

    await waitFor(() => {
      const cached = qc.getQueryData<SessionListItem[]>(["sessions", "ws-1"]);
      expect(cached?.[0]?.title).toBe("New Title From Opencode");
    });

    // Other sessions remain unchanged
    const cached = qc.getQueryData<SessionListItem[]>(["sessions", "ws-1"]);
    expect(cached?.[1]?.title).toBe("Existing");
  });

  it("does not modify sessions cache when title is empty", async () => {
    const sessions: SessionListItem[] = [
      { id: "sess-1", messageCount: 3, status: "idle", hasUnread: false },
    ];
    qc.setQueryData(["sessions", "ws-1"], sessions);

    (workspacesApi.getSession as ReturnType<typeof vi.fn>).mockResolvedValue({
      id: "sess-1",
      title: "",
    });

    renderHook(
      () => useSessionTitle("ws-1", "sess-1", true, false),
      { wrapper },
    );

    await waitFor(() => expect(workspacesApi.getSession).toHaveBeenCalled());
    // Give effect time to run
    await act(async () => { await new Promise((r) => setTimeout(r, 50)); });

    const cached = qc.getQueryData<SessionListItem[]>(["sessions", "ws-1"]);
    expect(cached?.[0]?.title).toBeUndefined();
  });

  it("handles missing sessions cache gracefully (no crash)", async () => {
    // No pre-populated cache
    (workspacesApi.getSession as ReturnType<typeof vi.fn>).mockResolvedValue({
      id: "sess-1",
      title: "Title",
    });

    const { result } = renderHook(
      () => useSessionTitle("ws-1", "sess-1", true, false),
      { wrapper },
    );

    await waitFor(() => expect(result.current).toBe("Title"));
    // Should not throw — cache remains undefined
    expect(qc.getQueryData(["sessions", "ws-1"])).toBeUndefined();
  });

  it("only updates the matching session in cache, not others", async () => {
    const sessions: SessionListItem[] = [
      { id: "sess-1", messageCount: 1, status: "idle", hasUnread: false },
      { id: "sess-2", messageCount: 2, status: "active", hasUnread: false },
      { id: "sess-3", title: "Keep Me", messageCount: 0, status: "idle", hasUnread: false },
    ];
    qc.setQueryData(["sessions", "ws-1"], sessions);
    qc.setQueryDefaults(["sessions", "ws-1"], { gcTime: Infinity });

    (workspacesApi.getSession as ReturnType<typeof vi.fn>).mockResolvedValue({
      id: "sess-2",
      title: "Updated",
    });

    renderHook(
      () => useSessionTitle("ws-1", "sess-2", true, false),
      { wrapper },
    );

    await waitFor(() => {
      const cached = qc.getQueryData<SessionListItem[]>(["sessions", "ws-1"]);
      expect(cached?.[1]?.title).toBe("Updated");
    });

    const cached = qc.getQueryData<SessionListItem[]>(["sessions", "ws-1"]);
    expect(cached?.[0]?.title).toBeUndefined();
    expect(cached?.[2]?.title).toBe("Keep Me");
  });

  // --- Streaming transition tests ---

  it("refetches title when streaming transitions from true to false", async () => {
    (workspacesApi.getSession as ReturnType<typeof vi.fn>).mockResolvedValue({
      id: "sess-1",
      title: undefined,
    });

    const { rerender } = renderHook(
      ({ streaming }) => useSessionTitle("ws-1", "sess-1", true, streaming),
      { wrapper, initialProps: { streaming: true } },
    );

    await waitFor(() => expect(workspacesApi.getSession).toHaveBeenCalledTimes(1));

    // Now return a title on the next fetch
    (workspacesApi.getSession as ReturnType<typeof vi.fn>).mockResolvedValue({
      id: "sess-1",
      title: "Generated Title",
    });

    // Transition streaming: true → false
    rerender({ streaming: false });

    // Should trigger at least one more fetch
    await waitFor(() => expect(workspacesApi.getSession).toHaveBeenCalledTimes(2));
  });

  it("does not refetch when streaming stays false (no transition)", async () => {
    (workspacesApi.getSession as ReturnType<typeof vi.fn>).mockResolvedValue({
      id: "sess-1",
      title: "Title",
    });

    const { rerender } = renderHook(
      ({ streaming }) => useSessionTitle("ws-1", "sess-1", true, streaming),
      { wrapper, initialProps: { streaming: false } },
    );

    await waitFor(() => expect(workspacesApi.getSession).toHaveBeenCalledTimes(1));

    // Re-render with same streaming=false — should not trigger refetch
    rerender({ streaming: false });

    await act(async () => { await new Promise((r) => setTimeout(r, 100)); });
    expect(workspacesApi.getSession).toHaveBeenCalledTimes(1);
  });

  it("does not refetch when streaming transitions false to true", async () => {
    (workspacesApi.getSession as ReturnType<typeof vi.fn>).mockResolvedValue({
      id: "sess-1",
      title: "Title",
    });

    const { rerender } = renderHook(
      ({ streaming }) => useSessionTitle("ws-1", "sess-1", true, streaming),
      { wrapper, initialProps: { streaming: false } },
    );

    await waitFor(() => expect(workspacesApi.getSession).toHaveBeenCalledTimes(1));

    // Transition false → true should NOT trigger refetch
    rerender({ streaming: true });

    await act(async () => { await new Promise((r) => setTimeout(r, 100)); });
    expect(workspacesApi.getSession).toHaveBeenCalledTimes(1);
  });

  // --- Error handling ---

  it("returns undefined when getSession rejects", async () => {
    (workspacesApi.getSession as ReturnType<typeof vi.fn>).mockRejectedValue(
      new Error("Not found"),
    );

    const { result } = renderHook(
      () => useSessionTitle("ws-1", "sess-1", true, false),
      { wrapper },
    );

    await waitFor(() => expect(workspacesApi.getSession).toHaveBeenCalled());
    expect(result.current).toBeUndefined();
  });

  it("does not retry on error (retry: false)", async () => {
    (workspacesApi.getSession as ReturnType<typeof vi.fn>).mockRejectedValue(
      new Error("Not found"),
    );

    renderHook(
      () => useSessionTitle("ws-1", "sess-1", true, false),
      { wrapper },
    );

    await waitFor(() => expect(workspacesApi.getSession).toHaveBeenCalledTimes(1));
    await act(async () => { await new Promise((r) => setTimeout(r, 200)); });
    // Should still be 1 — no retry
    expect(workspacesApi.getSession).toHaveBeenCalledTimes(1);
  });
});
