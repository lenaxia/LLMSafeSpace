import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { orgsApi, type OrgResponse } from "../../api/orgs";
import { Button } from "../ui/Button";
import { ApiClientError } from "../../api/client";

function slugify(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 48);
}

function CreateOrgForm({
  onCreated,
  onCancel,
}: {
  onCreated: () => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const handleSubmit = async () => {
    setError("");
    if (!name.trim()) {
      setError("Name is required");
      return;
    }
    setLoading(true);
    try {
      const finalSlug = slug.trim() || slugify(name);
      await orgsApi.create({ name: name.trim(), slug: finalSlug });
      onCreated();
    } catch (e) {
      if (e instanceof ApiClientError && e.status === 409) {
        setError("An organisation with this slug already exists");
      } else {
        setError(e instanceof Error ? e.message : "Failed to create organisation");
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="rounded border border-border bg-background p-4 space-y-3">
      <p className="text-sm font-medium">New Organisation</p>
      {error && <p className="text-xs text-red-500">{error}</p>}
      <div className="space-y-2">
        <input
          type="text"
          value={name}
          onChange={(e) => {
            setName(e.target.value);
            if (!slug || slug === slugify(name)) {
              setSlug(slugify(e.target.value));
            }
          }}
          placeholder="Organisation name"
          className="h-8 w-full rounded border border-border bg-background px-2 text-sm focus:outline-none focus:ring-1 focus:ring-ring"
        />
        <input
          type="text"
          value={slug}
          onChange={(e) => setSlug(e.target.value)}
          placeholder="slug (auto-generated from name)"
          className="h-8 w-full rounded border border-border bg-background px-2 text-sm focus:outline-none focus:ring-1 focus:ring-ring"
        />
      </div>
      <div className="flex gap-2">
        <Button size="sm" disabled={loading} onClick={handleSubmit}>
          {loading ? "Creating..." : "Create"}
        </Button>
        <Button size="sm" variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  );
}

export function OrgCard({
  org,
  onDeleted,
}: {
  org: OrgResponse;
  onDeleted: () => void;
}) {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const handleDelete = async () => {
    if (!confirm(`Delete "${org.name}"? This cannot be undone.`)) return;
    setLoading(true);
    setError("");
    try {
      await orgsApi.delete(org.id);
      onDeleted();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to delete");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="rounded border border-border p-3 space-y-1">
      <div className="flex items-center justify-between">
        <div>
          <span className="text-sm font-medium">{org.name}</span>
          <span className="ml-2 text-xs text-muted-foreground">{org.slug}</span>
        </div>
        <span className="text-xs rounded-full bg-accent px-2 py-0.5">
          {org.userRole}
        </span>
      </div>
      <div className="text-xs text-muted-foreground">
        {org.memberCount} member{org.memberCount !== 1 ? "s" : ""}
      </div>
      {error && <p className="text-xs text-red-500">{error}</p>}
      <div className="flex gap-2 pt-1">
        <Link
          to={`/orgs/${org.id}`}
          className="text-xs text-accent hover:underline"
        >
          Manage
        </Link>
        {org.userRole === "admin" && (
          <button
            onClick={handleDelete}
            disabled={loading}
            className="text-xs text-red-500 hover:text-red-400 disabled:opacity-50"
          >
            Delete
          </button>
        )}
      </div>
    </div>
  );
}

export function OrgSettingsTab() {
  const [orgs, setOrgs] = useState<OrgResponse[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [showCreate, setShowCreate] = useState(false);

  const fetchOrgs = useCallback(async () => {
    try {
      const data = await orgsApi.list();
      setOrgs(data || []);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load organisations");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchOrgs();
  }, [fetchOrgs]);

  if (loading) {
    return <div className="text-sm text-muted-foreground">Loading...</div>;
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold">Organisations</h3>
        <Button size="sm" onClick={() => setShowCreate(true)}>
          New Organisation
        </Button>
      </div>

      {error && <p className="text-xs text-red-500">{error}</p>}

      {showCreate && (
        <CreateOrgForm
          onCreated={() => {
            setShowCreate(false);
            fetchOrgs();
          }}
          onCancel={() => setShowCreate(false)}
        />
      )}

      {orgs.length === 0 && !showCreate ? (
        <p className="text-xs text-muted-foreground">
          You are not a member of any organisations.
        </p>
      ) : (
        <div className="space-y-2">
          {orgs.map((org) => (
            <OrgCard key={org.id} org={org} onDeleted={fetchOrgs} />
          ))}
        </div>
      )}
    </div>
  );
}
