import { useState } from "react";
import type { SettingDef } from "../../api/settings";
import { Toggle } from "../ui/Toggle";
import { NumberInput } from "../ui/NumberInput";
import { Select } from "../ui/Select";

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
          <div className="space-y-4">
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
  const [error, setError] = useState<string | null>(null);

  const handleChange = async (newValue: unknown) => {
    setSaving(true);
    setError(null);
    try {
      await onSave(def.key, newValue);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex items-center justify-between gap-4 rounded-md border border-border p-3">
      <div className="flex-1 min-w-0">
        <label className="text-sm font-medium" htmlFor={def.key}>
          {def.label}
        </label>
        <p className="text-xs text-muted-foreground">{def.description}</p>
        {error && <p className="text-xs text-destructive mt-1">{error}</p>}
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
        <input
          id={def.key}
          type="text"
          value={value as string}
          onChange={(e) => onChange(e.target.value)}
          onBlur={(e) => onChange(e.target.value)}
          disabled={disabled}
          className="w-48 rounded-md border border-border bg-background px-2 py-1 text-sm disabled:opacity-50"
        />
      );
    case "strings":
      return (
        <input
          id={def.key}
          type="text"
          value={(value as string[])?.join(", ") ?? ""}
          onChange={(e) => onChange(e.target.value.split(",").map((s) => s.trim()).filter(Boolean))}
          disabled={disabled}
          placeholder="comma-separated"
          className="w-48 rounded-md border border-border bg-background px-2 py-1 text-sm disabled:opacity-50"
        />
      );
    default:
      return <span className="text-xs text-muted-foreground">unsupported</span>;
  }
}
