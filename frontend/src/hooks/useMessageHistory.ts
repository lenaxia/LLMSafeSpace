import { useInfiniteQuery } from "@tanstack/react-query";
import { messagesApi } from "../api/messages";

export function useMessageHistory(workspaceId: string | undefined, sessionId: string | undefined) {
  return useInfiniteQuery({
    queryKey: ["messages", workspaceId, sessionId],
    queryFn: ({ pageParam }) =>
      messagesApi.getHistoryPage(workspaceId!, sessionId!, { before: pageParam }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextCursor,
    enabled: !!workspaceId && !!sessionId,
    staleTime: 10_000,
    refetchOnWindowFocus: false,
  });
}
