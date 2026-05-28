import { useState, useEffect } from "react";

interface NumberInputProps {
  value: number;
  onChange: (value: number) => void;
  min?: number;
  max?: number;
  disabled?: boolean;
  id?: string;
}

export function NumberInput({ value, onChange, min, max, disabled, id }: NumberInputProps) {
  const [local, setLocal] = useState(String(value));
  const [invalid, setInvalid] = useState(false);

  // Sync local state when prop changes externally
  useEffect(() => { setLocal(String(value)); }, [value]);

  const commit = () => {
    const n = parseInt(local, 10);
    if (isNaN(n)) {
      setLocal(String(value)); // revert
      setInvalid(false);
      return;
    }
    if ((min !== undefined && n < min) || (max !== undefined && n > max)) {
      setInvalid(true);
      return;
    }
    setInvalid(false);
    if (n !== value) onChange(n);
  };

  return (
    <input
      id={id}
      type="number"
      value={local}
      min={min}
      max={max}
      disabled={disabled}
      onChange={(e) => { setLocal(e.target.value); setInvalid(false); }}
      onBlur={commit}
      onKeyDown={(e) => { if (e.key === "Enter") e.currentTarget.blur(); }}
      className={`h-8 w-20 rounded-md border bg-background px-2 text-sm tabular-nums focus:outline-none focus:ring-2 focus:ring-ring disabled:opacity-50 disabled:cursor-not-allowed ${
        invalid ? "border-destructive" : "border-border"
      }`}
    />
  );
}
