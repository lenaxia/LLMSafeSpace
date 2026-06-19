import { api } from "./client";

export interface SettingDef {
  key: string;
  tier: number;
  type: "bool" | "int" | "string" | "enum" | "strings";
  default: unknown;
  min?: number;
  max?: number;
  pattern?: string;
  enum?: string[];
  category: string;
  label: string;
  description: string;
  readOnly?: boolean;
}

export interface SettingsResponse {
  settings: Record<string, unknown>;
  schemaVersion: number;
}

export interface SchemaResponse {
  settings: SettingDef[];
  schemaVersion: number;
}

export const settingsApi = {
  // Admin (instance) settings
  getAdminSettings: () => api.get<SettingsResponse>("/admin/settings"),
  getAdminSchema: () => api.get<SchemaResponse>("/admin/settings/schema"),
  setAdminSetting: (key: string, value: unknown) =>
    api.put<{ key: string; value: unknown }>(`/admin/settings/${key}`, { value }),

  // User settings
  getUserSettings: () => api.get<SettingsResponse>("/users/me/settings"),
  getUserSchema: () => api.get<SchemaResponse>("/users/me/settings/schema"),
  setUserSetting: (key: string, value: unknown) =>
    api.put<{ key: string; value: unknown }>(`/users/me/settings/${key}`, { value }),
};
