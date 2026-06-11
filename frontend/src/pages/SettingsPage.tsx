import { useState } from "react";
import { cn } from "../lib/utils";
import { UserSettingsTab } from "../components/settings/UserSettingsTab";
import { SecretsTab } from "../components/settings/SecretsTab";
import { ApiKeysTab } from "../components/settings/ApiKeysTab";
import { AdminSettingsPage } from "./AdminSettingsPage";
import { AdminProviderCredentialsTab } from "../components/settings/AdminProviderCredentialsTab";
import { UserProviderCredentialsTab } from "../components/settings/UserProviderCredentialsTab";
import { useAuth } from "../providers/AuthProvider";

const allTabs = [
  { id: "preferences", label: "Preferences", adminOnly: false },
  { id: "provider-keys", label: "Provider Keys", adminOnly: false },
  { id: "secrets", label: "Secrets", adminOnly: false },
  { id: "api-keys", label: "API Keys", adminOnly: false },
  { id: "platform-credentials", label: "Platform Credentials", adminOnly: true },
  { id: "admin", label: "Admin", adminOnly: true },
] as const;

type TabId = (typeof allTabs)[number]["id"];

export function SettingsPage() {
  const { user } = useAuth();
  const isAdmin = user?.role === "admin";
  const tabs = allTabs.filter((t) => !t.adminOnly || isAdmin);
  const [activeTab, setActiveTab] = useState<TabId>("preferences");

  return (
    <div className="flex h-full flex-col md:flex-row">
      {/* Mobile: horizontal tab bar. Desktop: vertical sidebar */}
      <nav className="border-b border-border p-2 md:border-b-0 md:border-r md:w-52 md:p-4 md:shrink-0">
        <h2 className="hidden md:block mb-4 text-sm font-semibold">Settings</h2>
        <ul className="flex gap-1 overflow-x-auto touch-pan-x md:flex-col">
          {tabs.map((tab) => (
            <li key={tab.id}>
              <button
                onClick={() => setActiveTab(tab.id)}
                className={cn(
                  "whitespace-nowrap rounded-md px-3 py-1.5 text-left text-sm transition-colors w-full",
                  activeTab === tab.id ? "bg-accent text-accent-foreground" : "hover:bg-accent/50",
                )}
              >
                {tab.label}
              </button>
            </li>
          ))}
        </ul>
      </nav>
      <div className="flex-1 min-w-0 overflow-y-auto p-4 md:p-6">
        {activeTab === "preferences" && <UserSettingsTab />}
        {activeTab === "provider-keys" && <UserProviderCredentialsTab />}
        {activeTab === "secrets" && <SecretsTab />}
        {activeTab === "api-keys" && <ApiKeysTab />}
        {activeTab === "platform-credentials" && isAdmin && <AdminProviderCredentialsTab />}
        {activeTab === "admin" && isAdmin && <AdminSettingsPage />}
      </div>
    </div>
  );
}
