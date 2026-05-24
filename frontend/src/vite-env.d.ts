/// <reference types="vite/client" />
/// <reference types="vite-plugin-pwa/client" />
declare module "*.css" {}
declare module "*.txt?raw" {
  const content: string;
  export default content;
}
declare module "virtual:pwa-register/react" {
  export function useRegisterSW(options?: { immediate?: boolean }): {
    needRefresh: [boolean, (v: boolean) => void];
    offlineReady: [boolean, (v: boolean) => void];
    updateServiceWorker: (reloadPage?: boolean) => Promise<void>;
  };
}
