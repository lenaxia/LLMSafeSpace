import { useState } from "react";
import { useOutletContext } from "react-router-dom";
import { orgsApi, type OrgResponse } from "../../api/orgs";
import { Button } from "../ui/Button";

interface SSOContext {
  org: OrgResponse;
  isAdmin: boolean;
}

export function OrgSSOTab() {
  const { org } = useOutletContext<SSOContext>();
  const [discoveryUrl, setDiscoveryUrl] = useState("");
  const [clientId, setClientId] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [adminGroup, setAdminGroup] = useState("llmsafespace-admins");
  const [memberGroup, setMemberGroup] = useState("llmsafespace-members");
  const [autoProvision, setAutoProvision] = useState(true);
  const [enabled, setEnabled] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  const handleSave = async () => {
    if (!discoveryUrl.trim() || !clientId.trim() || !clientSecret.trim()) {
      setError("Discovery URL, Client ID, and Client Secret are required");
      return;
    }
    setLoading(true);
    setError("");
    setSaved(false);
    try {
      await orgsApi.updateSSO(org.id, {
        discoveryUrl: discoveryUrl.trim(),
        clientId: clientId.trim(),
        clientSecret: clientSecret.trim(),
        groupAdminClaim: adminGroup,
        groupMemberClaim: memberGroup,
        autoProvision,
        enabled,
      });
      setClientSecret("");
      setSaved(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to save SSO config");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold">SSO (OIDC)</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          Configure OpenID Connect single sign-on for this organization.
        </p>
      </div>

      {saved && (
        <p className="rounded border border-green-500/30 bg-green-500/10 p-2 text-sm text-green-700">
          SSO configuration saved.
        </p>
      )}
      {error && <p className="text-sm text-red-500">{error}</p>}

      <div className="space-y-4 rounded border border-border p-4">
        <div>
          <label className="mb-1 block text-sm font-medium">Discovery URL</label>
          <input
            className="w-full rounded border border-border bg-background px-3 py-1.5 text-sm"
            placeholder="https://idp.example.com/.well-known/openid-configuration"
            value={discoveryUrl}
            onChange={(e) => setDiscoveryUrl(e.target.value)}
          />
        </div>
        <div>
          <label className="mb-1 block text-sm font-medium">Client ID</label>
          <input
            className="w-full rounded border border-border bg-background px-3 py-1.5 text-sm"
            placeholder="your-oidc-client-id"
            value={clientId}
            onChange={(e) => setClientId(e.target.value)}
          />
        </div>
        <div>
          <label className="mb-1 block text-sm font-medium">Client Secret</label>
          <input
            className="w-full rounded border border-border bg-background px-3 py-1.5 text-sm"
            type="password"
            placeholder="••••••••"
            value={clientSecret}
            onChange={(e) => setClientSecret(e.target.value)}
          />
        </div>
        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="mb-1 block text-sm font-medium">Admin Group Claim</label>
            <input
              className="w-full rounded border border-border bg-background px-3 py-1.5 text-sm"
              value={adminGroup}
              onChange={(e) => setAdminGroup(e.target.value)}
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">Member Group Claim</label>
            <input
              className="w-full rounded border border-border bg-background px-3 py-1.5 text-sm"
              value={memberGroup}
              onChange={(e) => setMemberGroup(e.target.value)}
            />
          </div>
        </div>
        <div className="flex items-center gap-4">
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={autoProvision}
              onChange={(e) => setAutoProvision(e.target.checked)}
            />
            Auto-provision users
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={enabled}
              onChange={(e) => setEnabled(e.target.checked)}
            />
            Enabled
          </label>
        </div>
        <Button size="sm" onClick={handleSave} disabled={loading}>
          {loading ? "Saving…" : "Save SSO Config"}
        </Button>
      </div>
    </div>
  );
}
