import { useMutation, useQueryClient } from "@tanstack/react-query";
import { workspacesApi } from "../api/workspaces";
import { wsLog } from "./useEventStream";

export function useActivateWorkspace() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (workspaceId: string) => {
      wsLog("activate.called", workspaceId);
      return workspacesApi.activate(workspaceId);
    },
    onSuccess: (_data, workspaceId) => {
      wsLog("activate.success", workspaceId);
      queryClient.invalidateQueries({ queryKey: ["workspaces"] });
      queryClient.invalidateQueries({ queryKey: ["workspace-status"] });
    },
  });
}
