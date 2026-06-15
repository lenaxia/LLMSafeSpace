import { useRegisterSW } from "virtual:pwa-register/react";

export function usePWA() {
  const {
    needRefresh: [needRefresh, setNeedRefresh],
    updateServiceWorker,
  } = useRegisterSW();

  const update = async () => {
    // Tell the waiting service worker to skip waiting and take control.
    // vite-plugin-pwa's updateServiceWorker(true) posts SKIP_WAITING to the
    // waiting SW; the page reload is triggered by the workbox-window
    // "controlling" event listener.
    //
    // As a safety net we also schedule window.location.reload() after a short
    // delay: if the "controlling" event never fires (e.g. the SW was already
    // active, or the browser skipped the event), the page still reloads.
    // The SW-driven reload and this fallback are both safe to call — a
    // double-reload is harmless, and the fallback is only reached in edge cases.
    let reloaded = false;
    const fallback = setTimeout(() => {
      if (!reloaded) {
        reloaded = true;
        window.location.reload();
      }
    }, 1000);

    try {
      await updateServiceWorker(true);
    } finally {
      clearTimeout(fallback);
      if (!reloaded) {
        reloaded = true;
        window.location.reload();
      }
    }
  };

  const dismiss = () => setNeedRefresh(false);

  return { needRefresh, update, dismiss };
}
