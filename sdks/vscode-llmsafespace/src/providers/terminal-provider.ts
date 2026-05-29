import * as vscode from "vscode";
import WebSocket from "ws";
import { ApiService } from "../services/api";

const MAX_RECONNECT_ATTEMPTS = 3;
const RECONNECT_DELAY_MS = 2000;

/**
 * Opens a WebSocket terminal connected to a workspace pod.
 * Uses the one-time ticket pattern from US-14.2.
 */
export function registerTerminalCommand(context: vscode.ExtensionContext, api: ApiService) {
  return vscode.commands.registerCommand("llmsafespace.openTerminal", async (item: any) => {
    if (!item?.workspace?.id) {
      vscode.window.showErrorMessage("No workspace selected");
      return;
    }

    const workspaceId = item.workspace.id;
    const workspaceName = item.workspace.name || workspaceId;

    await vscode.window.withProgress(
      { location: vscode.ProgressLocation.Notification, title: `Connecting terminal to "${workspaceName}"...` },
      async () => {
        try {
          const ticket = await api.getTerminalTicket(workspaceId);
          const apiUrl = vscode.workspace.getConfiguration("llmsafespace").get<string>("apiUrl") || "";
          const wsUrl = apiUrl.replace(/^http/, "ws").replace(/\/$/, "");
          const terminalUrl = `${wsUrl}/api/v1/workspaces/${workspaceId}/terminal?ticket=${ticket}`;

          const pty = new WebSocketPty(terminalUrl, workspaceId, api);
          const terminal = vscode.window.createTerminal({
            name: `🔒 ${workspaceName}`,
            pty,
            iconPath: new vscode.ThemeIcon("terminal"),
          });
          terminal.show();
        } catch (e: any) {
          vscode.window.showErrorMessage(`Failed to open terminal: ${e.message}`);
        }
      },
    );
  });
}

/**
 * WebSocket-backed pseudo-terminal with reconnection support.
 */
class WebSocketPty implements vscode.Pseudoterminal {
  private writeEmitter = new vscode.EventEmitter<string>();
  private closeEmitter = new vscode.EventEmitter<number | void>();
  onDidWrite = this.writeEmitter.event;
  onDidClose = this.closeEmitter.event;

  private ws: WebSocket | undefined;
  private reconnectAttempts = 0;
  private closed = false;
  private dimensions: { cols: number; rows: number } = { cols: 80, rows: 24 };

  constructor(
    private url: string,
    private workspaceId: string,
    private api: ApiService,
  ) {}

  open(initialDimensions?: vscode.TerminalDimensions): void {
    if (initialDimensions) {
      this.dimensions = { cols: initialDimensions.columns, rows: initialDimensions.rows };
    }
    this.connect();
  }

  close(): void {
    this.closed = true;
    this.ws?.close();
  }

  handleInput(data: string): void {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: "input", data }));
    }
  }

  setDimensions(dimensions: vscode.TerminalDimensions): void {
    this.dimensions = { cols: dimensions.columns, rows: dimensions.rows };
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: "resize", cols: dimensions.columns, rows: dimensions.rows }));
    }
  }

  private connect(): void {
    this.ws = new WebSocket(this.url);

    this.ws.on("open", () => {
      this.reconnectAttempts = 0;
      // Send initial dimensions
      this.ws!.send(JSON.stringify({ type: "resize", cols: this.dimensions.cols, rows: this.dimensions.rows }));
    });

    this.ws.on("message", (data: WebSocket.Data) => {
      try {
        const msg = JSON.parse(data.toString());
        switch (msg.type) {
          case "output":
            this.writeEmitter.fire(msg.data);
            break;
          case "exit":
            this.writeEmitter.fire(`\r\n[Process exited with code ${msg.code ?? 0}]\r\n`);
            this.closeEmitter.fire(msg.code ?? 0);
            break;
          case "error":
            this.writeEmitter.fire(`\r\n\x1b[31m[Error: ${msg.message}]\x1b[0m\r\n`);
            break;
        }
      } catch {
        // Non-JSON data — write raw
        this.writeEmitter.fire(data.toString());
      }
    });

    this.ws.on("error", (err) => {
      if (!this.closed) {
        this.writeEmitter.fire(`\r\n\x1b[31m[Connection error: ${err.message}]\x1b[0m\r\n`);
      }
    });

    this.ws.on("close", () => {
      if (this.closed) {
        this.closeEmitter.fire(0);
        return;
      }
      this.attemptReconnect();
    });
  }

  private async attemptReconnect(): Promise<void> {
    if (this.reconnectAttempts >= MAX_RECONNECT_ATTEMPTS) {
      this.writeEmitter.fire(`\r\n\x1b[33m[Disconnected — max reconnect attempts reached]\x1b[0m\r\n`);
      this.closeEmitter.fire(1);
      return;
    }

    this.reconnectAttempts++;
    this.writeEmitter.fire(`\r\n\x1b[33m[Reconnecting (${this.reconnectAttempts}/${MAX_RECONNECT_ATTEMPTS})...]\x1b[0m\r\n`);

    await new Promise(r => setTimeout(r, RECONNECT_DELAY_MS));
    if (this.closed) return;

    try {
      // Get a fresh ticket for reconnection
      const ticket = await this.api.getTerminalTicket(this.workspaceId);
      const apiUrl = vscode.workspace.getConfiguration("llmsafespace").get<string>("apiUrl") || "";
      const wsUrl = apiUrl.replace(/^http/, "ws").replace(/\/$/, "");
      this.url = `${wsUrl}/api/v1/workspaces/${this.workspaceId}/terminal?ticket=${ticket}`;
      this.connect();
    } catch {
      this.writeEmitter.fire(`\r\n\x1b[31m[Reconnect failed — could not get ticket]\x1b[0m\r\n`);
      this.closeEmitter.fire(1);
    }
  }
}
