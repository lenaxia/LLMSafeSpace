import { useEffect, useRef, useState } from "react";
import { Outlet, useLocation, useMatches } from "react-router-dom";
import { Menu, X } from "lucide-react";
import { Sidebar } from "./Sidebar";
import { useIsMobile } from "../../hooks/useMediaQuery";
import { useUserEventStream } from "../../hooks/useUserEventStream";
import { useSwipeableSidebar } from "../../hooks/useSwipeableSidebar";

const SIDEBAR_WIDTH_PX = 256;

export function AppShell() {
  const mainRef = useRef<HTMLElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const sidebarWrapperRef = useRef<HTMLDivElement>(null);
  const overlayRef = useRef<HTMLDivElement>(null);
  const location = useLocation();
  const isMobile = useIsMobile();
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const matches = useMatches();
  const isInitialMount = useRef(true);

  useUserEventStream();

  useSwipeableSidebar({
    containerRef,
    sidebarRef: sidebarWrapperRef,
    overlayRef,
    isOpen: sidebarOpen,
    setIsOpen: setSidebarOpen,
    enabled: isMobile,
    sidebarWidth: SIDEBAR_WIDTH_PX,
  });

  useEffect(() => {
    if (isInitialMount.current) {
      isInitialMount.current = false;
      const hasSession = matches.some(m => m.params?.sessionId);
      if (isMobile && !hasSession) {
        setSidebarOpen(true);
      }
    } else {
      setSidebarOpen(false);
    }
    mainRef.current?.focus();
  }, [location.pathname]);

  return (
    <div
      ref={containerRef}
      className="flex h-screen overflow-hidden overscroll-none"
      style={{ touchAction: "pan-y" }}
    >
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:absolute focus:z-50 focus:bg-background focus:p-2 focus:text-foreground"
      >
        Skip to content
      </a>

      {isMobile && (
        <div
          ref={overlayRef}
          className={`fixed inset-0 z-30 bg-black/50 transition-opacity duration-200 ${
            sidebarOpen ? "opacity-100 pointer-events-auto" : "opacity-0 pointer-events-none"
          }`}
          onClick={() => setSidebarOpen(false)}
          aria-hidden="true"
        />
      )}

      <div
        ref={sidebarWrapperRef}
        className={
          isMobile
            ? `fixed inset-y-0 left-0 z-40 w-64 transform transition-transform duration-200 ${
                sidebarOpen ? "translate-x-0" : "-translate-x-full"
              }`
            : "relative"
        }
      >
        <Sidebar onNavigate={() => setSidebarOpen(false)} />
      </div>

      <div className="flex flex-1 flex-col overflow-hidden">
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
