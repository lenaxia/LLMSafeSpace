import { useEffect, useState } from "react";
import { Link, NavLink, Outlet, useParams } from "react-router-dom";
import { orgsApi, type OrgResponse } from "../../api/orgs";
import { ApiClientError } from "../../api/client";
import { Badge } from "../ui/Badge";
import { Spinner } from "../ui/Spinner";

export function OrgAdminLayout() {
  const { slug } = useParams<{ slug: string }>();
  const [org, setOrg] = useState<OrgResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!slug) return;
    setLoading(true);
    setError("");
    orgsApi
      .get(slug)
      .then((data) => setOrg(data))
      .catch((e) => {
        if (e instanceof ApiClientError && (e.status === 403 || e.status === 404)) {
          setError("You do not have access to this organization.");
        } else {
          setError(e instanceof Error ? e.message : "Failed to load organization");
        }
      })
      .finally(() => setLoading(false));
  }, [slug]);

  if (loading)
    return (
      <div className="flex h-screen items-center justify-center">
        <Spinner size="lg" />
      </div>
    );

  if (error || !org)
    return (
      <div className="flex h-screen flex-col items-center justify-center gap-4">
        <p className="text-sm text-red-500">{error || "Organization not found"}</p>
        <Link to="/chat" className="text-sm text-accent hover:underline">
          ← Back to Chat
        </Link>
      </div>
    );

  const isAdmin = org.userRole === "admin";

  const navItems = [
    { to: "overview", label: "Overview", adminOnly: false },
    { to: "members", label: "Members", adminOnly: true },
    { to: "credentials", label: "Credentials", adminOnly: true },
    { to: "workspaces", label: "Workspaces", adminOnly: false },
    { to: "audit", label: "Audit", adminOnly: true },
    { to: "sso", label: "SSO", adminOnly: true },
  ].filter((item) => !item.adminOnly || isAdmin);

  return (
    <div className="flex h-screen flex-col bg-background">
      <header className="flex items-center justify-between border-b border-border px-6 py-3">
        <div className="flex items-center gap-3">
          <Link
            to="/chat"
            className="text-sm text-muted-foreground hover:text-foreground"
          >
            ← Back to Chat
          </Link>
          <span className="text-border">|</span>
          <h1 className="text-lg font-semibold">{org.name}</h1>
          {org.status === "pending_activation" && (
            <Badge variant="warning">Pending Activation</Badge>
          )}
          {org.status === "suspended" && (
            <Badge variant="destructive">Suspended</Badge>
          )}
          <Badge variant="default">{org.planId}</Badge>
        </div>
        <span className="text-xs text-muted-foreground">
          {org.memberCount} member{org.memberCount !== 1 ? "s" : ""}
        </span>
      </header>

      <div className="flex flex-1 overflow-hidden">
        <nav className="w-48 border-r border-border py-2">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                `block px-4 py-2 text-sm ${
                  isActive
                    ? "bg-accent/10 font-medium text-accent"
                    : "text-muted-foreground hover:bg-muted hover:text-foreground"
                }`
              }
            >
              {item.label}
            </NavLink>
          ))}
        </nav>

        <main className="flex-1 overflow-y-auto p-6">
          <Outlet context={{ org, isAdmin }} />
        </main>
      </div>
    </div>
  );
}
