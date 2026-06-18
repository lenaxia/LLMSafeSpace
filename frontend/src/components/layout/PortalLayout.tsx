import type { ReactNode } from "react";
import { Link, NavLink, Outlet } from "react-router-dom";

export interface NavItem {
  to: string;
  label: string;
}

export interface PortalLayoutProps {
  title: string;
  backLink: string;
  backLabel?: string;
  badges?: ReactNode;
  meta?: ReactNode;
  navItems: NavItem[];
  context: unknown;
}

export function PortalLayout({
  title,
  backLink,
  backLabel = "Back to Chat",
  badges,
  meta,
  navItems,
  context,
}: PortalLayoutProps) {
  return (
    <div className="flex h-screen flex-col bg-background">
      <header className="flex items-center justify-between border-b border-border px-6 py-3">
        <div className="flex items-center gap-3">
          <Link
            to={backLink}
            className="text-sm text-muted-foreground hover:text-foreground"
          >
            ← {backLabel}
          </Link>
          <span className="text-border">|</span>
          <h1 className="text-lg font-semibold">{title}</h1>
          {badges}
        </div>
        {meta && <div className="text-xs text-muted-foreground">{meta}</div>}
      </header>

      <div className="flex flex-1 overflow-hidden">
        <nav className="w-48 border-r border-border py-2">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                `block px-4 py-2 text-sm ${
                  isActive
                    ? "bg-accent/10 font-medium text-accent"
                    : "text-muted-foreground hover:bg-muted hover:text-foreground"
                }`
              }
            >
              {item.label}
            </NavLink>
          ))}
        </nav>

        <main className="flex-1 overflow-y-auto p-6">
          <Outlet context={context} />
        </main>
      </div>
    </div>
  );
}
