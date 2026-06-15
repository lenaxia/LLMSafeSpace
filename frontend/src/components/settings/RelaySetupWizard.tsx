import { useState, useEffect, useCallback } from "react";
import { relayApi, type RelaySetup } from "../../api/relay";
import { useToast } from "../../providers/ToastProvider";
import { Spinner } from "../ui/Spinner";
import { Check, X, Cloud, Server, Wifi } from "lucide-react";

export function RelaySetupWizard({ onComplete }: { onComplete?: () => void }) {
  const [setup, setSetup] = useState<RelaySetup | null>(null);
  const [loading, setLoading] = useState(true);
  const [step, setStep] = useState(0);
  const [ociCreds, setOCICreds] = useState({
    tenancy: "",
    user: "",
    fingerprint: "",
    key: "",
    region: "us-ashburn-1",
  });
  const [gcpCreds, setGCPCreds] = useState({ serviceAccountJson: "" });
  const [deployConfig, setDeployConfig] = useState({
    upstreamURL: "https://opencode.ai/zen/v1",
    routerEndpoint: "",
    providers: { oci: true, gcp: true },
  });
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

  const handleSaveOCI = async () => {
    try {
      await relayApi.saveOCICreds(ociCreds);
      toast("OCI credentials saved");
      await load();
    } catch (e) {
      toast(e instanceof Error ? e.message : "Failed to save OCI credentials", "error");
    }
  };

  const handleSaveGCP = async () => {
    try {
      await relayApi.saveGCPCreds(gcpCreds);
      toast("GCP credentials saved");
      await load();
    } catch (e) {
      toast(e instanceof Error ? e.message : "Failed to save GCP credentials", "error");
    }
  };

  const handleDeploy = async () => {
    const providers = Object.entries(deployConfig.providers)
      .filter(([, enabled]) => enabled)
      .map(([name]) => name);
    if (providers.length === 0) {
      toast("Select at least one provider", "error");
      return;
    }
    if (!deployConfig.routerEndpoint) {
      toast("WireGuard endpoint is required", "error");
      return;
    }
    setDeploying(true);
    try {
      await relayApi.deploy({
        upstreamURL: deployConfig.upstreamURL,
        routerEndpoint: deployConfig.routerEndpoint,
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
    { label: "MetalLB installed", ok: setup.metalLBInstalled },
    { label: "Relay router deployed", ok: setup.routerDeployed },
    { label: "InferenceRelay CRD installed", ok: setup.crdInstalled },
  ];

  const steps = [
    {
      title: "Prerequisites",
      icon: Server,
      content: (
        <div className="space-y-2">
          {prerequisites.map((p) => (
            <div key={p.label} className="flex items-center gap-2">
              {p.ok ? <Check className="h-4 w-4 text-green-500" /> : <X className="h-4 w-4 text-red-500" />}
              <span className={p.ok ? "" : "text-muted-foreground"}>{p.label}</span>
            </div>
          ))}
        </div>
      ),
    },
    {
      title: "OCI Credentials",
      icon: Cloud,
      content: (
        <div className="space-y-2">
          <input className="w-full rounded-md border border-border bg-background px-3 py-1.5 text-sm" placeholder="Tenancy OCID" value={ociCreds.tenancy} onChange={(e) => setOCICreds({ ...ociCreds, tenancy: e.target.value })} />
          <input className="w-full rounded-md border border-border bg-background px-3 py-1.5 text-sm" placeholder="User OCID" value={ociCreds.user} onChange={(e) => setOCICreds({ ...ociCreds, user: e.target.value })} />
          <input className="w-full rounded-md border border-border bg-background px-3 py-1.5 text-sm" placeholder="Fingerprint" value={ociCreds.fingerprint} onChange={(e) => setOCICreds({ ...ociCreds, fingerprint: e.target.value })} />
          <textarea className="w-full rounded-md border border-border bg-background px-3 py-1.5 text-sm font-mono" rows={4} placeholder="API Private Key" value={ociCreds.key} onChange={(e) => setOCICreds({ ...ociCreds, key: e.target.value })} />
          <button onClick={handleSaveOCI} disabled={!ociCreds.tenancy} className="rounded-md bg-primary px-3 py-1.5 text-sm text-primary-foreground disabled:opacity-50">Save</button>
          {setup.ociConfigured && <span className="ml-2 text-sm text-green-500">OCI configured</span>}
        </div>
      ),
    },
    {
      title: "GCP Credentials",
      icon: Cloud,
      content: (
        <div className="space-y-2">
          <textarea className="w-full rounded-md border border-border bg-background px-3 py-1.5 text-sm font-mono" rows={8} placeholder="Service Account JSON" value={gcpCreds.serviceAccountJson} onChange={(e) => setGCPCreds({ serviceAccountJson: e.target.value })} />
          <button onClick={handleSaveGCP} disabled={!gcpCreds.serviceAccountJson} className="rounded-md bg-primary px-3 py-1.5 text-sm text-primary-foreground disabled:opacity-50">Save</button>
          {setup.gcpConfigured && <span className="ml-2 text-sm text-green-500">GCP configured</span>}
        </div>
      ),
    },
    {
      title: "Deploy",
      icon: Wifi,
      content: (
        <div className="space-y-3">
          <input className="w-full rounded-md border border-border bg-background px-3 py-1.5 text-sm" placeholder="WireGuard endpoint (relay-gw.example.com:51820)" value={deployConfig.routerEndpoint} onChange={(e) => setDeployConfig({ ...deployConfig, routerEndpoint: e.target.value })} />
          <input className="w-full rounded-md border border-border bg-background px-3 py-1.5 text-sm" placeholder="Upstream URL" value={deployConfig.upstreamURL} onChange={(e) => setDeployConfig({ ...deployConfig, upstreamURL: e.target.value })} />
          <div className="space-y-1">
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={deployConfig.providers.oci} onChange={(e) => setDeployConfig({ ...deployConfig, providers: { ...deployConfig.providers, oci: e.target.checked } })} />
              OCI (primary, Always Free — 10 TB egress)
            </label>
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={deployConfig.providers.gcp} onChange={(e) => setDeployConfig({ ...deployConfig, providers: { ...deployConfig.providers, gcp: e.target.checked } })} />
              GCP (failover, Always Free — 1 GB egress)
            </label>
          </div>
          <button onClick={handleDeploy} disabled={deploying} className="rounded-md bg-primary px-3 py-1.5 text-sm text-primary-foreground disabled:opacity-50">
            {deploying ? "Deploying..." : "Deploy Relay Fleet"}
          </button>
        </div>
      ),
    },
  ];

  const currentStep = steps[step];
  const StepIcon = currentStep?.icon;

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        {steps.map((s, i) => (
          <div key={s.title} className="flex items-center">
            <button
              onClick={() => setStep(i)}
              className={`flex items-center gap-1 rounded-md px-3 py-1.5 text-sm transition-colors ${
                i === step ? "bg-accent text-accent-foreground" : "text-muted-foreground hover:bg-accent/50"
              }`}
            >
              <span className={`flex h-5 w-5 items-center justify-center rounded-full text-xs ${
                i < step ? "bg-green-500 text-white" : i === step ? "bg-primary text-primary-foreground" : "bg-muted"
              }`}>
                {i < step ? <Check className="h-3 w-3" /> : i + 1}
              </span>
              {s.title}
            </button>
            {i < steps.length - 1 && <span className="mx-1 text-muted-foreground">→</span>}
          </div>
        ))}
      </div>

      <div className="rounded-lg border border-border p-4">
        <div className="mb-3 flex items-center gap-2">
          {StepIcon && <StepIcon className="h-5 w-5" />}
          <h3 className="font-semibold">{currentStep?.title}</h3>
        </div>
        {currentStep?.content}
      </div>

      <div className="flex justify-between">
        <button onClick={() => setStep(Math.max(0, step - 1))} disabled={step === 0} className="rounded-md border border-border px-3 py-1.5 text-sm disabled:opacity-50">← Back</button>
        <button onClick={() => setStep(Math.min(steps.length - 1, step + 1))} disabled={step === steps.length - 1} className="rounded-md border border-border px-3 py-1.5 text-sm disabled:opacity-50">Next →</button>
      </div>
    </div>
  );
}
