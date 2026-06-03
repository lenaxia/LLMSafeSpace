import { Monitor, Moon, Sun } from "lucide-react";
import { useTheme } from "../../providers/ThemeProvider";
import { useUserSettings } from "../../hooks/useUserSettings";
import { cn } from "../../lib/utils";

const options = [
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
  { value: "system", label: "System", icon: Monitor },
] as const;

export function AppearanceTab() {
  const { theme, setTheme } = useTheme();
  const { settings, setSetting } = useUserSettings();
  const autoExpandChildren = (settings.autoExpandChildren as boolean) ?? true;

  return (
    <div className="flex flex-col gap-6">
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

      <div>
        <h3 className="text-lg font-semibold">Navigation</h3>
        <p className="mb-4 text-sm text-muted-foreground">Customize sidebar behaviour</p>
        <label className="flex items-center justify-between gap-4 cursor-pointer">
          <div>
            <p className="text-sm font-medium">Auto-expand child sessions</p>
            <p className="text-xs text-muted-foreground">
              Automatically expand a session&apos;s children when you activate it, and collapse them when you navigate away.
            </p>
          </div>
          <button
            role="switch"
            aria-checked={autoExpandChildren}
            onClick={() => setSetting("autoExpandChildren", !autoExpandChildren)}
            className={cn(
              "relative inline-flex h-5 w-9 flex-shrink-0 rounded-full border-2 border-transparent transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              autoExpandChildren ? "bg-primary" : "bg-input",
            )}
          >
            <span
              className={cn(
                "pointer-events-none inline-block h-4 w-4 rounded-full bg-background shadow-lg transition-transform",
                autoExpandChildren ? "translate-x-4" : "translate-x-0",
              )}
            />
          </button>
        </label>
      </div>
    </div>
  );
}
