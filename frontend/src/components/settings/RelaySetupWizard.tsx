import { useState, useEffect, useCallback } from "react";
import { relayApi, type RelaySetup, type AWSCredsRequest, type OCICredsRequest, type GCPCredsRequest } from "../../api/relay";
import { useToast } from "../../providers/ToastProvider";
import { Spinner } from "../ui/Spinner";
import { Check, X, Cloud, Wifi, Plus, ChevronDown, ChevronRight } from "lucide-react";

const PROVIDER_INSTRUCTIONS: Record<string, { title: string; cost: string; steps: string[] }> = {
  aws: {
    title: "AWS (paid primary — $7–8/month, most reliable)",
    cost: "~$7/month for t4g.micro Graviton2 instance. Most reliable option for production.",
    steps: [
      "Go to AWS IAM → https://console.aws.amazon.com/iam",
      "Create a user (or use an existing one) with programmatic access",
      "Attach the 'AmazonEC2FullAccess' policy (or a custom policy with ec2:RunInstances, ec2:TerminateInstances, ec2:Describe*)",
      "Go to the user → Security Credentials → Create Access Key",
      "Copy the Access Key ID (starts with AKIA...)",
      "Copy the Secret Access Key (show it once — you won't see it again)",
      "Choose a region for your relay VM (e.g. us-east-1). t4g.micro is available globally.",
    ],
  },
  oci: {
    title: "OCI (free secondary — 10 TB egress, Always Free tier)",
    cost: "$0 (Always Free tier: VM.Standard.A1.Flex, 2 OCPU, 12 GB, Arm). Up to 10 TB/month egress.",
    steps: [
      "Go to OCI Console → https://cloud.oracle.com/identity/domains",
      "Navigate to Identity & Security → Domains → your domain → Users",
      "Click your user → API Keys → Add API Key",
      "Download the private key (you won't be able to download it again)",
      "Copy the Fingerprint shown after generating the key",
      "Copy your Tenancy OCID (Profile → Tenancy → OCID)",
      "Copy your User OCID (Profile → User → OCID)",
      "Region must be your tenancy's home region for Always Free eligibility (e.g. us-ashburn-1)",
      "Paste the full private key contents, including BEGIN/END lines",
    ],
  },
  gcp: {
    title: "GCP (optional paid — IP diversity only)",
    cost: "~$7/month for e2-micro instance. GCP Always Free tier was eliminated — this is now a paid provider. Use only if you need additional IP diversity beyond AWS + OCI.",
    steps: [
      "Go to GCP Console → https://console.cloud.google.com/iam-admin/serviceaccounts",
      "Create a service account or select an existing one",
      "Assign the Compute Admin role (or a custom role with compute.instances.create/delete)",
      "Go to the service account → Keys → Add Key → Create New Key → JSON",
      "Download the JSON key file",
      "Paste the entire contents of the downloaded JSON file below",
      "The default region is us-central1. e2-micro instances qualify for free tier in us-west1/us-central1/us-east1.",
    ],
  },
};

const PROVIDER_FIELDS: Record<string, { name: string; placeholder: string; type?: string }[]> = {
  aws: [
    { name: "accessKeyId", placeholder: "Access Key ID (AKIA...)" },
    { name: "secretAccessKey", placeholder: "Secret Access Key" },
    { name: "region", placeholder: "Region (e.g. us-east-1)" },
  ],
  oci: [
    { name: "tenancy", placeholder: "Tenancy OCID" },
    { name: "user", placeholder: "User OCID" },
    { name: "fingerprint", placeholder: "API Key Fingerprint" },
    { name: "region", placeholder: "Region (e.g. us-ashburn-1)" },
    { name: "key", placeholder: "Private Key (paste full key including BEGIN/END lines)", type: "textarea" },
  ],
  gcp: [
    { name: "serviceAccountJson", placeholder: "Service Account JSON (paste entire file)", type: "textarea" },
  ],
};

