import { useQuery } from "@tanstack/react-query";
import { messagesApi } from "../api/messages";

export function useMessageHistory(workspaceId: string | undefined, sessionId: string | undefined) {
  return useQuery({
    queryKey: ["messages", workspaceId, sessionId],
    queryFn: () => messagesApi.getHistory(workspaceId!, sessionId!),
    enabled: !!workspaceId && !!sessionId,
    staleTime: 10_000,
    refetchOnWindowFocus: false,
  });
}
