import { useInfiniteQuery } from "@tanstack/react-query";
import { messagesApi, type HistoryPage } from "../api/messages";
import type { Message } from "../api/types";

interface InfiniteData {
  pages: HistoryPage[];
  pageParams: (string | undefined)[];
}

function selectChronological(data: InfiniteData): Message[] {
  const all = data.pages.flatMap((p) => p.messages);
  return all.sort((a, b) => a.id.localeCompare(b.id));
}

export function useMessageHistory(workspaceId: string | undefined, sessionId: string | undefined) {
  return useInfiniteQuery({
    queryKey: ["messages", workspaceId, sessionId],
    queryFn: ({ pageParam }) =>
      messagesApi.getHistoryPage(workspaceId!, sessionId!, { before: pageParam }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage?.nextCursor,
    enabled: !!workspaceId && !!sessionId,
    staleTime: 10_000,
    refetchOnWindowFocus: false,
    select: selectChronological,
  });
}
