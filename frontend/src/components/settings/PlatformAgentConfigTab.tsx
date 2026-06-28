import { useCallback, useEffect, useState } from "react";
import { promptsApi } from "../../api/prompts";
import {
  agentRolesApi,
  type AgentRole,
} from "../../api/agentRoles";
import { ApiClientError } from "../../api/client";
import { useToast } from "../../providers/ToastProvider";
import { Button, Card, CardContent, CardHeader, CardTitle, Input, Badge } from "../ui";
import { Spinner } from "../ui";

export function PlatformAgentConfigTab() {
  const { toast } = useToast();
  const [prompt, setPrompt] = useState("");
  const [loadingPrompt, setLoadingPrompt] = useState(true);
  const [savingPrompt, setSavingPrompt] = useState(false);

  const [roles, setRoles] = useState<AgentRole[]>([]);
  const [loadingRoles, setLoadingRoles] = useState(true);
  const [showCreate, setShowCreate] = useState(false);

  const refreshPrompt = useCallback(async () => {
    setLoadingPrompt(true);
    try {
      const data = await promptsApi.getPlatform();
      setPrompt(data.prompt);
    } catch {
      setPrompt("");
    } finally {
      setLoadingPrompt(false);
    }
  }, []);

  const refreshRoles = useCallback(async () => {
    setLoadingRoles(true);
    try {
      setRoles(await agentRolesApi.listPlatform());
    } catch {
      setRoles([]);
    } finally {
      setLoadingRoles(false);
    }
  }, []);

  useEffect(() => {
    refreshPrompt();
    refreshRoles();
  }, [refreshPrompt, refreshRoles]);

  const savePrompt = async () => {
    setSavingPrompt(true);
    try {
      await promptsApi.setPlatform(prompt);
      toast("Platform prompt saved");
    } catch (e) {
      toast(e instanceof Error ? e.message : "Failed to save", "error");
    } finally {
      setSavingPrompt(false);
    }
  };

  if (loadingPrompt && loadingRoles) {
    return (
      <div className="flex items-center justify-center py-12">
        <Spinner />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Platform System Prompt</CardTitle>
          <p className="text-sm text-muted-foreground">
            Base instructions applied to every workspace on the platform. Appended before org and role prompts.
          </p>
        </CardHeader>
        <CardContent className="space-y-4">
          <textarea
            className="w-full min-h-[200px] rounded-md border border-border bg-background px-3 py-2 text-sm font-mono"
            placeholder="Enter platform-wide instructions..."
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            maxLength={10000}
          />
          <div className="flex items-center justify-between">
            <span className="text-xs text-muted-foreground">{prompt.length} / 10,000 chars</span>
            <Button onClick={savePrompt} disabled={savingPrompt}>
              {savingPrompt ? "Saving..." : "Save Prompt"}
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <CardTitle>Platform Agent Roles</CardTitle>
            <Button size="sm" variant="outline" onClick={() => setShowCreate(!showCreate)}>
              {showCreate ? "Cancel" : "Create Role"}
            </Button>
          </div>
          <p className="text-sm text-muted-foreground">
            Named role definitions available to all orgs. Org roles can inherit from these.
          </p>
        </CardHeader>
        <CardContent>
          {showCreate && (
            <CreateRoleForm
              onDone={() => {
                setShowCreate(false);
                refreshRoles();
              }}
              onError={(msg) => toast(msg, "error")}
            />
          )}

          {loadingRoles ? (
            <div className="py-8 text-center">
              <Spinner />
            </div>
          ) : roles.length === 0 ? (
            <p className="py-8 text-center text-sm text-muted-foreground">No platform roles yet.</p>
          ) : (
            <div className="rounded border border-border overflow-x-auto">
              <table className="w-full text-sm">
                <thead className="border-b border-border bg-muted/50">
                  <tr>
                    <th className="px-3 py-2 text-left font-medium">Name</th>
                    <th className="px-3 py-2 text-left font-medium">Slug</th>
                    <th className="px-3 py-2 text-left font-medium">Inherits</th>
                    <th className="px-3 py-2 text-right font-medium">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {roles.map((role) => (
                    <tr key={role.id} className="border-b border-border last:border-0">
                      <td className="px-3 py-2 font-medium">{role.name}</td>
                      <td className="px-3 py-2">
                        <code className="text-xs">{role.slug}</code>
                      </td>
                      <td className="px-3 py-2">
                        {role.extends ? <Badge variant="muted">{role.extends}</Badge> : "—"}
                      </td>
                      <td className="px-3 py-2 text-right">
                        <Button
                          size="sm"
                          variant="destructive"
                          onClick={async () => {
                            try {
                              await agentRolesApi.deletePlatform(role.id);
                              toast("Role deleted");
                              refreshRoles();
                            } catch (e) {
                              const msg =
                                e instanceof ApiClientError
                                  ? e.body?.error || "Failed to delete"
                                  : "Failed to delete";
                              toast(msg, "error");
                            }
                          }}
                        >
                          Delete
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function CreateRoleForm({ onDone, onError }: { onDone: () => void; onError: (msg: string) => void }) {
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [system, setSystem] = useState("");
  const [busy, setBusy] = useState(false);

  const handleSubmit = async () => {
    if (!name || !slug) {
      onError("Name and slug are required");
      return;
    }
    setBusy(true);
    try {
      await agentRolesApi.createPlatform({
        name,
        slug,
        config: system ? { version: 1, system } : undefined,
      });
      onDone();
    } catch (e) {
      onError(e instanceof ApiClientError ? e.body?.error || "Failed to create" : "Failed to create");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mb-4 rounded-md border border-border p-4 space-y-3 bg-muted/30">
      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className="text-xs font-medium text-muted-foreground">Name</label>
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Code Reviewer" />
        </div>
        <div>
          <label className="text-xs font-medium text-muted-foreground">Slug</label>
          <Input value={slug} onChange={(e) => setSlug(e.target.value)} placeholder="code-reviewer" />
        </div>
      </div>
      <div>
        <label className="text-xs font-medium text-muted-foreground">System Prompt</label>
        <textarea
          className="w-full min-h-[100px] rounded-md border border-border bg-background px-3 py-2 text-sm font-mono"
          placeholder="You are a code reviewer..."
          value={system}
          onChange={(e) => setSystem(e.target.value)}
        />
      </div>
      <Button size="sm" onClick={handleSubmit} disabled={busy}>
        {busy ? "Creating..." : "Create"}
      </Button>
    </div>
  );
}
