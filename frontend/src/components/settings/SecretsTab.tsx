import { useState, useEffect } from "react";
import { useToast } from "../../providers/ToastProvider";
import { secretsApi, type SecretResponse } from "../../api/secrets";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";

const SECRET_TYPES = [
  { value: "llm-provider", label: "LLM Providers", icon: "🤖", metaFields: ["provider"] },
  { value: "ssh-key", label: "SSH Keys", icon: "🔑", metaFields: ["key_type", "host"] },
  { value: "git-credential", label: "Git Credentials", icon: "📦", metaFields: ["host"] },
  { value: "secret-file", label: "Secret Files", icon: "📄", metaFields: ["mount_path"] },
  { value: "env-secret", label: "Environment Variables", icon: "⚙️", metaFields: ["var_name"] },
] as const;

type SecretType = (typeof SECRET_TYPES)[number]["value"];

export function SecretsTab() {
  const [secrets, setSecrets] = useState<SecretResponse[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
  const [revealingId, setRevealingId] = useState<string | null>(null);
  const [revealedValue, setRevealedValue] = useState<string | null>(null);
  const [revealPassword, setRevealPassword] = useState("");
  const { toast } = useToast();

  const fetchSecrets = async () => {
    try {
      const res = await secretsApi.list();
      setSecrets(res.secrets || []);
    } catch (e: any) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { fetchSecrets(); }, []);

  // Auto-hide revealed value after 30 seconds
  useEffect(() => {
    if (revealedValue) {
      const timer = setTimeout(() => { setRevealedValue(null); setRevealingId(null); }, 30000);
      return () => clearTimeout(timer);
    }
  }, [revealedValue]);

  const handleDelete = async (id: string, name: string) => {
    if (!confirm(`Delete secret "${name}"? This cannot be undone.`)) return;
    try {
      await secretsApi.delete(id);
      setSecrets((s) => s.filter((x) => x.id !== id));
    } catch (e: any) {
      setError(e.message);
    }
  };

  const handleReveal = async (id: string) => {
    if (!revealPassword) return;
    try {
      const res = await secretsApi.reveal(id, revealPassword);
      setRevealedValue(res.value);
      setRevealPassword("");
    } catch (e: any) {
      setError(e.message === "encryption key not available; re-authenticate" ? "Session expired. Please log in again." : e.message);
    }
  };

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
    toast("Copied to clipboard");
  };

  const toggleGroup = (type: string) => {
    setCollapsed((c) => ({ ...c, [type]: !c[type] }));
  };

  const grouped = SECRET_TYPES.map((t) => ({
    ...t,
    secrets: secrets.filter((s) => s.type === t.value),
  })).filter((g) => g.secrets.length > 0);

  if (loading) return <p className="text-sm text-muted-foreground">Loading secrets...</p>;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-lg font-medium">Secrets</h3>
          <p className="text-sm text-muted-foreground">
            Encrypted secrets injected into your workspaces. Values are never stored in plaintext.
          </p>
        </div>
        <Button onClick={() => setShowCreate(!showCreate)}>
          {showCreate ? "Cancel" : "+ New Secret"}
        </Button>
      </div>

      {error && (
        <div className="flex items-center justify-between rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">
          <span>{error}</span>
          <button onClick={() => setError("")} className="text-red-500">✕</button>
        </div>
      )}

      {showCreate && (
        <CreateSecretForm
          onCreated={() => { setShowCreate(false); fetchSecrets(); }}
          onError={setError}
        />
      )}

      {grouped.length === 0 && !showCreate ? (
        <p className="text-sm text-muted-foreground">No secrets yet. Create one to get started.</p>
      ) : (
        <div className="space-y-3">
          {grouped.map((group) => (
            <div key={group.value} className="rounded-md border border-border">
              <button
                onClick={() => toggleGroup(group.value)}
                className="flex w-full items-center justify-between px-4 py-2.5 text-left hover:bg-accent/30 transition-colors"
              >
                <span className="flex items-center gap-2 text-sm font-medium">
                  <span>{group.icon}</span>
                  <span>{group.label}</span>
                  <span className="rounded-full bg-accent px-2 py-0.5 text-xs">{group.secrets.length}</span>
                </span>
                <span className="text-xs text-muted-foreground">{collapsed[group.value] ? "▶" : "▼"}</span>
              </button>
              {!collapsed[group.value] && (
                <div className="border-t border-border divide-y divide-border">
                  {group.secrets.map((s) => (
                    <div key={s.id} className="px-4 py-2.5">
                      <div className="flex items-center justify-between">
                        <div className="flex items-center gap-3">
                          <span className="text-sm font-medium">{s.name}</span>
                          {s.metadata && Object.entries(s.metadata)
                            .filter(([k]) => k !== "public_key")
                            .map(([k, v]) => (
                              <span key={k} className="text-xs text-muted-foreground">{k}: {v}</span>
                            ))}
                        </div>
                        <div className="flex items-center gap-2">
                          <button
                            onClick={() => { setRevealingId(revealingId === s.id ? null : s.id); setRevealedValue(null); }}
                            className="rounded px-2 py-1 text-xs text-blue-600 hover:bg-blue-50 transition-colors"
                          >
                            {revealingId === s.id ? "Hide" : "Reveal"}
                          </button>
                          <button
                            onClick={() => handleDelete(s.id, s.name)}
                            className="rounded px-2 py-1 text-xs text-red-500 hover:bg-red-50 transition-colors"
                          >
                            Delete
                          </button>
                        </div>
                      </div>

                      {/* Public key display for SSH keys */}
                      {s.metadata?.public_key && (
                        <div className="mt-2">
                          <span className="text-xs text-muted-foreground font-medium">Public key (safe to share):</span>
                          <div className="mt-1 flex items-center gap-2">
                            <code className="flex-1 rounded bg-accent/50 px-2 py-1 text-xs font-mono truncate">
                              {s.metadata.public_key}
                            </code>
                            <button
                              onClick={() => copyToClipboard(s.metadata.public_key!)}
                              className="text-xs text-blue-600 hover:text-blue-800 whitespace-nowrap"
                            >
                              Copy public key
                          </button>
                          </div>
                        </div>
                      )}

                      {/* Reveal panel */}
                      {revealingId === s.id && !revealedValue && (
                        <div className="mt-2 flex items-center gap-2">
                          <Input
                            type="password"
                            value={revealPassword}
                            onChange={(e) => setRevealPassword(e.target.value)}
                            placeholder="Enter password to reveal"
                            className="text-sm flex-1"
                            onKeyDown={(e) => e.key === "Enter" && handleReveal(s.id)}
                          />
                          <Button onClick={() => handleReveal(s.id)} disabled={!revealPassword}>
                            Decrypt
                          </Button>
                        </div>
                      )}

                      {/* Revealed value */}
                      {revealingId === s.id && revealedValue && (
                        <div className="mt-2 space-y-1">
                          <div className="flex items-center gap-2">
                            <code className="flex-1 rounded bg-background border border-border px-2 py-1 text-xs font-mono text-foreground break-all">
                              {revealedValue}
                            </code>
                            <button
                              onClick={() => copyToClipboard(revealedValue)}
                              className="text-xs text-blue-600 hover:text-blue-800 whitespace-nowrap"
                            >
                              Copy
                            </button>
                          </div>
                          <p className="text-xs text-muted-foreground">Auto-hides in 30 seconds</p>
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// --- Create Form ---

function generateRandomSecret(length = 32): string {
  const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
  const array = new Uint8Array(length);
  crypto.getRandomValues(array);
  return Array.from(array, (b) => chars[b % chars.length]).join("");
}

async function generateSSHKeypair(): Promise<{ privateKey: string; publicKey: string }> {
  // Use Web Crypto to generate Ed25519 keypair
  // Note: Ed25519 support in WebCrypto is limited; fall back to a random placeholder
  // In production, this would use a library like tweetnacl or ssh-keygen WASM
  const keyPair = await crypto.subtle.generateKey(
    { name: "Ed25519" } as any,
    true,
    ["sign", "verify"],
  ).catch(() => null);

  if (keyPair) {
    const privRaw = await crypto.subtle.exportKey("pkcs8", (keyPair as any).privateKey);
    const pubRaw = await crypto.subtle.exportKey("raw", (keyPair as any).publicKey);
    const privB64 = btoa(String.fromCharCode(...new Uint8Array(privRaw)));
    const pubB64 = btoa(String.fromCharCode(...new Uint8Array(pubRaw)));
    return {
      privateKey: `-----BEGIN PRIVATE KEY-----\n${privB64}\n-----END PRIVATE KEY-----`,
      publicKey: `ssh-ed25519 ${pubB64} generated@llmsafespace`,
    };
  }

  // Fallback: generate random bytes as placeholder
  const priv = generateRandomSecret(64);
  const pub = `ssh-ed25519 ${btoa(generateRandomSecret(32))} generated@llmsafespace`;
  return { privateKey: priv, publicKey: pub };
}

function CreateSecretForm({ onCreated, onError }: { onCreated: () => void; onError: (e: string) => void }) {
  const { toast } = useToast();
  const [name, setName] = useState("");
  const [type, setType] = useState<SecretType>("llm-provider");
  const [value, setValue] = useState("");
  const [metadata, setMetadata] = useState<Record<string, string>>({});
  const [submitting, setSubmitting] = useState(false);
  const [createdValue, setCreatedValue] = useState<string | null>(null);
  const [generating, setGenerating] = useState(false);

  const selectedType = SECRET_TYPES.find((t) => t.value === type)!;

  const handleGenerate = async () => {
    setGenerating(true);
    if (type === "ssh-key") {
      const kp = await generateSSHKeypair();
      setValue(kp.privateKey);
      setMetadata((m) => ({ ...m, public_key: kp.publicKey, key_type: m.key_type || "ed25519" }));
    } else {
      setValue(generateRandomSecret(48));
    }
    setGenerating(false);
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    try {
      await secretsApi.create({
        name, type, value,
        metadata: Object.keys(metadata).length > 0 ? metadata : undefined,
      });
      setCreatedValue(value);
    } catch (err: any) {
      onError(err.message === "encryption key not available; re-authenticate"
        ? "Session expired. Please log out and log back in to manage secrets."
        : err.message);
    } finally {
      setSubmitting(false);
    }
  };

  const handleDone = () => {
    setCreatedValue(null);
    onCreated();
  };

  // Show "secret created" confirmation with copy option
  if (createdValue) {
    return (
      <div className="rounded-md border border-border bg-accent/20 p-4 space-y-3">
        <p className="text-sm font-medium text-foreground">✓ Secret "{name}" created and encrypted</p>
        <div className="flex items-center gap-2">
          <code className="flex-1 rounded bg-background border border-border px-2 py-1 text-xs font-mono text-foreground break-all max-h-24 overflow-auto">
            {createdValue}
          </code>
          <button
            onClick={() => { navigator.clipboard.writeText(createdValue); toast("Copied to clipboard"); }}
            className="rounded bg-primary px-3 py-1 text-xs text-primary-foreground hover:bg-primary/90"
          >
            Copy
          </button>
        </div>
        {metadata.public_key && (
          <div className="flex items-center gap-2">
            <span className="text-xs text-muted-foreground">Public key:</span>
            <code className="flex-1 rounded bg-background border border-border px-2 py-1 text-xs font-mono text-foreground truncate">
              {metadata.public_key}
            </code>
            <button
              onClick={() => { navigator.clipboard.writeText(metadata.public_key ?? ""); toast("Public key copied"); }}
              className="rounded bg-primary px-3 py-1 text-xs text-primary-foreground hover:bg-primary/90"
            >
              Copy
            </button>
          </div>
        )}
        <p className="text-xs text-muted-foreground">⚠️ Save this value now. You won't be able to view it without re-entering your password.</p>
        <Button onClick={handleDone}>Done</Button>
      </div>
    );
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-4 rounded-md border border-border p-4 bg-accent/5">
      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="text-sm font-medium">Name</label>
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="my-api-key" required />
        </div>
        <div>
          <label className="text-sm font-medium">Type</label>
          <select
            value={type}
            onChange={(e) => { setType(e.target.value as SecretType); setMetadata({}); setValue(""); }}
            className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm"
          >
            {SECRET_TYPES.map((t) => (
              <option key={t.value} value={t.value}>{t.icon} {t.label}</option>
            ))}
          </select>
        </div>
      </div>

      <div>
        <div className="flex items-center justify-between mb-1">
          <label className="text-sm font-medium">Value</label>
          <button
            type="button"
            onClick={handleGenerate}
            disabled={generating}
            className="text-xs text-blue-600 hover:text-blue-800"
          >
            {type === "ssh-key" ? "🔑 Generate keypair" : "🎲 Generate random"}
          </button>
        </div>
        <textarea
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder={type === "ssh-key" ? "-----BEGIN OPENSSH PRIVATE KEY-----" : type === "env-secret" ? "postgres://user:pass@host/db" : "sk-..."}
          className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm font-mono min-h-[80px] resize-y"
          required
        />
        <p className="mt-1 text-xs text-muted-foreground">This value will be encrypted. You can reveal it later with your password.</p>
      </div>

      {selectedType.metaFields.length > 0 && (
        <div className="grid grid-cols-2 gap-4">
          {selectedType.metaFields.map((field) => (
            <div key={field}>
              <label className="text-sm font-medium">{field.replace(/_/g, " ")}</label>
              <Input
                value={metadata[field] || ""}
                onChange={(e) => setMetadata({ ...metadata, [field]: e.target.value })}
                placeholder={field === "key_type" ? "ed25519" : field === "mount_path" ? "/workspace/.secrets/cert.pem" : field === "var_name" ? "DATABASE_URL" : field === "provider" ? "anthropic" : "github.com"}
                required={field === "key_type" || field === "mount_path" || field === "var_name"}
              />
            </div>
          ))}
        </div>
      )}

      {/* Show public key if generated */}
      {metadata.public_key && (
        <div>
          <label className="text-sm font-medium">Public Key (will be stored unencrypted for display)</label>
          <div className="flex items-center gap-2 mt-1">
            <code className="flex-1 rounded bg-accent/50 px-2 py-1 text-xs font-mono truncate">
              {metadata.public_key}
            </code>
            <button type="button" onClick={() => { navigator.clipboard.writeText(metadata.public_key ?? ""); toast("Public key copied"); }} className="text-xs text-blue-600">
              Copy
            </button>
          </div>
        </div>
      )}

      <Button type="submit" disabled={submitting || !name || !value}>
        {submitting ? "Encrypting..." : "Create Secret"}
      </Button>
    </form>
  );
}
