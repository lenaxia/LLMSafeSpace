// Integration-level test for useMessageHistory: drives the full
// useInfiniteQuery pagination through messagesApi, exercising the
// X-Next-Cursor contract from page-1 to page-2 to end-of-history.
//
// Replicates the production bug observed in session ses_0f01dd6f1ffe8awjS68zzWTjI5
// where 84 upstream messages collapsed to a single page with hasNextPage=false
// because the server never set X-Next-Cursor.

import { describe, expect, it, vi } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
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

describe("useMessageHistory — full pagination flow", () => {
  it("exposes hasNextPage=true when server returns X-Next-Cursor", async () => {
    (messagesApi.getHistoryPage as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      messages: [
        { id: "msg_0034", role: "user", parts: [{ type: "text", text: "page1-a" }], createdAt: "1970-01-01T00:00:34.000Z" },
        { id: "msg_0083", role: "assistant", parts: [{ type: "text", text: "page1-b" }], createdAt: "1970-01-01T00:01:23.000Z" },
      ],
      nextCursor: "msg_0034",
    });

    const { result } = renderHook(() => useMessageHistory("ws-1", "ses_1"), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.hasNextPage).toBe(true);
  });

  it("walks backwards through two pages and stops when nextCursor is absent", async () => {
    (messagesApi.getHistoryPage as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce({
        // First page = newest 2 of 4.
        messages: [
          { id: "msg_2", role: "user", parts: [{ type: "text", text: "third" }], createdAt: "1970-01-01T00:00:03.000Z" },
          { id: "msg_3", role: "assistant", parts: [{ type: "text", text: "fourth" }], createdAt: "1970-01-01T00:00:04.000Z" },
        ],
        nextCursor: "msg_2",
      })
      .mockResolvedValueOnce({
        // Second page = oldest 2.
        messages: [
          { id: "msg_0", role: "user", parts: [{ type: "text", text: "first" }], createdAt: "1970-01-01T00:00:01.000Z" },
          { id: "msg_1", role: "assistant", parts: [{ type: "text", text: "second" }], createdAt: "1970-01-01T00:00:02.000Z" },
        ],
        nextCursor: undefined,
      });

    const { result } = renderHook(() => useMessageHistory("ws-1", "ses_1"), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.hasNextPage).toBe(true);
    expect(result.current.data!.map((m) => m.id)).toEqual(["msg_2", "msg_3"]);

    // Fetch the next (older) page.
    await act(async () => {
      await result.current.fetchNextPage();
    });
    await waitFor(() => expect(result.current.isFetching).toBe(false));

    // Server was called with the cursor as ?before= for the second page.
    const calls = (messagesApi.getHistoryPage as ReturnType<typeof vi.fn>).mock.calls;
    const beforeArgs = calls.map((c) => c[2]?.before);
    expect(beforeArgs).toContain("msg_2");

    // All four messages are now present, in chronological order.
    expect(result.current.data!.map((m) => m.id)).toEqual(["msg_0", "msg_1", "msg_2", "msg_3"]);
    // And we know there are no more.
    expect(result.current.hasNextPage).toBe(false);
  });

  it("reproduces the production bug: when server never sets nextCursor, hasNextPage stays false even with many messages", async () => {
    // This is what the server does TODAY — returns the full history in one
    // shot with no cursor header. The hook believes there's nothing older,
    // so the 'Load earlier messages' button never renders.
    const eightyFour = Array.from({ length: 84 }, (_, i) => ({
      id: `msg_${String(i).padStart(4, "0")}`,
      role: i % 2 === 0 ? ("user" as const) : ("assistant" as const),
      parts: [{ type: "text" as const, text: `body ${i}` }],
      createdAt: new Date(1000 + i).toISOString(),
    }));
    (messagesApi.getHistoryPage as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      messages: eightyFour,
      nextCursor: undefined,
    });

    const { result } = renderHook(() => useMessageHistory("ws-1", "ses_1"), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    // The hook has all 84 messages, but no way to know there could have been more.
    // hasNextPage is correctly false IFF the server has truly returned everything.
    // The bug shows up in the SERVER test — server fails to filter+cap+set-cursor.
    expect(result.current.data).toHaveLength(84);
    expect(result.current.hasNextPage).toBe(false);
  });
});
