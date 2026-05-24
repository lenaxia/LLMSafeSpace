import { Link } from "react-router-dom";
import { useAuth } from "../providers/AuthProvider";
import { AuthCard } from "../components/auth/AuthCard";
import { RegisterForm } from "../components/auth/RegisterForm";

export function RegisterPage() {
  const { register } = useAuth();

  return (
    <AuthCard
      title="Create account"
      description="Get started with Safe Space"
      footer={
        <Link to="/login" className="text-primary underline-offset-4 hover:underline">
          Already have an account? Sign in
        </Link>
      }
    >
      <RegisterForm onSubmit={register} />
    </AuthCard>
  );
}
