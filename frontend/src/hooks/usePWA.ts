import { useRegisterSW } from "virtual:pwa-register/react";

export function usePWA() {
  const {
    needRefresh: [needRefresh, setNeedRefresh],
    updateServiceWorker,
  } = useRegisterSW();

  const update = () => updateServiceWorker(true);
  const dismiss = () => setNeedRefresh(false);

  return { needRefresh, update, dismiss };
}
