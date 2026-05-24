import { Monitor, Moon, Sun } from "lucide-react";
import { useTheme } from "../../providers/ThemeProvider";
import { cn } from "../../lib/utils";

const options = [
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
  { value: "system", label: "System", icon: Monitor },
] as const;

export function AppearanceTab() {
  const { theme, setTheme } = useTheme();

  return (
    <div>
      <h3 className="text-lg font-semibold">Appearance</h3>
      <p className="mb-4 text-sm text-muted-foreground">Customize how Safe Space looks</p>
      <div className="flex gap-3">
        {options.map(({ value, label, icon: Icon }) => (
          <button
            key={value}
            onClick={() => setTheme(value)}
            className={cn(
              "flex flex-col items-center gap-2 rounded-lg border p-4 transition-colors",
              theme === value ? "border-primary bg-accent" : "border-border hover:border-primary/50",
            )}
          >
            <Icon className="h-5 w-5" />
            <span className="text-xs font-medium">{label}</span>
          </button>
        ))}
      </div>
    </div>
  );
}
