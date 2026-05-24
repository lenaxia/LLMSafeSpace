import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { useAuth } from "../providers/AuthProvider";
import { authApi } from "../api/auth";
import { AuthCard } from "../components/auth/AuthCard";
import { LoginForm } from "../components/auth/LoginForm";

export function LoginPage() {
  const { login } = useAuth();
  const [registrationEnabled, setRegistrationEnabled] = useState(false);

  useEffect(() => {
    authApi.getConfig().then((c) => setRegistrationEnabled(c.registrationEnabled)).catch(() => {});
  }, []);

  return (
    <AuthCard
      title="Welcome back"
      description="Sign in to your Safe Space account"
      footer={
        registrationEnabled ? (
          <Link to="/register" className="text-primary underline-offset-4 hover:underline">
            Create an account
          </Link>
        ) : undefined
      }
    >
      <LoginForm onSubmit={login} />
    </AuthCard>
  );
}
