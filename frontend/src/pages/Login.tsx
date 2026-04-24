// PRD-43: custom-branded login page. Dark bg, HAL iris centered above
// the form, matching the main app's design tokens. No Cognito Hosted UI.

import { useState, FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { useAuth } from "../components/AuthProvider";
import MatrixRain from "../components/MatrixRain";

export default function Login() {
  const navigate = useNavigate();
  const { login } = useAuth();

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await login(email, password);
      navigate("/", { replace: true });
    } catch (err) {
      const msg = err instanceof Error ? err.message : "Login failed";
      if (msg === "invalid_credentials") {
        setError("Invalid credentials.");
      } else if (msg === "user_not_confirmed") {
        setError("Account not confirmed. Contact an administrator.");
      } else if (msg === "password_reset_required") {
        setError("Password reset required. Contact an administrator.");
      } else {
        setError("Login service unavailable. Try again in a moment.");
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="relative min-h-screen flex items-start justify-center pt-24 text-ink-0 px-4 overflow-hidden">
      <MatrixRain backgroundColor="#000" frameIntervalMs={90} />
      <div className="relative z-10 w-full max-w-sm">
        <div className="flex flex-col items-center mb-8">
          {/* HAL iris — scaled up with a glow and a solid black disc
              behind the rings so the matrix rain doesn't pass through
              the iris. */}
          <div className="w-28 h-28 relative flex items-center justify-center mb-6">
            <div className="absolute inset-0 rounded-full bg-black" />
            <div className="absolute inset-0 rounded-full border-2 border-ink-2 shadow-[0_0_40px_rgba(57,255,136,0.35)]" />
            <div className="absolute inset-[14px] rounded-full border border-signal/60" />
            <div className="w-12 h-12 rounded-full bg-signal animate-hal-iris shadow-[0_0_30px_rgba(57,255,136,0.8)]" />
          </div>
          <span className="font-mono text-[14px] tracking-widemech text-ink-0 drop-shadow-[0_0_8px_rgba(0,0,0,0.8)]">
            ACCELBENCH
          </span>
        </div>

        <form
          onSubmit={onSubmit}
          className="bg-surface-1/90 backdrop-blur border border-line rounded p-6 flex flex-col gap-4"
        >
          <label className="flex flex-col gap-1">
            <span className="eyebrow">EMAIL</span>
            <input
              type="email"
              autoComplete="email"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              className="input"
              disabled={submitting}
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="eyebrow">PASSWORD</span>
            <input
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="input"
              disabled={submitting}
            />
          </label>

          {error && (
            <div className="font-mono text-[12px] text-danger border border-danger/40 bg-danger/5 px-3 py-2">
              {error}
            </div>
          )}

          <button
            type="submit"
            disabled={submitting}
            className="btn btn-primary w-full justify-center"
          >
            {submitting ? "SIGNING IN…" : "SIGN IN"}
          </button>
        </form>
      </div>
    </div>
  );
}
