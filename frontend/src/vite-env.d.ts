/// <reference types="vite/client" />
/// <reference types="vite-plugin-pwa/client" />
declare module "*.css" {}
declare module "virtual:pwa-register/react" {
  export function useRegisterSW(options?: { immediate?: boolean }): {
    needRefresh: [boolean, (v: boolean) => void];
    offlineReady: [boolean, (v: boolean) => void];
    updateServiceWorker: (reloadPage?: boolean) => Promise<void>;
  };
}
