import { useCallback, useEffect, useState } from "react";
import { useOutletContext } from "react-router-dom";
import {
  orgsApi,
  type OrgCredential,
  type OrgResponse,
} from "../../api/orgs";
import { Button } from "../ui/Button";
import { Badge } from "../ui/Badge";

interface CredentialsContext {
  org: OrgResponse;
  isAdmin: boolean;
}

export function OrgCredentialsTab() {
  const { org } = useOutletContext<CredentialsContext>();
  const [creds, setCreds] = useState<OrgCredential[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [showForm, setShowForm] = useState(false);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      setCreds(await orgsApi.listCredentials(org.id));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load credentials");
    } finally {
      setLoading(false);
    }
  }, [org.id]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-semibold">Credentials</h2>
        <Button size="sm" onClick={() => setShowForm((s) => !s)}>
          Add Credential
        </Button>
      </div>

      {showForm && (
        <CredentialForm
          orgId={org.id}
          onDone={() => {
            setShowForm(false);
            refresh();
          }}
        />
      )}

      {loading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {error && <p className="text-sm text-red-500">{error}</p>}

      {!loading && creds.length === 0 && (
        <p className="text-sm text-muted-foreground">
          No org credentials configured. Add one to share across members.
        </p>
      )}

      <div className="space-y-3">
        {creds.map((c) => (
          <div
            key={c.id}
            className="flex items-center justify-between rounded border border-border p-4"
          >
            <div>
              <p className="font-medium">{c.name}</p>
              <p className="text-xs text-muted-foreground">
                Provider: {c.provider}
              </p>
              {c.modelAllowlist.length > 0 && (
                <div className="mt-1 flex flex-wrap gap-1">
                  {c.modelAllowlist.map((m) => (
                    <Badge key={m} variant="muted">
                      {m}
                    </Badge>
                  ))}
                </div>
              )}
            </div>
            <Button
              size="sm"
              variant="destructive"
              onClick={async () => {
                try {
                  await orgsApi.deleteCredential(org.id, c.id);
                  refresh();
                } catch (e) {
                  setError(
                    e instanceof Error ? e.message : "Delete failed",
                  );
                }
              }}
            >
              Delete
            </Button>
          </div>
        ))}
      </div>
    </div>
  );
}

function CredentialForm({
  orgId,
  onDone,
}: {
  orgId: string;
  onDone: () => void;
}) {
  const [name, setName] = useState("");
  const [provider, setProvider] = useState("openai");
  const [apiKey, setApiKey] = useState("");
  const [baseURL, setBaseURL] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const handleSubmit = async () => {
    if (!name.trim() || !apiKey.trim()) {
      setError("Name and API key are required");
      return;
    }
    setLoading(true);
    setError("");
    try {
      await orgsApi.createCredential(orgId, {
        name: name.trim(),
        provider,
        apiKey: apiKey.trim(),
        baseURL: baseURL.trim() || undefined,
      });
      setName("");
      setApiKey("");
      setBaseURL("");
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to create credential");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="space-y-3 rounded border border-border bg-muted/30 p-4">
      <h3 className="text-sm font-medium">New Org Credential</h3>
      {error && <p className="text-xs text-red-500">{error}</p>}
      <input
        className="w-full rounded border border-border bg-background px-3 py-1.5 text-sm"
        placeholder="Name (e.g. Team OpenAI Key)"
        value={name}
        onChange={(e) => setName(e.target.value)}
      />
      <select
        className="w-full rounded border border-border bg-background px-3 py-1.5 text-sm"
        value={provider}
        onChange={(e) => setProvider(e.target.value)}
      >
        <option value="openai">OpenAI</option>
        <option value="anthropic">Anthropic</option>
        <option value="google">Google</option>
        <option value="custom">Custom</option>
      </select>
      <input
        className="w-full rounded border border-border bg-background px-3 py-1.5 text-sm"
        type="password"
        placeholder="API Key"
        value={apiKey}
        onChange={(e) => setApiKey(e.target.value)}
      />
      <input
        className="w-full rounded border border-border bg-background px-3 py-1.5 text-sm"
        placeholder="Custom base URL (optional)"
        value={baseURL}
        onChange={(e) => setBaseURL(e.target.value)}
      />
      <Button size="sm" onClick={handleSubmit} disabled={loading}>
        {loading ? "Creating…" : "Create Credential"}
      </Button>
    </div>
  );
}
