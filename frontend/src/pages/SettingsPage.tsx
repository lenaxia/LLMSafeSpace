import { useState } from "react";
import { cn } from "../lib/utils";
import { AppearanceTab } from "../components/settings/AppearanceTab";
import { ApiKeysTab } from "../components/settings/ApiKeysTab";
import { ComingSoonTab } from "../components/settings/ComingSoonTab";

const tabs = [
  { id: "api-keys", label: "API Keys" },
  { id: "appearance", label: "Appearance" },
  { id: "profile", label: "Profile" },
  { id: "mcp", label: "MCP Servers" },
  { id: "presets", label: "Presets" },
] as const;

type TabId = (typeof tabs)[number]["id"];

export function SettingsPage() {
  const [activeTab, setActiveTab] = useState<TabId>("api-keys");

  return (
    <div className="flex h-full">
      <nav className="w-48 border-r border-border p-4">
        <h2 className="mb-4 text-sm font-semibold">Settings</h2>
        <ul className="flex flex-col gap-1">
          {tabs.map((tab) => (
            <li key={tab.id}>
              <button
                onClick={() => setActiveTab(tab.id)}
                className={cn(
                  "w-full rounded-md px-3 py-1.5 text-left text-sm transition-colors",
                  activeTab === tab.id ? "bg-accent text-accent-foreground" : "hover:bg-accent/50",
                )}
              >
                {tab.label}
              </button>
            </li>
          ))}
        </ul>
      </nav>
      <div className="flex-1 overflow-y-auto p-6">
        {activeTab === "api-keys" && <ApiKeysTab />}
        {activeTab === "appearance" && <AppearanceTab />}
        {(activeTab === "profile" || activeTab === "mcp" || activeTab === "presets") && (
          <ComingSoonTab name={tabs.find((t) => t.id === activeTab)!.label} />
        )}
      </div>
    </div>
  );
}
