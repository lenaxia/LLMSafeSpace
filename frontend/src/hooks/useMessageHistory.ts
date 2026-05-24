import { useQuery } from "@tanstack/react-query";
import { messagesApi } from "../api/messages";

export function useMessageHistory(sandboxId: string | undefined, sessionId: string | undefined) {
  return useQuery({
    queryKey: ["messages", sandboxId, sessionId],
    queryFn: () => messagesApi.getHistory(sandboxId!, sessionId!),
    enabled: !!sandboxId && !!sessionId,
  });
}
