import type { ReactNode } from "react";

type PromptVariant = "permission" | "question";

interface VariantConfig {
  card: string;
  icon: string;
  title: string;
  ariaLabel: string;
}

const VARIANTS: Record<PromptVariant, VariantConfig> = {
  permission: {
    card: "border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-950/30",
    icon: "⚠️",
    title: "Permission required",
    ariaLabel: "Permission required",
  },
  question: {
    card: "border-blue-300 dark:border-blue-700 bg-blue-50 dark:bg-blue-950/30",
    icon: "🤖",
    title: "Agent has a question",
    ariaLabel: "Agent has a question",
  },
};

interface AgentPromptProps {
  variant: PromptVariant;
  title?: string;
  onDismiss?: () => void;
  dismissDisabled?: boolean;
  dismissLabel?: string;
  children: ReactNode;
}

export function AgentPrompt({ variant, title, onDismiss, dismissDisabled, dismissLabel = "Dismiss", children }: AgentPromptProps) {
  const config = VARIANTS[variant];
  return (
    <div className={`border ${config.card} rounded-lg p-4 mb-3`} role="dialog" aria-label={config.ariaLabel}>
      <div className="flex items-center justify-between mb-3">
        <span className="font-semibold text-sm">{config.icon} {title ?? config.title}</span>
        {onDismiss && (
          <button onClick={onDismiss} disabled={dismissDisabled} className="text-muted-foreground hover:text-foreground" aria-label={dismissLabel}>✕</button>
        )}
      </div>
      {children}
    </div>
  );
}
