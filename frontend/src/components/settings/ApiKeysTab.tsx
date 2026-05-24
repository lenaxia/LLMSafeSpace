import { Button } from "../ui/Button";

export function ApiKeysTab() {
  return (
    <div>
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-lg font-semibold">API Keys</h3>
          <p className="text-sm text-muted-foreground">Manage your API keys for programmatic access</p>
        </div>
        <Button size="sm">Create key</Button>
      </div>
      <div className="mt-6 rounded-md border border-border p-8 text-center text-sm text-muted-foreground">
        No API keys yet. Create one to get started.
      </div>
    </div>
  );
}
