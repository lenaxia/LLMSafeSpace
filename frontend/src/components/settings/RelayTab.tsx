import { useState, useEffect, useCallback } from "react";
import { relayApi, type RelaySetup } from "../../api/relay";
import { Spinner } from "../ui/Spinner";
import { RelaySetupWizard } from "./RelaySetupWizard";
import { RelayStatusDashboard } from "./RelayStatusDashboard";

export function RelayTab() {
  const [setup, setSetup] = useState<RelaySetup | null>(null);
  const [loading, setLoading] = useState(true);
  const [showWizard, setShowWizard] = useState(false);

  const load = useCallback(async () => {
    try {
      const data = await relayApi.getSetup();
      setSetup(data);
      setShowWizard(!data.deployed);
    } catch {
      setShowWizard(true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  if (loading) return <Spinner />;

  if (showWizard) {
    return <RelaySetupWizard onComplete={load} />;
  }

  return (
    <div className="space-y-4">
      <RelayStatusDashboard />
      <div className="flex justify-end">
        <button
          onClick={() => setShowWizard(true)}
          className="text-sm text-muted-foreground underline hover:text-foreground"
        >
          Reconfigure
        </button>
      </div>
    </div>
  );
}
