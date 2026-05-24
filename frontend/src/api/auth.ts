import { api } from "./client";
import type {
  AuthConfig,
  AuthResponse,
  LoginRequest,
  RegisterRequest,
  User,
} from "./types";

export const authApi = {
  getConfig: () => api.get<AuthConfig>("/auth/config"),
  login: (req: LoginRequest) => api.post<AuthResponse>("/auth/login", req),
  register: (req: RegisterRequest) => api.post<AuthResponse>("/auth/register", req),
  logout: () => api.post<void>("/auth/logout"),
  me: () => api.get<User>("/auth/me"),
};
