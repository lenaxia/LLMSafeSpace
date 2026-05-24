import { RouterProvider } from "react-router-dom";
import { AuthProvider } from "./providers/AuthProvider";
import { ThemeProvider } from "./providers/ThemeProvider";
import { QueryClientProvider } from "./providers/QueryClientProvider";
import { router } from "./router";

export function App() {
  return (
    <ThemeProvider>
      <QueryClientProvider>
        <AuthProvider>
          <RouterProvider router={router} />
        </AuthProvider>
      </QueryClientProvider>
    </ThemeProvider>
  );
}
