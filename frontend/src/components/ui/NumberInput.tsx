interface NumberInputProps {
  value: number;
  onChange: (value: number) => void;
  min?: number;
  max?: number;
  disabled?: boolean;
  id?: string;
}

export function NumberInput({ value, onChange, min, max, disabled, id }: NumberInputProps) {
  return (
    <input
      id={id}
      type="number"
      value={value}
      min={min}
      max={max}
      disabled={disabled}
      onChange={(e) => {
        const n = parseInt(e.target.value, 10);
        if (!isNaN(n)) onChange(n);
      }}
      className="w-24 rounded-md border border-border bg-background px-2 py-1 text-sm disabled:opacity-50"
    />
  );
}
