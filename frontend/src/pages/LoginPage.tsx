import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { useAuth } from "../providers/AuthProvider";
import { authApi } from "../api/auth";
import { AuthCard } from "../components/auth/AuthCard";
import { LoginForm } from "../components/auth/LoginForm";

export function LoginPage() {
  const { login } = useAuth();
  const [registrationEnabled, setRegistrationEnabled] = useState(false);
  const [instanceName, setInstanceName] = useState("Safe Space");
  const [motd, setMotd] = useState("");

  useEffect(() => {
    authApi.getConfig().then((c) => {
      setRegistrationEnabled(c.registrationEnabled);
      if (c.instanceName) setInstanceName(c.instanceName);
      if (c.motd) setMotd(c.motd);
    }).catch(() => {});
  }, []);

  return (
    <AuthCard
      title={`Welcome to ${instanceName}`}
      description={motd || "Sign in to your account"}
      footer={
        registrationEnabled ? (
          <Link to="/register" className="text-primary underline-offset-4 hover:underline">
            Create an account
          </Link>
        ) : undefined
      }
    >
      <LoginForm onSubmit={(username, password, rememberMe) => login(username, password, rememberMe)} />
    </AuthCard>
  );
}
