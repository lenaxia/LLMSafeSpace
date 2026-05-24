import { useQuery } from "@tanstack/react-query";
import { workspacesApi } from "../../api/workspaces";
import { SessionList } from "../session/SessionList";

interface Props {
  workspaceId: string | undefined;
  selectedSessionId?: string;
  onSelectSession: (sessionId: string) => void;
}

export function WorkspaceSessionList({ workspaceId, selectedSessionId, onSelectSession }: Props) {
  const { data: sessions } = useQuery({
    queryKey: ["sessions", workspaceId],
    queryFn: () => workspacesApi.getSessions(workspaceId!),
    enabled: !!workspaceId,
  });

  return (
    <SessionList
      sessions={sessions ?? []}
      selectedId={selectedSessionId}
      onSelect={onSelectSession}
    />
  );
}