const PROVIDER_DEFAULTS: Record<string, Record<string, string>> = {
  aws: { region: "us-east-1" },
  oci: { region: "us-ashburn-1" },
  gcp: {},
};

export function RelaySetupWizard({ onComplete }: { onComplete?: () => void }) {
  const [setup, setSetup] = useState<RelaySetup | null>(null);
  const [loading, setLoading] = useState(true);
  const [expandedProvider, setExpandedProvider] = useState<string | null>(null);
  const [addingProvider, setAddingProvider] = useState<string | null>(null);
  const [formData, setFormData] = useState<Record<string, Record<string, string>>>({});
  const [saving, setSaving] = useState(false);
  const [deploying, setDeploying] = useState(false);
  const { toast } = useToast();

  const load = useCallback(async () => {
    try {
      const data = await relayApi.getSetup();
      setSetup(data);
      if (data.deployed) {
        onComplete?.();
      }
    } catch {
      toast("Failed to load relay setup", "error");
    } finally {
      setLoading(false);
    }
  }, [onComplete, toast]);

  useEffect(() => {
    load();
  }, [load]);

  const getFieldValue = (provider: string, field: string) =>
    formData[provider]?.[field] ?? PROVIDER_DEFAULTS[provider]?.[field] ?? "";

  const setFieldValue = (provider: string, field: string, value: string) => {
    setFormData((prev) => ({
      ...prev,
      [provider]: { ...prev[provider], [field]: value },
    }));
  };

  const configuredProviders = ["aws", "oci", "gcp"].filter(
    (p) => setup?.[`${p}Configured` as keyof RelaySetup],
  );

  const handleSave = async (provider: string) => {
    setSaving(true);
    try {
      const fields = PROVIDER_FIELDS[provider];
      if (!fields) return;
      const data: Record<string, string> = {};
      for (const f of fields) {
        data[f.name] = getFieldValue(provider, f.name);
      }
      if (provider === "aws") await relayApi.saveAWSCreds(data as unknown as AWSCredsRequest);
      else if (provider === "oci") await relayApi.saveOCICreds(data as unknown as OCICredsRequest);
      else if (provider === "gcp") await relayApi.saveGCPCreds(data as unknown as GCPCredsRequest);
      toast(`${provider.toUpperCase()} credentials saved`);
      setAddingProvider(null);
      setFormData((prev) => ({ ...prev, [provider]: {} }));
      await load();
    } catch (e) {
      toast(e instanceof Error ? e.message : `Failed to save ${provider} credentials`, "error");
    } finally {
      setSaving(false);
    }
  };

  const handleDeploy = async () => {
    const providers = configuredProviders;
    if (providers.length === 0) {
      toast("Configure at least one provider first", "error");
      return;
    }
    setDeploying(true);
    try {
      await relayApi.deploy({
        providers,
      });
      toast("Relay fleet deployed");
      await load();
    } catch (e) {
      toast(e instanceof Error ? e.message : "Deploy failed", "error");
    } finally {
      setDeploying(false);
    }
  };

  if (loading) return <Spinner />;
  if (!setup) return <div className="text-muted-foreground">Failed to load setup state.</div>;

  const prerequisites = [
    { label: "Relay router deployed", ok: setup.routerDeployed },
    { label: "InferenceRelay CRD installed", ok: setup.crdInstalled },
  ];

  return (
    <div className="space-y-6">
      <div>
        <h3 className="text-lg font-medium">Inference Relay</h3>
        <p className="text-sm text-muted-foreground">
          Route inference traffic through cloud VMs to distribute requests across multiple IPs.
          Each provider runs a relay VM that proxies requests to the LLM upstream.
        </p>
      </div>

      {/* Prerequisites */}
      <div className="rounded-lg border border-border p-4">
        <h4 className="mb-2 text-sm font-semibold">Prerequisites</h4>
        <div className="space-y-1">
          {prerequisites.map((p) => (
            <div key={p.label} className="flex items-center gap-2 text-sm">
              {p.ok ? (
                <Check className="h-4 w-4 text-green-500" />
              ) : (
                <X className="h-4 w-4 text-red-500" />
              )}
              <span className={p.ok ? "" : "text-muted-foreground"}>{p.label}</span>
            </div>
          ))}
        </div>
      </div>

      {/* Configured providers list */}
      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <h4 className="text-sm font-semibold">Relay Providers</h4>
          {!addingProvider && (
            <button
              onClick={() => setAddingProvider("select")}
              className="flex items-center gap-1 rounded-md bg-primary px-3 py-1.5 text-sm text-primary-foreground hover:bg-primary/90"
            >
              <Plus className="h-4 w-4" /> Add Relay Provider
            </button>
          )}
        </div>

        {/* Provider selection card */}
        {addingProvider === "select" && (
          <div className="rounded-lg border border-border p-4">
            <h4 className="mb-3 text-sm font-semibold">Select Provider</h4>
            <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
              {(["aws", "oci", "gcp"] as const).map((p) => {
                const info = PROVIDER_INSTRUCTIONS[p]!;
                const configured = setup[`${p}Configured` as keyof RelaySetup] as boolean;
                return (
                  <button
                    key={p}
                    onClick={() => !configured && setAddingProvider(p)}
                    disabled={configured}
                    className={`flex flex-col items-center gap-2 rounded-lg border p-4 text-left transition-colors ${
                      configured
                        ? "border-green-500/30 bg-green-500/5 cursor-default"
                        : "border-border hover:border-primary/50 hover:bg-accent/30"
                    }`}
                  >
                    <Cloud className={`h-8 w-8 ${configured ? "text-green-500" : "text-muted-foreground"}`} />
                    <span className="font-semibold text-sm">{p.toUpperCase()}</span>
                    <span className="text-xs text-muted-foreground">{info.cost}</span>
                    {configured ? (
                      <span className="rounded-full bg-green-500/10 px-2 py-0.5 text-xs text-green-500">
                        Configured
                      </span>
                    ) : (
                      <span className="text-xs text-primary">Click to configure</span>
                    )}
                  </button>
                );
              })}
            </div>
            <div className="mt-3 flex justify-end">
              <button
                onClick={() => setAddingProvider(null)}
                className="text-sm text-muted-foreground hover:text-foreground"
              >
                Cancel
              </button>
            </div>
          </div>
        )}

        {/* Provider credential form card */}
        {addingProvider && addingProvider !== "select" && (() => {
          const provider = addingProvider;
          const info = PROVIDER_INSTRUCTIONS[provider];
          if (!info) return null;
          const fields = PROVIDER_FIELDS[provider];
          if (!fields) return null;
          return (
            <div className="rounded-lg border border-primary/30 p-4 bg-accent/5">
              <div className="mb-4">
                <div className="flex items-center gap-2 mb-1">
                  <Cloud className="h-5 w-5 text-primary" />
                  <h4 className="font-semibold">{info.title}</h4>
                </div>
              </div>

              <details className="mb-4">
                <summary className="cursor-pointer text-sm font-medium text-muted-foreground hover:text-foreground">
                  How to get {provider.toUpperCase()} credentials
                </summary>
                <div className="mt-2 ml-4 space-y-1">
                  {info.steps.map((step, i) => (
                    <p key={i} className="text-xs text-muted-foreground">
                      {i + 1}. {step}
                    </p>
                  ))}
                </div>
              </details>

              <div className="space-y-3">
                {fields.map((field) =>
                  field.type === "textarea" ? (
                    <textarea
                      key={field.name}
                      className="w-full rounded-md border border-border bg-background px-3 py-1.5 text-sm font-mono min-h-[100px] resize-y"
                      placeholder={field.placeholder}
                      value={getFieldValue(provider, field.name)}
                      onChange={(e) => setFieldValue(provider, field.name, e.target.value)}
                    />
                  ) : (
                    <input
                      key={field.name}
                      className="w-full rounded-md border border-border bg-background px-3 py-1.5 text-sm"
                      placeholder={field.placeholder}
                      value={getFieldValue(provider, field.name)}
                      onChange={(e) => setFieldValue(provider, field.name, e.target.value)}
                    />
                  ),
                )}
              </div>

              <div className="mt-3 flex justify-end gap-2">
                <button
                  onClick={() => { setAddingProvider(null); setFormData((prev) => ({ ...prev, [provider]: {} })); }}
                  className="rounded-md border border-border px-3 py-1.5 text-sm hover:bg-accent"
                >
                  Cancel
                </button>
                <button
                  onClick={() => handleSave(provider)}
                  disabled={saving || !getFieldValue(provider, PROVIDER_FIELDS[provider]?.[0]?.name ?? "")}
                  className="rounded-md bg-primary px-3 py-1.5 text-sm text-primary-foreground disabled:opacity-50"
                >
                  {saving ? "Saving..." : `Save ${provider.toUpperCase()} Credentials`}
                </button>
              </div>
            </div>
          );
        })()}

        {/* Configured provider accordion items */}
        {configuredProviders.length > 0 && (
          <div className="space-y-1">
            {configuredProviders.map((provider) => {
              const info = PROVIDER_INSTRUCTIONS[provider]!;
              const isExpanded = expandedProvider === provider;
              return (
                <div key={provider} className="rounded-md border border-border">
                  <button
                    onClick={() => setExpandedProvider(isExpanded ? null : provider)}
                    className="flex w-full items-center justify-between px-4 py-2.5 text-left hover:bg-accent/30 transition-colors"
                  >
                    <span className="flex items-center gap-2 text-sm font-medium">
                      <Cloud className="h-4 w-4" />
                      <span>{provider.toUpperCase()} Relay</span>
                      <span className="rounded-full bg-green-500/10 px-2 py-0.5 text-xs text-green-500">
                        Configured
                      </span>
                    </span>
                    <span className="text-xs text-muted-foreground">
                      {isExpanded ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
                    </span>
                  </button>
                  {isExpanded && (
                    <div className="border-t border-border px-4 py-2.5">
                      <p className="text-xs text-muted-foreground mb-2">{info.cost}</p>
                      <details className="mb-2">
                        <summary className="cursor-pointer text-xs font-medium text-muted-foreground hover:text-foreground">
                          How to get {provider.toUpperCase()} credentials
                        </summary>
                        <div className="mt-1 ml-4 space-y-0.5">
                          {info.steps.map((step, i) => (
                            <p key={i} className="text-xs text-muted-foreground">
                              {i + 1}. {step}
                            </p>
                          ))}
                        </div>
                      </details>
                      <button
                        onClick={() => {
                          setAddingProvider(provider);
                          setExpandedProvider(null);
                        }}
                        className="text-xs text-primary hover:text-primary/80"
                      >
                        Reconfigure
                      </button>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}

        {configuredProviders.length === 0 && !addingProvider && (
          <p className="text-sm text-muted-foreground">No relay providers configured yet. Add at least one to deploy.</p>
        )}
      </div>

      {/* Deploy section */}
      {configuredProviders.length > 0 && (
        <div className="rounded-lg border border-border p-4">
          <h4 className="mb-3 flex items-center gap-2 text-sm font-semibold">
            <Wifi className="h-4 w-4" /> Deploy
          </h4>
          <div className="space-y-3">
            <div className="text-sm text-muted-foreground">
              Providers:{" "}
              {configuredProviders.map((p) => p.toUpperCase()).join(", ")}
              <span className="block text-xs mt-1">
                Upstream: https://opencode.ai/zen/v1 (auto-configured)
              </span>
            </div>
            <button
              onClick={handleDeploy}
              disabled={deploying}
              className="rounded-md bg-primary px-4 py-2 text-sm text-primary-foreground disabled:opacity-50"
            >
              {deploying ? "Deploying..." : "Deploy Relay Fleet"}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
