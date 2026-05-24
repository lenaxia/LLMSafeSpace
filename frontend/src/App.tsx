import { RouterProvider } from "react-router-dom";
import { AuthProvider } from "./providers/AuthProvider";
import { ThemeProvider } from "./providers/ThemeProvider";
import { QueryClientProvider } from "./providers/QueryClientProvider";
import { UpdateAvailableToast } from "./components/layout/UpdateAvailableToast";
import { usePWA } from "./hooks/usePWA";
import { router } from "./router";

function PWAUpdater() {
  const { needRefresh, update, dismiss } = usePWA();
  if (!needRefresh) return null;
  return <UpdateAvailableToast onUpdate={update} onDismiss={dismiss} />;
}

export function App() {
  return (
    <ThemeProvider>
      <QueryClientProvider>
        <AuthProvider>
          <RouterProvider router={router} />
          <PWAUpdater />
        </AuthProvider>
      </QueryClientProvider>
    </ThemeProvider>
  );
}
