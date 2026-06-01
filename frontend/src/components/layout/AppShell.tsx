import { useCallback, useEffect, useRef, useState } from "react";
import { Outlet, useLocation } from "react-router-dom";
import { Menu, X } from "lucide-react";
import { Sidebar } from "./Sidebar";
import { useIsMobile } from "../../hooks/useMediaQuery";

const EDGE_ZONE = 30;
const SWIPE_THRESHOLD = 60;

export function AppShell() {
  const mainRef = useRef<HTMLElement>(null);
  const location = useLocation();
  const isMobile = useIsMobile();
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const touchStartX = useRef(0);
  const touchStartY = useRef(0);
  const isEdgeSwipe = useRef(false);
  const isInitialMount = useRef(true);

  useEffect(() => {
    if (isInitialMount.current) {
      isInitialMount.current = false;
      const hasSession = /^\/chat\/[^\/]+\/[^\/]+$/.test(location.pathname);
      if (isMobile && !hasSession) {
        setSidebarOpen(true);
      }
    } else {
      setSidebarOpen(false);
    }
    mainRef.current?.focus();
  }, [location.pathname]);

  const handleTouchStart = useCallback((e: React.TouchEvent) => {
    const touch = e.touches[0];
    if (!touch) return;
    touchStartX.current = touch.clientX;
    touchStartY.current = touch.clientY;
    isEdgeSwipe.current = touch.clientX < EDGE_ZONE;
  }, []);

  const handleTouchMove = useCallback(
    (e: React.TouchEvent) => {
      if (!isMobile) return;
      const touch = e.touches[0];
      if (!touch) return;
      const dx = touch.clientX - touchStartX.current;
      const dy = Math.abs(touch.clientY - touchStartY.current);

      // If primarily vertical, let it scroll normally
      if (dy > Math.abs(dx)) return;

      // Prevent browser back/forward navigation on horizontal swipes
      e.preventDefault();
    },
    [isMobile],
  );

  const handleTouchEnd = useCallback(
    (e: React.TouchEvent) => {
      if (!isMobile) return;
      const touch = e.changedTouches[0];
      if (!touch) return;
      const dx = touch.clientX - touchStartX.current;
      const dy = Math.abs(touch.clientY - touchStartY.current);
      if (dy > Math.abs(dx)) return;
      if (touchStartX.current < EDGE_ZONE && dx > SWIPE_THRESHOLD) {
        setSidebarOpen(true);
      } else if (sidebarOpen && dx < -SWIPE_THRESHOLD) {
        setSidebarOpen(false);
      }
      isEdgeSwipe.current = false;
    },
    [isMobile, sidebarOpen],
  );

  return (
    <div
      className="flex h-screen overflow-hidden overscroll-none"
      style={{ touchAction: "pan-y" }}
      onTouchStart={handleTouchStart}
      onTouchMove={handleTouchMove}
      onTouchEnd={handleTouchEnd}
    >
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:absolute focus:z-50 focus:bg-background focus:p-2 focus:text-foreground"
      >
        Skip to content
      </a>

      {isMobile && sidebarOpen && (
        <div
          className="fixed inset-0 z-30 bg-black/50"
          onClick={() => setSidebarOpen(false)}
          aria-hidden="true"
        />
      )}

      <div
        className={
          isMobile
            ? `fixed inset-y-0 left-0 z-40 w-64 transform transition-transform duration-200 ${sidebarOpen ? "translate-x-0" : "-translate-x-full"}`
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
