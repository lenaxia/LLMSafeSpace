import { useEffect, useRef } from "react";
import { Outlet, useLocation } from "react-router-dom";
import { Sidebar } from "./Sidebar";

export function AppShell() {
  const mainRef = useRef<HTMLElement>(null);
  const location = useLocation();

  // Move focus to main content on route change for screen readers
  useEffect(() => {
    mainRef.current?.focus();
  }, [location.pathname]);

  return (
    <div className="flex h-screen overflow-hidden">
      {/* Skip to content link — visible on focus for keyboard users */}
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:absolute focus:z-50 focus:bg-background focus:p-2 focus:text-foreground"
      >
        Skip to content
      </a>
      <Sidebar />
      <main
        id="main-content"
        ref={mainRef}
        className="flex-1 overflow-hidden"
        tabIndex={-1}
        aria-label="Main content"
      >
        <Outlet />
      </main>
    </div>
  );
}
