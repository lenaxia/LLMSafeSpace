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

  it("sorts by createdAt with id as tiebreaker", async () => {
    (messagesApi.getHistoryPage as ReturnType<typeof vi.fn>).mockResolvedValue({
      messages: [
        { id: "zz", role: "assistant", parts: [{ type: "text", text: "later" }], createdAt: "1970-01-01T00:00:02.000Z" },
        { id: "aa", role: "user", parts: [{ type: "text", text: "earlier" }], createdAt: "1970-01-01T00:00:01.000Z" },
      ],
      nextCursor: undefined,
    });
    const { result } = renderHook(() => useMessageHistory("sb-1", "sess-1"), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toHaveLength(2);
    expect(result.current.data![0]!.id).toBe("aa");
    expect(result.current.data![1]!.id).toBe("zz");
  });

  it("uses id as tiebreaker when createdAt is equal", async () => {
    (messagesApi.getHistoryPage as ReturnType<typeof vi.fn>).mockResolvedValue({
      messages: [
        { id: "b", role: "assistant", parts: [{ type: "text", text: "second" }], createdAt: "1970-01-01T00:00:01.000Z" },
        { id: "a", role: "user", parts: [{ type: "text", text: "first" }], createdAt: "1970-01-01T00:00:01.000Z" },
      ],
      nextCursor: undefined,
    });
    const { result } = renderHook(() => useMessageHistory("sb-1", "sess-1"), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toHaveLength(2);
    expect(result.current.data![0]!.id).toBe("a");
    expect(result.current.data![1]!.id).toBe("b");
  });

  it("sorts queued messages (msg_q_*) correctly among native messages", async () => {
    (messagesApi.getHistoryPage as ReturnType<typeof vi.fn>).mockResolvedValue({
      messages: [
        { id: "msg_q_ccc", role: "user", parts: [{ type: "text", text: "interjection" }], createdAt: "1970-01-01T00:00:01.500Z" },
        { id: "msg_eAAA", role: "assistant", parts: [{ type: "text", text: "first response" }], createdAt: "1970-01-01T00:00:01.000Z" },
        { id: "msg_q_bbb", role: "user", parts: [{ type: "text", text: "second interjection" }], createdAt: "1970-01-01T00:00:02.000Z" },
      ],
      nextCursor: undefined,
    });
    const { result } = renderHook(() => useMessageHistory("sb-1", "sess-1"), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toHaveLength(3);
    expect(result.current.data![0]!.id).toBe("msg_eAAA");
    expect(result.current.data![1]!.id).toBe("msg_q_ccc");
    expect(result.current.data![2]!.id).toBe("msg_q_bbb");
  });

  it("falls back to id sort when createdAt is missing", async () => {
    (messagesApi.getHistoryPage as ReturnType<typeof vi.fn>).mockResolvedValue({
      messages: [
        { id: "c", role: "assistant", parts: [{ type: "text", text: "msg c" }] },
        { id: "a", role: "user", parts: [{ type: "text", text: "msg a" }] },
        { id: "b", role: "assistant", parts: [{ type: "text", text: "msg b" }] },
      ],
      nextCursor: undefined,
    });
    const { result } = renderHook(() => useMessageHistory("sb-1", "sess-1"), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toHaveLength(3);
    expect(result.current.data![0]!.id).toBe("a");
    expect(result.current.data![1]!.id).toBe("b");
    expect(result.current.data![2]!.id).toBe("c");
  });
});
