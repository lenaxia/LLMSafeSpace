import { describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useSessions } from "./useSessions";

vi.mock("../api/workspaces", () => ({
  workspacesApi: {
    getSessions: vi.fn(),
  },
}));

import { workspacesApi } from "../api/workspaces";

function wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

describe("useSessions", () => {
  it("does not fetch when workspaceId is undefined", () => {
    const { result } = renderHook(() => useSessions(undefined), { wrapper });
    expect(result.current.isFetching).toBe(false);
  });

  it("fetches sessions for given workspace", async () => {
    (workspacesApi.getSessions as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: "s1", title: "Chat", messageCount: 5, status: "idle" },
    ]);
    const { result } = renderHook(() => useSessions("ws-1"), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toHaveLength(1);
    expect(result.current.data![0]!.title).toBe("Chat");
  });
});
