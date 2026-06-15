import { RouterProvider } from "react-router-dom";
import { AuthProvider } from "./providers/AuthProvider";
import { ThemeProvider } from "./providers/ThemeProvider";
import { ToastProvider } from "./providers/ToastProvider";
import { QueryClientProvider } from "./providers/QueryClientProvider";
import { ErrorBoundary } from "./components/layout/ErrorBoundary";
import { UpdateAvailableToast } from "./components/layout/UpdateAvailableToast";
import { TooltipProvider } from "./components/ui";
import { usePWA } from "./hooks/usePWA";
import { router } from "./router";

function PWAUpdater() {
  const { needRefresh, update, dismiss } = usePWA();
  if (!needRefresh) return null;
  return <UpdateAvailableToast onUpdate={update} onDismiss={dismiss} />;
}

export function App() {
  return (
    <ErrorBoundary>
      <ThemeProvider>
        <ToastProvider>
          <QueryClientProvider>
            <AuthProvider>
              {/* TooltipProvider must wrap the entire app so Radix tooltips
                  rendered inside portals can find the provider context. */}
              <TooltipProvider delayDuration={300}>
                <RouterProvider router={router} />
                <PWAUpdater />
              </TooltipProvider>
            </AuthProvider>
          </QueryClientProvider>
        </ToastProvider>
      </ThemeProvider>
    </ErrorBoundary>
  );
}
