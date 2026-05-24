import { useQuery } from "@tanstack/react-query";
import { workspacesApi } from "../api/workspaces";

export function useSessions(workspaceId: string | undefined) {
  return useQuery({
    queryKey: ["sessions", workspaceId],
    queryFn: () => workspacesApi.getSessions(workspaceId!),
    enabled: !!workspaceId,
  });
}
