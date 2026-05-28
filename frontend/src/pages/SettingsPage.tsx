import { useState } from "react";
import { cn } from "../lib/utils";
import { UserSettingsTab } from "../components/settings/UserSettingsTab";
import { ApiKeysTab } from "../components/settings/ApiKeysTab";
import { AdminSettingsPage } from "./AdminSettingsPage";
import { AdminCredentialsTab } from "../components/settings/AdminCredentialsTab";
import { useAuth } from "../providers/AuthProvider";

const allTabs = [
  { id: "preferences", label: "Preferences", adminOnly: false },
  { id: "api-keys", label: "API Keys", adminOnly: false },
  { id: "credentials", label: "Credentials", adminOnly: true },
  { id: "admin", label: "Admin", adminOnly: true },
] as const;

type TabId = (typeof allTabs)[number]["id"];

export function SettingsPage() {
  const { user } = useAuth();
  const isAdmin = user?.role === "admin";
  const tabs = allTabs.filter((t) => !t.adminOnly || isAdmin);
  const [activeTab, setActiveTab] = useState<TabId>("preferences");

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
        {activeTab === "preferences" && <UserSettingsTab />}
        {activeTab === "api-keys" && <ApiKeysTab />}
        {activeTab === "credentials" && isAdmin && <AdminCredentialsTab />}
        {activeTab === "admin" && isAdmin && <AdminSettingsPage />}
      </div>
    </div>
  );
}
