# LLMSafeSpaces for VS Code

Manage your [LLMSafeSpaces](https://github.com/lenaxia/LLMSafeSpaces) AI agent sandboxes directly from VS Code.

## Features

### Workspace Sidebar
Browse and manage all your sandboxes from the activity bar. See real-time status with color-coded icons:
- 🟢 Active — agent running and ready
- 🟡 Suspended — PVC retained, pod stopped
- ⚪ Terminated

### Terminal Access
Open a secure terminal connected to any active workspace. Traffic flows through the API's WebSocket proxy — no SSH keys or port-forwarding needed.

### Chat Participant
Type `@llmsafespaces` in Copilot Chat to route prompts directly to your sandbox agent. The agent has full access to the workspace filesystem and tools.

### Commands
All available from the Command Palette (`Ctrl+Shift+P`):
- **Create Workspace** — pick a runtime, name it, done
- **Suspend / Resume** — save costs by suspending idle workspaces
- **Activate** — one-click resume + session creation
- **Open Terminal** — WebSocket shell into the sandbox
- **Configure** — set API URL and key

## Getting Started

1. Install the extension
2. Open the Command Palette and run `LLMSafeSpaces: Configure`
3. Enter your API URL and API key (`lsp_...`)
4. Your workspaces appear in the sidebar

## Requirements

- VS Code 1.95 or later
- A running LLMSafeSpaces instance
- An API key (create one at your instance's web UI)

## Extension Settings

| Setting | Default | Description |
|---------|---------|-------------|
| `llmsafespaces.apiUrl` | (empty) | Base URL of your LLMSafeSpaces instance |
| `llmsafespaces.refreshInterval` | 30 | Auto-refresh interval in seconds (5–300) |

API keys are stored securely in VS Code's SecretStorage (OS keychain).

## Privacy

This extension communicates only with the LLMSafeSpaces instance you configure. No data is sent to third parties. No telemetry is collected.
