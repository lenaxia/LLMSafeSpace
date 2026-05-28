import { useEffect, useState } from "react";
import { credentialsApi, type CredentialSet, type RotateKeyResult } from "../../api/credentials";
import { Spinner } from "../ui/Spinner";
import { Shield, Trash2, Star } from "lucide-react";

export function AdminCredentialsTab() {
  const [sets, setSets] = useState<CredentialSet[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [rotateResult, setRotateResult] = useState<RotateKeyResult | null>(null);

  const load = async () => {
    try {
      const data = await credentialsApi.list();
      setSets(data);
    } catch (e: unknown) {
      if (e instanceof Error && e.message.includes("404")) {
        setError("not-admin");
      } else {
        setError(e instanceof Error ? e.message : "Failed to load");
      }
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { load(); }, []);

  const handleDelete = async (id: string) => {
    if (!confirm("Delete this credential set?")) return;
    try {
      await credentialsApi.delete(id);
      setSets((prev) => prev.filter((s) => s.id !== id));
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : "Delete failed");
    }
  };

  const handleSetDefault = async (id: string) => {
    try {
      await credentialsApi.setDefault(id);
      setSets((prev) => prev.map((s) => ({ ...s, isDefault: s.id === id })));
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : "Failed to set default");
    }
  };

  const handleRotate = async () => {
    try {
      const result = await credentialsApi.rotateKey();
      setRotateResult(result);
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : "Rotation failed");
    }
  };

  if (loading) return <div className="flex justify-center p-8"><Spinner /></div>;
  if (error === "not-admin") return null;
  if (error) return <p className="text-destructive p-4">{error}</p>;

  return (
    <div className="max-w-3xl mx-auto space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold">Credential Sets</h2>
        <button
          onClick={handleRotate}
          className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-accent"
        >
          Rotate Encryption Key
        </button>
      </div>

      {rotateResult && (
        <div className="rounded-md border border-border bg-muted/50 p-3 text-xs">
          Rotation complete: {rotateResult.rotated} rotated, {rotateResult.alreadyCurrent} already current, {rotateResult.errors} errors
        </div>
      )}

      {sets.length === 0 ? (
        <p className="text-sm text-muted-foreground">No credential sets configured.</p>
      ) : (
        <div className="space-y-3">
          {sets.map((cs) => (
            <div key={cs.id} className="flex items-center gap-3 rounded-md border border-border p-3">
              <Shield className="h-4 w-4 text-muted-foreground shrink-0" />
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium">{cs.name}</span>
                  {cs.isDefault && (
                    <span className="rounded bg-primary/10 px-1.5 py-0.5 text-xs text-primary">default</span>
                  )}
                </div>
                <p className="text-xs text-muted-foreground">
                  Providers: {cs.providers.join(", ") || "none"} · Models: {cs.modelAllowlist.length || "all"}
                </p>
              </div>
              <div className="flex gap-1 shrink-0">
                {!cs.isDefault && (
                  <button
                    onClick={() => handleSetDefault(cs.id)}
                    className="rounded p-1.5 hover:bg-accent"
                    title="Set as default"
                  >
                    <Star className="h-3.5 w-3.5" />
                  </button>
                )}
                <button
                  onClick={() => handleDelete(cs.id)}
                  className="rounded p-1.5 hover:bg-destructive/10 text-destructive"
                  title="Delete"
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
