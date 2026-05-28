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
      className="h-8 w-20 rounded-md border border-border bg-background px-2 text-sm tabular-nums focus:outline-none focus:ring-2 focus:ring-ring disabled:opacity-50 disabled:cursor-not-allowed"
    />
  );
}
