/**
 * useRelayClient — Browser-side relay for client-proxied inference (Epic 26).
 *
 * Connects a WebSocket to the API server's relay endpoint. Receives proxy
 * requests from the in-pod agentd, executes them via browser fetch(), and
 * streams the responses back.
 */
import { useCallback, useEffect, useRef, useState } from "react";
import { getEnv } from "../env";

// Protocol types (mirrors pkg/relay/protocol.go)
interface ProxyRequest {
  type: "proxy_request";
  id: string;
  method: string;
  url: string;
  headers: Record<string, string>;
  body?: string;
}

interface ProxyResponseStart {
  type: "proxy_response_start";
  id: string;
  status: number;
  headers?: Record<string, string>;
}

interface ProxyResponseChunk {
  type: "proxy_response_chunk";
  id: string;
  data: string;
}

interface ProxyResponseEnd {
  type: "proxy_response_end";
  id: string;
}

interface ProxyError {
  type: "proxy_error";
  id: string;
  error: string;
  status: number;
}

type RelayMessage =
  | ProxyResponseStart
  | ProxyResponseChunk
  | ProxyResponseEnd
  | ProxyError;

export type RelayStatus = "disconnected" | "connecting" | "connected" | "error";

interface UseRelayClientOptions {
  workspaceId: string | null;
  enabled?: boolean;
}

interface UseRelayClientResult {
  status: RelayStatus;
  activeRequests: number;
}

export function useRelayClient({
  workspaceId,
  enabled = true,
}: UseRelayClientOptions): UseRelayClientResult {
  const [status, setStatus] = useState<RelayStatus>("disconnected");
  const [activeRequests, setActiveRequests] = useState(0);
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const backoffRef = useRef(1000);

  const send = useCallback((msg: RelayMessage) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(msg));
    }
  }, []);

  const handleProxyRequest = useCallback(
    async (req: ProxyRequest) => {
      setActiveRequests((n) => n + 1);
      try {
        const fetchInit: RequestInit = {
          method: req.method,
          headers: req.headers,
        };
        if (req.body && req.method !== "GET" && req.method !== "HEAD") {
          fetchInit.body = req.body;
        }

        const resp = await fetch(req.url, fetchInit);

        // Send response start
        const respHeaders: Record<string, string> = {};
        resp.headers.forEach((v, k) => {
          respHeaders[k] = v;
        });
        send({
          type: "proxy_response_start",
          id: req.id,
          status: resp.status,
          headers: respHeaders,
        });

        // Stream body
        if (resp.body) {
          const reader = resp.body.getReader();
          const decoder = new TextDecoder();
          while (true) {
            const { done, value } = await reader.read();
            if (done) break;
            send({
              type: "proxy_response_chunk",
              id: req.id,
              data: decoder.decode(value, { stream: true }),
            });
          }
        }

        // Signal end
        send({ type: "proxy_response_end", id: req.id });
      } catch (err) {
        // CORS or network error
        const errorMsg =
          err instanceof TypeError ? "CORS blocked or network error" : String(err);
        send({
          type: "proxy_error",
          id: req.id,
          error: errorMsg,
          status: 0,
        });
      } finally {
        setActiveRequests((n) => Math.max(0, n - 1));
      }
    },
    [send],
  );

  const connect = useCallback(() => {
    if (!workspaceId || !enabled) return;

    const { apiBaseUrl } = getEnv();
    const wsProtocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const wsBase = apiBaseUrl.replace(/^https?:/, wsProtocol);
    const url = `${wsBase}/workspaces/${workspaceId}/relay?role=client`;

    setStatus("connecting");
    const ws = new WebSocket(url);
    wsRef.current = ws;

    ws.onopen = () => {
      setStatus("connected");
      backoffRef.current = 1000; // reset backoff on success
    };

    ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data);
        if (msg.type === "proxy_request") {
          handleProxyRequest(msg as ProxyRequest);
        } else if (msg.type === "ping") {
          ws.send(JSON.stringify({ type: "pong" }));
        }
      } catch {
        // Ignore malformed messages
      }
    };

    ws.onclose = () => {
      setStatus("disconnected");
      wsRef.current = null;
      // Reconnect with exponential backoff
      if (enabled && workspaceId) {
        reconnectTimer.current = setTimeout(() => {
          backoffRef.current = Math.min(backoffRef.current * 2, 30000);
          connect();
        }, backoffRef.current);
      }
    };

    ws.onerror = () => {
      setStatus("error");
    };
  }, [workspaceId, enabled, handleProxyRequest, send]);

  useEffect(() => {
    if (enabled && workspaceId) {
      connect();
    }
    return () => {
      if (reconnectTimer.current) {
        clearTimeout(reconnectTimer.current);
      }
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
      setStatus("disconnected");
    };
  }, [workspaceId, enabled, connect]);

  return { status, activeRequests };
}
