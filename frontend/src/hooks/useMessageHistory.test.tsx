import { describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useMessageHistory } from "./useMessageHistory";

vi.mock("../api/messages", () => ({
  messagesApi: {
    getHistory: vi.fn(),
    getHistoryPage: vi.fn(),
  },
}));

import { messagesApi } from "../api/messages";

function wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

describe("useMessageHistory", () => {
  it("does not fetch when workspaceId is undefined", () => {
    const { result } = renderHook(() => useMessageHistory(undefined, "sess-1"), { wrapper });
    expect(result.current.isFetching).toBe(false);
  });

  it("does not fetch when sessionId is undefined", () => {
    const { result } = renderHook(() => useMessageHistory("sb-1", undefined), { wrapper });
    expect(result.current.isFetching).toBe(false);
  });

  it("fetches message history when both ids provided", async () => {
    (messagesApi.getHistoryPage as ReturnType<typeof vi.fn>).mockResolvedValue({
      messages: [
        { id: "m1", role: "user", parts: [{ type: "text", text: "hi" }] },
        { id: "m2", role: "assistant", parts: [{ type: "text", text: "hello" }] },
      ],
      nextCursor: undefined,
    });
    const { result } = renderHook(() => useMessageHistory("sb-1", "sess-1"), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.pages).toHaveLength(1);
    expect(result.current.data?.pages[0]?.messages).toHaveLength(2);
  });
});
