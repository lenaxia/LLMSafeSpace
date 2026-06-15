import { useEffect, useState } from "react";
import { useOutletContext } from "react-router-dom";
import { orgsApi, type OrgResponse } from "../../api/orgs";
import { Button } from "../ui/Button";
import { Badge } from "../ui/Badge";

interface BillingContext {
  org: OrgResponse;
  isAdmin: boolean;
}

export function OrgBillingTab() {
  const { org } = useOutletContext<BillingContext>();
  const [portalUrl, setPortalUrl] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const handlePortal = async () => {
    setLoading(true);
    setError("");
    try {
      const resp = await orgsApi.portal(org.id);
      setPortalUrl(resp.url);
      window.location.href = resp.url;
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to open billing portal");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (portalUrl) {
      window.location.href = portalUrl;
    }
  }, [portalUrl]);

  const subBadge = () => {
    switch (org.subscriptionStatus) {
      case "active":
        return <Badge variant="success">Active</Badge>;
      case "trialing":
        return <Badge variant="default">Trial</Badge>;
      case "past_due":
        return <Badge variant="warning">Past Due</Badge>;
      case "canceled":
      case "unpaid":
        return <Badge variant="destructive">{org.subscriptionStatus}</Badge>;
      default:
        return <Badge variant="muted">Inactive</Badge>;
    }
  };

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold">Billing</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          Manage your subscription and billing via Stripe.
        </p>
      </div>

      {error && <p className="text-sm text-red-500">{error}</p>}

      <div className="rounded border border-border p-4">
        <dl className="grid grid-cols-2 gap-3 text-sm">
          <dt className="text-muted-foreground">Plan</dt>
          <dd className="font-mono font-medium">{org.planId}</dd>
          <dt className="text-muted-foreground">Status</dt>
          <dd>{subBadge()}</dd>
          <dt className="text-muted-foreground">Members</dt>
          <dd>{org.memberCount}</dd>
        </dl>
      </div>

      <div className="rounded border border-border p-4">
        <h3 className="mb-3 text-sm font-medium">Manage Subscription</h3>
        <p className="mb-4 text-xs text-muted-foreground">
          Upgrade, downgrade, update payment methods, or cancel your subscription
          through the Stripe Customer Portal.
        </p>
        <Button size="sm" onClick={handlePortal} disabled={loading}>
          {loading ? "Opening…" : "Manage in Stripe"}
        </Button>
      </div>
    </div>
  );
}
