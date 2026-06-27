// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

import { type ReactNode } from "react";

// ModelRow is the per-model row state for the model configuration table. It is
// shared by the admin, user, and org provider-credential forms.
//
// contextLimit and outputLimit MUST be set together for a model to take
// effect: opencode's published config JSON Schema requires both
// `limit.context` and `limit.output` whenever the `limit` block is present.
// When the user sets only one, the API stores both values but the
// agent-config.json formatter omits the entire `limit` block (and opencode
// falls back to built-in defaults) — see pkg/agent/opencode/format.go.
// The UI surfaces a hint in this case so the user understands why nothing
// changed.
export interface ModelRow {
  id: string;
  enabled: boolean;
  contextLimit: string; // stored as string for controlled input
  outputLimit: string; // stored as string for controlled input
}

interface ModelConfigTableProps {
  rows: ModelRow[];
  onChange: (rows: ModelRow[]) => void;
  // emptyMessage overrides the default empty-state hint.
  emptyMessage?: string;
  // footer is rendered below the table (e.g. the org tab's "Add model
  // manually" button). Omitted by the admin/user tabs.
  footer?: ReactNode;
}

// ModelConfigTable renders the editable checkbox + context-window + output-limit
// table used by the provider-credential forms to pick an allowlist and per-model
// token limits. It is a pure presentational component with no API or store
// dependencies — all state is passed in via props.
export function ModelConfigTable({
  rows,
  onChange,
  emptyMessage = "No models found. Check your API key and base URL.",
  footer,
}: ModelConfigTableProps) {
  const update = (idx: number, patch: Partial<ModelRow>) =>
    onChange(rows.map((r, i) => (i === idx ? { ...r, ...patch } : r)));

  if (rows.length === 0) {
    return (
      <p className="text-xs text-muted-foreground italic">{emptyMessage}</p>
    );
  }

  return (
    <div className="space-y-2">
      <div className="max-h-48 overflow-y-auto rounded-md border border-border">
        <table className="w-full text-xs">
          <thead className="sticky top-0 bg-muted/80 backdrop-blur-sm">
            <tr>
              <th className="px-2 py-1.5 text-left font-medium text-muted-foreground w-8">
                On
              </th>
              <th className="px-2 py-1.5 text-left font-medium text-muted-foreground">
                Model ID
              </th>
              <th className="px-2 py-1.5 text-left font-medium text-muted-foreground w-32">
                Context window <span className="text-muted-foreground/50">(tokens)</span>
              </th>
              <th className="px-2 py-1.5 text-left font-medium text-muted-foreground w-32">
                Max output <span className="text-muted-foreground/50">(tokens)</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row, idx) => {
              const ctx = row.contextLimit.trim();
              const out = row.outputLimit.trim();
              // Partial limit warning: opencode requires both fields or neither.
              const partial = row.enabled && (ctx === "") !== (out === "");
              return (
                <tr
                  key={row.id}
                  className="border-t border-border/50 hover:bg-muted/30"
                >
                  <td className="px-2 py-1">
                    <input
                      type="checkbox"
                      checked={row.enabled}
                      onChange={(e) =>
                        update(idx, { enabled: e.target.checked })
                      }
                      className="h-3.5 w-3.5"
                    />
                  </td>
                  <td className="px-2 py-1 font-mono">
                    {row.id}
                    {partial && (
                      <span
                        className="ml-2 text-amber-600 dark:text-amber-400"
                        title="opencode requires both context and output limits to be set together — partial values are ignored and the model falls back to opencode defaults"
                      >
                        ⚠ partial
                      </span>
                    )}
                  </td>
                  <td className="px-2 py-1">
                    <input
                      type="number"
                      min={0}
                      value={row.contextLimit}
                      onChange={(e) =>
                        update(idx, { contextLimit: e.target.value })
                      }
                      placeholder="e.g. 200000"
                      disabled={!row.enabled}
                      className="h-6 w-full rounded border border-border bg-background px-1.5 text-xs focus:outline-none focus:ring-1 focus:ring-ring disabled:opacity-40"
                    />
                  </td>
                  <td className="px-2 py-1">
                    <input
                      type="number"
                      min={0}
                      value={row.outputLimit}
                      onChange={(e) =>
                        update(idx, { outputLimit: e.target.value })
                      }
                      placeholder="e.g. 8192"
                      disabled={!row.enabled}
                      className="h-6 w-full rounded border border-border bg-background px-1.5 text-xs focus:outline-none focus:ring-1 focus:ring-ring disabled:opacity-40"
                    />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {footer}
    </div>
  );
}
