import { useState, useEffect } from "react";
import type { SettingDef } from "../../api/settings";
import { Toggle } from "../ui/Toggle";
import { NumberInput } from "../ui/NumberInput";
import { Select } from "../ui/Select";
import { TagInput } from "../ui/TagInput";

interface SettingsFormProps {
  schema: SettingDef[];
  values: Record<string, unknown>;
  onSave: (key: string, value: unknown) => Promise<void>;
  disabled?: boolean;
}

export function SettingsForm({ schema, values, onSave, disabled }: SettingsFormProps) {
  const categories = [...new Set(schema.map((s) => s.category))];

  return (
    <div className="space-y-8">
      {categories.map((category) => (
        <section key={category}>
          <h3 className="mb-3 text-sm font-semibold text-muted-foreground uppercase tracking-wide">
            {category}
          </h3>
          <div className="divide-y divide-border rounded-md border border-border">
            {schema
              .filter((s) => s.category === category)
              .map((def) => (
                <SettingRow
                  key={def.key}
                  def={def}
                  value={values[def.key] ?? def.default}
                  onSave={onSave}
                  disabled={disabled}
                />
              ))}
          </div>
        </section>
      ))}
    </div>
  );
}

interface SettingRowProps {
  def: SettingDef;
  value: unknown;
  onSave: (key: string, value: unknown) => Promise<void>;
  disabled?: boolean;
}

function SettingRow({ def, value, onSave, disabled }: SettingRowProps) {
  const [saving, setSaving] = useState(false);

  const handleChange = async (newValue: unknown) => {
    setSaving(true);
    try {
      await onSave(def.key, newValue);
    } catch {
      // Error handled by parent (toast)
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex items-center justify-between gap-4 px-4 py-3">
      <div className="flex-1 min-w-0">
        <label className="text-sm font-medium" htmlFor={def.key}>
          {def.label}
        </label>
        <p className="text-xs text-muted-foreground mt-0.5">{def.description}</p>
      </div>
      <div className="shrink-0">
        <SettingControl def={def} value={value} onChange={handleChange} disabled={disabled || saving} />
      </div>
    </div>
  );
}

interface SettingControlProps {
  def: SettingDef;
  value: unknown;
  onChange: (value: unknown) => void;
  disabled?: boolean;
}

function SettingControl({ def, value, onChange, disabled }: SettingControlProps) {
  switch (def.type) {
    case "bool":
      return <Toggle checked={value as boolean} onCheckedChange={onChange} disabled={disabled} id={def.key} />;
    case "int":
      return (
        <NumberInput
          value={value as number}
          onChange={onChange}
          min={def.min}
          max={def.max}
          disabled={disabled}
          id={def.key}
        />
      );
    case "enum":
      return (
        <Select
          value={value as string}
          onValueChange={onChange}
          options={def.enum ?? []}
          disabled={disabled}
          id={def.key}
        />
      );
    case "string":
      return (
        <StringInput
          id={def.key}
          value={value as string}
          onCommit={onChange}
          disabled={disabled}
        />
      );
    case "strings":
      return (
        <TagInput
          id={def.key}
          value={Array.isArray(value) ? (value as string[]) : []}
          onChange={onChange}
          disabled={disabled}
        />
      );
    default:
      return <span className="text-xs text-muted-foreground">unsupported</span>;
  }
}

/** Text input that only commits on blur or Enter — avoids saving on every keystroke. */
function StringInput({ id, value, onCommit, disabled, placeholder }: {
  id: string;
  value: string;
  onCommit: (value: unknown) => void;
  disabled?: boolean;
  placeholder?: string;
}) {
  const [local, setLocal] = useState(value);
  // Sync when value prop changes externally (e.g. API response)
  useEffect(() => { setLocal(value); }, [value]);
  return (
    <input
      id={id}
      type="text"
      value={local}
      onChange={(e) => setLocal(e.target.value)}
      onBlur={() => { if (local !== value) onCommit(local); }}
      onKeyDown={(e) => { if (e.key === "Enter") { e.currentTarget.blur(); } }}
      disabled={disabled}
      placeholder={placeholder}
      className="h-8 w-48 rounded-md border border-border bg-background px-2 text-sm focus:outline-none focus:ring-2 focus:ring-ring disabled:opacity-50 disabled:cursor-not-allowed"
    />
  );
}
