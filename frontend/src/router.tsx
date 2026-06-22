import { Navigate, Outlet, createBrowserRouter } from "react-router-dom";
import { useAuth } from "./providers/AuthProvider";
import { LoginPage } from "./pages/LoginPage";
import { RegisterPage } from "./pages/RegisterPage";
import { ChatPage } from "./pages/ChatPage";
import { SettingsPage } from "./pages/SettingsPage";
import { NotFoundPage } from "./pages/NotFoundPage";
import { AppShell } from "./components/layout/AppShell";
import { Spinner } from "./components/ui/Spinner";
import { OrgAdminLayout } from "./components/org-admin/OrgAdminLayout";
import { OrgOverviewTab } from "./components/org-admin/OrgOverviewTab";
import { OrgMembersTab } from "./components/org-admin/OrgMembersTab";
import { OrgCredentialsTab } from "./components/org-admin/OrgCredentialsTab";
import { OrgWorkspacesTab } from "./components/org-admin/OrgWorkspacesTab";
import { OrgAuditTab } from "./components/org-admin/OrgAuditTab";
import { OrgBillingTab } from "./components/org-admin/OrgBillingTab";
import { OrgSSOTab } from "./components/org-admin/OrgSSOTab";
import { PlatformAdminLayout } from "./components/platform-admin/PlatformAdminLayout";
import { AdminSettingsPage } from "./pages/AdminSettingsPage";
import { AdminProviderCredentialsTab } from "./components/settings/AdminProviderCredentialsTab";
import { OrgSettingsTab } from "./components/settings/OrgSettingsTab";
import { PlatformUsersTab } from "./components/settings/PlatformUsersTab";
import { PlatformAuditTab } from "./components/settings/PlatformAuditTab";
import { RelayTab } from "./components/settings/RelayTab";

function RequireAuth() {
  const { user, loading } = useAuth();
  if (loading) return <div className="flex h-screen items-center justify-center"><Spinner size="lg" /></div>;
  if (!user) return <Navigate to="/login" replace />;
  return <Outlet />;
}

function GuestOnly() {
  const { user, loading } = useAuth();
  if (loading) return <div className="flex h-screen items-center justify-center"><Spinner size="lg" /></div>;
  if (user) return <Navigate to="/chat" replace />;
  return <Outlet />;
}

export const router = createBrowserRouter([
  {
    element: <GuestOnly />,
    children: [
      { path: "/login", element: <LoginPage /> },
      { path: "/register", element: <RegisterPage /> },
    ],
  },
  {
    element: <RequireAuth />,
    children: [
      {
        element: <AppShell />,
        children: [
          { path: "/chat", element: <ChatPage /> },
          { path: "/chat/:workspaceId", element: <ChatPage /> },
          { path: "/chat/:workspaceId/:sessionId", element: <ChatPage /> },
          { path: "/settings", element: <SettingsPage /> },
        ],
      },
      {
        path: "/orgs/:id",
        element: <OrgAdminLayout />,
        children: [
          { index: true, element: <Navigate to="overview" replace /> },
          { path: "overview", element: <OrgOverviewTab /> },
          { path: "members", element: <OrgMembersTab /> },
          { path: "credentials", element: <OrgCredentialsTab /> },
          { path: "workspaces", element: <OrgWorkspacesTab /> },
          { path: "audit", element: <OrgAuditTab /> },
          { path: "billing", element: <OrgBillingTab /> },
          { path: "sso", element: <OrgSSOTab /> },
        ],
      },
      {
        path: "/admin",
        element: <PlatformAdminLayout />,
        children: [
          { index: true, element: <Navigate to="users" replace /> },
          { path: "users", element: <PlatformUsersTab /> },
          { path: "organisations", element: <OrgSettingsTab /> },
          { path: "credentials", element: <AdminProviderCredentialsTab /> },
          { path: "relay", element: <RelayTab /> },
          { path: "settings", element: <AdminSettingsPage /> },
          { path: "audit", element: <PlatformAuditTab /> },
        ],
      },
    ],
  },
  { path: "/", element: <Navigate to="/chat" replace /> },
  { path: "*", element: <NotFoundPage /> },
]);
