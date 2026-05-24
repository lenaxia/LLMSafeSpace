import { describe, expect, it, vi } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useActivateWorkspace } from "./useActivateWorkspace";

vi.mock("../api/workspaces", () => ({
  workspacesApi: {
    activate: vi.fn(),
  },
}));

import { workspacesApi } from "../api/workspaces";

function wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

describe("useActivateWorkspace", () => {
  it("calls activate API with workspace id", async () => {
    (workspacesApi.activate as ReturnType<typeof vi.fn>).mockResolvedValue({ resumed: "ws-1" });
    const { result } = renderHook(() => useActivateWorkspace(), { wrapper });

    await act(async () => { result.current.mutate("ws-1"); });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(workspacesApi.activate).toHaveBeenCalledWith("ws-1");
  });

  it("reports error on failure", async () => {
    (workspacesApi.activate as ReturnType<typeof vi.fn>).mockRejectedValue(new Error("fail"));
    const { result } = renderHook(() => useActivateWorkspace(), { wrapper });

    await act(async () => { result.current.mutate("ws-bad"); });
    await waitFor(() => expect(result.current.isError).toBe(true));
  });
});
