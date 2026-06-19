import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { useAuth } from "../providers/AuthProvider";
import { authApi } from "../api/auth";
import { ssoApi, ssoRedirectURL, type SSODomain } from "../api/sso";
import { AuthCard } from "../components/auth/AuthCard";
import { LoginForm } from "../components/auth/LoginForm";
import { Button } from "../components/ui/Button";

export function LoginPage() {
  const { login } = useAuth();
  const [registrationEnabled, setRegistrationEnabled] = useState(false);
  const [instanceName, setInstanceName] = useState("Safe Space");
  const [motd, setMotd] = useState("");
  const [email, setEmail] = useState("");
  const [domains, setDomains] = useState<SSODomain[]>([]);
  const [ssoStatus, setSsoStatus] = useState<string | null>(null);

  useEffect(() => {
    authApi.getConfig().then((c) => {
      setRegistrationEnabled(c.registrationEnabled);
      if (c.instanceName) setInstanceName(c.instanceName);
      if (c.motd) setMotd(c.motd);
      if (c.oidcEnabled) {
        ssoApi.domains().then((r) => setDomains(r.domains)).catch(() => {});
      }
    }).catch(() => {});
  }, []);

  // Surface the SSO outcome from the callback redirect (?sso=...) so the user
  // sees a clear error if the IdP flow failed.
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const sso = params.get("sso");
    if (!sso) return;
    setSsoStatus(sso);
    params.delete("sso");
    const clean = params.toString();
    window.history.replaceState({}, "", clean ? `?${clean}` : window.location.pathname);
  }, []);

  const matchedDomain = domains.find((d) => email.toLowerCase().endsWith(d.domain.toLowerCase()));

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
      {ssoStatus && ssoStatus !== "success" && (
        <p className="mb-3 text-sm text-red-500">
          {ssoStatus === "provisioning_disabled"
            ? "Your account is not provisioned. Contact your administrator."
            : ssoStatus === "suspended"
              ? "Your account is suspended."
              : ssoStatus === "state_invalid"
                ? "Single sign-on session expired or was invalid. Please try again."
                : "Single sign-in failed. Please try again."}
        </p>
      )}
      <LoginForm onSubmit={(u, p, r) => login(u, p, r)} onEmailChange={setEmail} />
      {matchedDomain && (
        <div className="mt-4 border-t border-border pt-4">
          <p className="mb-2 text-center text-xs text-muted-foreground">or</p>
          <Button
            variant="outline"
            className="w-full"
            onClick={() => {
              window.location.href = ssoRedirectURL(matchedDomain.orgSlug);
            }}
          >
            Sign in with {matchedDomain.orgName}
          </Button>
        </div>
      )}
    </AuthCard>
  );
}
