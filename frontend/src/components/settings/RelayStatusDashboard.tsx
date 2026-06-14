import { useState, useEffect, useCallback } from "react";
import { ApiClientError } from "../../api/client";
import {
  relayApi,
  type RelayStatus,
  type RelayInstance,
} from "../../api/relay";
import { useToast } from "../../providers/ToastProvider";
import { Spinner } from "../ui/Spinner";
import {
  Activity,
  AlertTriangle,
  RefreshCw,
  Pause,
  Play,
  Cloud,
  Zap,
} from "lucide-react";

export function RelayStatusDashboard() {
  const [status, setStatus] = useState<RelayStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [paused, setPaused] = useState(false);
  const { toast } = useToast();

  const load = useCallback(async () => {
    try {
      const data = await relayApi.getStatus();
      setStatus(data);
    } catch (e) {
      if (e instanceof ApiClientError && e.status === 404) {
        setStatus(null);
      } else {
        toast(e instanceof Error ? e.message : "Failed to load status", "error");
      }
    } finally {
      setLoading(false);
    }
  }, [toast]);

  useEffect(() => {
    load();
    const interval = setInterval(load, 15000);
    return () => clearInterval(interval);
  }, [load]);

  const handleRotate = async (id: string) => {
    try {
      await relayApi.rotate(id);
      toast(`Rotation triggered for ${id}`);
      await load();
    } catch (e) {
      toast(e instanceof Error ? e.message : "Rotation failed", "error");
    }
  };

  const handlePauseToggle = async () => {
    try {
      if (paused) {
        await relayApi.resume();
        setPaused(false);
        toast("Relay fleet resumed");
      } else {
        await relayApi.pause();
        setPaused(true);
        toast("Relay fleet paused");
      }
    } catch (e) {
      toast(e instanceof Error ? e.message : "Action failed", "error");
    }
  };

  if (loading) return <Spinner />;
  if (!status || !status.deployed) {
    return (
      <div className="text-muted-foreground">
        No relay fleet deployed. Use the setup wizard to configure.
      </div>
    );
  }

  const overallColor =
    status.overall === "healthy"
      ? "text-green-500"
      : status.overall === "degraded"
        ? "text-yellow-500"
        : "text-red-500";

  return (
    <div className="space-y-4">
      {/* Fleet overview */}
      <div className="flex items-center justify-between rounded-lg border border-border p-4">
        <div className="flex items-center gap-4">
          <span className={`text-lg font-semibold ${overallColor}`}>
            ● {status.overall.charAt(0).toUpperCase() + status.overall.slice(1)}
          </span>
          <span className="text-sm text-muted-foreground">
            {status.healthyReplicas}/{status.totalReplicas} relays active
          </span>
          {status.fallbackActive && (
            <span className="flex items-center gap-1 text-sm text-yellow-500">
              <AlertTriangle className="h-4 w-4" /> Fallback active
            </span>
          )}
          <span className="flex items-center gap-1 text-sm text-muted-foreground">
            <Activity className="h-4 w-4" /> {status.activeStreams} streams
          </span>
        </div>
        <button
          onClick={handlePauseToggle}
          className="flex items-center gap-1 rounded-md border border-border px-3 py-1.5 text-sm hover:bg-accent/50"
        >
          {paused ? <Play className="h-4 w-4" /> : <Pause className="h-4 w-4" />}
          {paused ? "Resume" : "Pause"}
        </button>
      </div>

      {/* Per-relay cards */}
      {status.instances.map((inst) => (
        <RelayInstanceCard
          key={inst.id}
          instance={inst}
          onRotate={handleRotate}
        />
      ))}

      {/* Alert rules */}
      {status.alerts.length > 0 && (
        <div className="rounded-lg border border-border p-4">
          <h3 className="mb-2 text-sm font-semibold">Alerting Rules</h3>
          <div className="space-y-1">
            {status.alerts.map((alert) => (
              <div key={alert.name} className="flex items-center justify-between text-sm">
                <span className="font-mono text-xs">{alert.name}</span>
                <span
                  className={`rounded px-2 py-0.5 text-xs ${
                    alert.firing
                      ? "bg-red-500/20 text-red-500"
                      : "bg-green-500/20 text-green-500"
                  }`}
                >
                  {alert.firing ? "FIRING" : "OK"}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Recent events */}
      {status.recentEvents.length > 0 && (
        <div className="rounded-lg border border-border p-4">
          <h3 className="mb-2 text-sm font-semibold">Recent Events</h3>
          <div className="space-y-1">
            {status.recentEvents.map((evt, i) => (
              <div key={i} className="flex items-center gap-2 text-sm">
                <span className="text-xs text-muted-foreground">
                  {new Date(evt.timestamp).toLocaleString()}
                </span>
                <span>{evt.message}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function RelayInstanceCard({
  instance,
  onRotate,
}: {
  instance: RelayInstance;
  onRotate: (id: string) => void;
}) {
  const healthy = instance.state === "healthy";
  const colorClass = healthy ? "text-green-500" : "text-red-500";

  return (
    <div className="rounded-lg border border-border p-4">
      <div className="mb-2 flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Cloud className="h-5 w-5 text-muted-foreground" />
          <span className="font-semibold uppercase">{instance.provider}</span>
          <span className="text-sm text-muted-foreground">{instance.region}</span>
          <span className={`text-sm font-semibold ${colorClass}`}>
            ● {instance.state}
          </span>
        </div>
        <button
          onClick={() => onRotate(instance.id)}
          className="flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs hover:bg-accent/50"
        >
          <RefreshCw className="h-3 w-3" /> Rotate
        </button>
      </div>

      <div className="grid grid-cols-2 gap-2 text-sm md:grid-cols-4">
        <div>
          <span className="text-muted-foreground">IP: </span>
          <span className="font-mono">{instance.publicIP || "—"}</span>
        </div>
        <div>
          <span className="text-muted-foreground">WG: </span>
          <span className="font-mono">{instance.wgIP || "—"}</span>
        </div>
        <div>
          <span className="text-muted-foreground">Requests: </span>
          <span>{instance.metrics.requestsToday.toLocaleString()}</span>
        </div>
        <div>
          <span className="text-muted-foreground">Cost: </span>
          <span>
            ${(instance.cost.spentThisMonth / 100).toFixed(2)} / $
            {(instance.cost.monthlyEstimate / 100).toFixed(2)}
          </span>
        </div>
      </div>

      {/* Error state (US-43.10) */}
      {instance.lastProvisionError && (
        <div className="mt-2 flex items-start gap-2 rounded-md bg-red-500/10 p-2 text-sm">
          <AlertTriangle className="h-4 w-4 shrink-0 text-red-500" />
          <div>
            <span className="font-semibold text-red-500">Provisioning failed</span>
            <p className="text-muted-foreground">{instance.lastProvisionError}</p>
          </div>
        </div>
      )}

      {/* 429 rate indicator */}
      {instance.metrics.requests429Today > 0 && (
        <div className="mt-1 flex items-center gap-1 text-xs text-yellow-500">
          <Zap className="h-3 w-3" />
          {instance.metrics.requests429Today} rate-limited requests today
        </div>
      )}
    </div>
  );
}
