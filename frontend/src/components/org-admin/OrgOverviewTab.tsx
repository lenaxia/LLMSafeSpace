import { useOutletContext } from "react-router-dom";
import type { OrgResponse } from "../../api/orgs";
import { Badge } from "../ui/Badge";
import { DangerZone } from "./DangerZone";

interface OverviewContext {
  org: OrgResponse;
  isAdmin: boolean;
}

export function OrgOverviewTab() {
  const { org, isAdmin } = useOutletContext<OverviewContext>();

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold">Overview</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          Organization summary and plan status.
        </p>
      </div>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <StatCard label="Members" value={org.memberCount} />
        <StatCard label="Plan" value={org.planId} />
        <StatCard
          label="Subscription"
          value={org.subscriptionStatus}
        />
      </div>

      <div className="rounded border border-border p-4">
        <h3 className="mb-3 text-sm font-medium">Plan Details</h3>
        <dl className="grid grid-cols-2 gap-2 text-sm">
          <dt className="text-muted-foreground">Status</dt>
          <dd>
            <Badge
            variant={
              org.status === "active"
                ? "success"
                : org.status === "suspended"
                  ? "destructive"
                  : "warning"
            }
            >
              {org.status}
            </Badge>
          </dd>
          <dt className="text-muted-foreground">Plan</dt>
          <dd className="font-mono">{org.planId}</dd>
          <dt className="text-muted-foreground">Subscription</dt>
          <dd className="font-mono">{org.subscriptionStatus}</dd>
          <dt className="text-muted-foreground">Slug</dt>
          <dd className="font-mono">{org.slug}</dd>
          <dt className="text-muted-foreground">Created</dt>
          <dd>{new Date(org.createdAt).toLocaleDateString()}</dd>
        </dl>
      </div>

      {isAdmin && <DangerZone orgId={org.id} orgName={org.name} />}
    </div>
  );
}

function StatCard({
  label,
  value,
}: {
  label: string;
  value: string | number;
}) {
  return (
    <div className="rounded border border-border p-4">
      <p className="text-xs uppercase tracking-wide text-muted-foreground">
        {label}
      </p>
      <p className="mt-1 text-2xl font-semibold">{value}</p>
    </div>
  );
}
