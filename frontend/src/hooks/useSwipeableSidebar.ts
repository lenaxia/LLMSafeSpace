import { useEffect, useRef, type RefObject } from "react";

const EDGE_ZONE = 30;
const SETTLE_RATIO = 1 / 3;

interface UseSwipeableSidebarOptions {
  containerRef: RefObject<HTMLDivElement | null>;
  sidebarRef: RefObject<HTMLDivElement | null>;
  overlayRef: RefObject<HTMLDivElement | null>;
  isOpen: boolean;
  setIsOpen: (value: boolean | ((prev: boolean) => boolean)) => void;
  enabled: boolean;
  sidebarWidth: number;
}

export function useSwipeableSidebar({
  containerRef,
  sidebarRef,
  overlayRef,
  isOpen,
  setIsOpen,
  enabled,
  sidebarWidth,
}: UseSwipeableSidebarOptions) {
  const touchStartX = useRef(0);
  const touchStartY = useRef(0);
  const isEdgeSwipe = useRef(false);
  const isSwiping = useRef(false);
  const swipeOffset = useRef(0);
  const isOpenRef = useRef(isOpen);
  const pendingCleanup = useRef<(() => void) | null>(null);

  useEffect(() => {
    isOpenRef.current = isOpen;
  }, [isOpen]);

  useEffect(() => {
    if (!enabled) return;

    const el = containerRef.current;
    if (!el) return;

    const onStart = (e: TouchEvent) => {
      if (e.touches.length > 1) return;
      const t = e.touches[0]!;
      touchStartX.current = t.clientX;
      touchStartY.current = t.clientY;
      isEdgeSwipe.current = t.clientX < EDGE_ZONE;
    };

    const onMove = (e: TouchEvent) => {
      if (e.touches.length > 1) return;
      const t = e.touches[0]!;
      const dx = t.clientX - touchStartX.current;
      const dy = Math.abs(t.clientY - touchStartY.current);

      if (dy > Math.abs(dx)) return;

      e.preventDefault();

      const side = sidebarRef.current;
      const over = overlayRef.current;
      const open = isOpenRef.current;

      if (isEdgeSwipe.current && dx > 0 && !open) {
        isSwiping.current = true;
        const offset = Math.min(dx, sidebarWidth);
        swipeOffset.current = offset;
        if (side) {
          side.style.transition = "none";
          side.style.transform = `translateX(${-sidebarWidth + offset}px)`;
        }
        if (over) {
          over.style.transition = "none";
          over.style.opacity = String((offset / sidebarWidth) * 0.5);
          over.style.pointerEvents = "auto";
        }
      } else if (open && dx < 0) {
        isSwiping.current = true;
        const offset = Math.max(dx, -sidebarWidth);
        swipeOffset.current = offset;
        if (side) {
          side.style.transition = "none";
          side.style.transform = `translateX(${offset}px)`;
        }
        if (over) {
          over.style.transition = "none";
          over.style.opacity = String(((sidebarWidth + offset) / sidebarWidth) * 0.5);
        }
      }
    };

    const onEnd = (e: TouchEvent) => {
      if (pendingCleanup.current) {
        pendingCleanup.current();
        pendingCleanup.current = null;
      }

      const side = sidebarRef.current;
      const over = overlayRef.current;

      if (isSwiping.current) {
        const open = isOpenRef.current;
        const settleThreshold = sidebarWidth * SETTLE_RATIO;

        const targetOpen = open
          ? swipeOffset.current > -settleThreshold
          : swipeOffset.current > settleThreshold;

        if (side) {
          side.style.transition = "";
          side.style.transform = "";
        }
        if (over) {
          over.style.transition = "";
          over.style.opacity = "";
          over.style.pointerEvents = "";
        }

        setIsOpen(targetOpen);
        isSwiping.current = false;
        swipeOffset.current = 0;
      } else {
        const t = e.changedTouches[0];
        if (!t) return;
        const dx = t.clientX - touchStartX.current;
        const dy = Math.abs(t.clientY - touchStartY.current);
        if (dy > Math.abs(dx)) return;

        if (isEdgeSwipe.current && dx > EDGE_ZONE) {
          setIsOpen(true);
        } else if (isOpenRef.current && dx < -EDGE_ZONE) {
          setIsOpen(false);
        }
      }

      isEdgeSwipe.current = false;
    };

    el.addEventListener("touchstart", onStart, { passive: true });
    el.addEventListener("touchmove", onMove, { passive: false });
    el.addEventListener("touchend", onEnd, { passive: true });

    const cleanup = () => {
      el.removeEventListener("touchstart", onStart);
      el.removeEventListener("touchmove", onMove);
      el.removeEventListener("touchend", onEnd);
    };

    pendingCleanup.current = cleanup;

    return cleanup;
  }, [enabled, sidebarWidth, sidebarRef, overlayRef, setIsOpen, containerRef]);
}
