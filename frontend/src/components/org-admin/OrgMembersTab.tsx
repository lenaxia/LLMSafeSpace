import { useCallback, useEffect, useState } from "react";
import { useOutletContext } from "react-router-dom";
import {
  orgsApi,
  type OrgMember,
  type OrgInvitation,
  type OrgResponse,
} from "../../api/orgs";
import { ApiClientError } from "../../api/client";
import { Button } from "../ui/Button";
import { Badge } from "../ui/Badge";

interface MembersContext {
  org: OrgResponse;
  isAdmin: boolean;
}

export function OrgMembersTab() {
  const { org, isAdmin } = useOutletContext<MembersContext>();
  const [members, setMembers] = useState<OrgMember[]>([]);
  const [invitations, setInvitations] = useState<OrgInvitation[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [showInvite, setShowInvite] = useState(false);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [mems, invs] = await Promise.all([
        orgsApi.listMembers(org.id),
        orgsApi.listInvitations(org.id).catch(() => [] as OrgInvitation[]),
      ]);
      setMembers(mems);
      setInvitations(invs);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load members");
    } finally {
      setLoading(false);
    }
  }, [org.id]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  if (loading) return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (error) return <p className="text-sm text-red-500">{error}</p>;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-semibold">Members</h2>
        {isAdmin && (
          <Button size="sm" onClick={() => setShowInvite((s) => !s)}>
            Invite
          </Button>
        )}
      </div>

      {showInvite && isAdmin && (
        <InviteForm
          orgId={org.id}
          onDone={() => {
            setShowInvite(false);
            refresh();
          }}
        />
      )}

      <div className="rounded border border-border">
        <table className="w-full text-sm">
          <thead className="border-b border-border bg-muted/50">
            <tr>
              <th className="px-4 py-2 text-left font-medium">Name</th>
              <th className="px-4 py-2 text-left font-medium">Email</th>
              <th className="px-4 py-2 text-left font-medium">Role</th>
              <th className="px-4 py-2 text-left font-medium">Key Status</th>
              {isAdmin && <th className="px-4 py-2 text-right font-medium">Actions</th>}
            </tr>
          </thead>
          <tbody>
            {members.map((m) => (
              <tr key={m.userId} className="border-b border-border last:border-0">
                <td className="px-4 py-2 font-medium">{m.username}</td>
                <td className="px-4 py-2 text-muted-foreground">{m.email}</td>
                <td className="px-4 py-2">
                  <Badge variant={m.role === "admin" ? "default" : "muted"}>
                    {m.role}
                  </Badge>
                </td>
                <td className="px-4 py-2">
                  {m.pendingKeyWrap ? (
                    <Badge variant="warning">Pending</Badge>
                  ) : (
                    <span className="text-xs text-muted-foreground">Active</span>
                  )}
                </td>
                {isAdmin && (
                  <td className="px-4 py-2 text-right">
                    <MemberActions
                      orgId={org.id}
                      member={m}
                      onChanged={refresh}
                    />
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {isAdmin && invitations.length > 0 && (
        <div>
          <h3 className="mb-2 text-sm font-medium">Pending Invitations</h3>
          <div className="rounded border border-border">
            <table className="w-full text-sm">
              <thead className="border-b border-border bg-muted/50">
                <tr>
                  <th className="px-4 py-2 text-left font-medium">Email</th>
                  <th className="px-4 py-2 text-left font-medium">Role</th>
                  <th className="px-4 py-2 text-left font-medium">Expires</th>
                  <th className="px-4 py-2 text-right font-medium">Actions</th>
                </tr>
              </thead>
              <tbody>
                {invitations.map((inv) => (
                  <tr key={inv.id} className="border-b border-border last:border-0">
                    <td className="px-4 py-2">{inv.email}</td>
                    <td className="px-4 py-2">
                      <Badge variant={inv.role === "admin" ? "default" : "muted"}>
                        {inv.role}
                      </Badge>
                    </td>
                    <td className="px-4 py-2 text-muted-foreground">
                      {new Date(inv.expiresAt).toLocaleDateString()}
                    </td>
                    <td className="px-4 py-2 text-right">
                      <div className="flex justify-end gap-2">
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={async () => {
                            try {
                              await orgsApi.resendInvitation(org.id, inv.id);
                              refresh();
                            } catch (e) {
                              setError(
                                e instanceof Error ? e.message : "Resend failed",
                              );
                            }
                          }}
                        >
                          Resend
                        </Button>
                        <Button
                          size="sm"
                          variant="destructive"
                          onClick={async () => {
                            try {
                              await orgsApi.revokeInvitation(org.id, inv.id);
                              refresh();
                            } catch (e) {
                              setError(
                                e instanceof Error ? e.message : "Revoke failed",
                              );
                            }
                          }}
                        >
                          Revoke
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {error && <p className="text-sm text-red-500">{error}</p>}
    </div>
  );
}

function MemberActions({
  orgId,
  member,
  onChanged,
}: {
  orgId: string;
  member: OrgMember;
  onChanged: () => void;
}) {
  return (
    <div className="flex justify-end gap-2">
      {member.pendingKeyWrap && member.role === "admin" && (
        <span className="text-xs text-yellow-600">Awaiting key setup</span>
      )}
      <Button
        size="sm"
        variant="ghost"
        onClick={async () => {
          const newRole = member.role === "admin" ? "member" : "admin";
          try {
            await orgsApi.changeMemberRole(orgId, member.userId, newRole);
            onChanged();
          } catch {
            /* handled by parent */
          }
        }}
      >
        {member.role === "admin" ? "Demote" : "Promote"}
      </Button>
      <Button
        size="sm"
        variant="destructive"
        onClick={async () => {
          try {
            await orgsApi.removeMember(orgId, member.userId);
            onChanged();
          } catch {
            /* handled by parent */
          }
        }}
      >
        Remove
      </Button>
    </div>
  );
}

function InviteForm({
  orgId,
  onDone,
}: {
  orgId: string;
  onDone: () => void;
}) {
  const [emails, setEmails] = useState("");
  const [role, setRole] = useState<"admin" | "member">("member");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const handleSubmit = async () => {
    const list = emails
      .split(/[,\n\s]+/)
      .map((e) => e.trim())
      .filter(Boolean);
    if (list.length === 0) {
      setError("Enter at least one email");
      return;
    }
    setLoading(true);
    setError("");
    try {
      await orgsApi.createInvitations(orgId, { emails: list, role });
      onDone();
    } catch (e) {
      if (e instanceof ApiClientError && e.status === 429) {
        setError("Rate limit exceeded. Try again later.");
      } else {
        setError(e instanceof Error ? e.message : "Failed to send invitations");
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="rounded border border-border bg-muted/30 p-4">
      <h3 className="mb-3 text-sm font-medium">Invite Members</h3>
      {error && <p className="mb-2 text-xs text-red-500">{error}</p>}
      <div className="space-y-3">
        <textarea
          className="w-full rounded border border-border bg-background p-2 text-sm"
          rows={3}
          placeholder="Email addresses (comma or newline separated)"
          value={emails}
          onChange={(e) => setEmails(e.target.value)}
        />
        <div className="flex items-center gap-3">
          <select
            className="rounded border border-border bg-background px-2 py-1 text-sm"
            value={role}
            onChange={(e) => setRole(e.target.value as "admin" | "member")}
          >
            <option value="member">Member</option>
            <option value="admin">Admin</option>
          </select>
          <Button size="sm" onClick={handleSubmit} disabled={loading}>
            {loading ? "Sending…" : "Send Invitations"}
          </Button>
        </div>
      </div>
    </div>
  );
}
