import { useEffect, useRef, useState } from "react";
import { Outlet, useLocation } from "react-router-dom";
import { Menu, X } from "lucide-react";
import { Sidebar } from "./Sidebar";
import { useIsMobile } from "../../hooks/useMediaQuery";

export function AppShell() {
  const mainRef = useRef<HTMLElement>(null);
  const location = useLocation();
  const isMobile = useIsMobile();
  const [sidebarOpen, setSidebarOpen] = useState(false);

  // Close sidebar on route change (mobile)
  useEffect(() => {
    setSidebarOpen(false);
    mainRef.current?.focus();
  }, [location.pathname]);

  return (
    <div className="flex h-screen overflow-hidden">
      {/* Skip to content link */}
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:absolute focus:z-50 focus:bg-background focus:p-2 focus:text-foreground"
      >
        Skip to content
      </a>

      {/* Mobile overlay */}
      {isMobile && sidebarOpen && (
        <div
          className="fixed inset-0 z-30 bg-black/50"
          onClick={() => setSidebarOpen(false)}
          aria-hidden="true"
        />
      )}

      {/* Sidebar */}
      <div
        className={
          isMobile
            ? `fixed inset-y-0 left-0 z-40 w-64 transform transition-transform duration-200 ${sidebarOpen ? "translate-x-0" : "-translate-x-full"}`
            : "relative"
        }
      >
        <Sidebar />
      </div>

      {/* Main content */}
      <div className="flex flex-1 flex-col overflow-hidden">
        {/* Mobile top bar */}
        {isMobile && (
          <div className="flex items-center border-b border-border px-3 py-2">
            <button
              onClick={() => setSidebarOpen(!sidebarOpen)}
              className="rounded p-2 hover:bg-accent"
              aria-label={sidebarOpen ? "Close menu" : "Open menu"}
            >
              {sidebarOpen ? <X className="h-5 w-5" /> : <Menu className="h-5 w-5" />}
            </button>
            <span className="ml-2 text-sm font-semibold">Safe Space</span>
          </div>
        )}

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
    </div>
  );
}
