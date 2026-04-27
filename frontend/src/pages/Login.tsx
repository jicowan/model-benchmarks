// PRD-43: custom-branded login page. Dark bg, HAL iris centered above
// the form, matching the main app's design tokens. No Cognito Hosted UI.

import { useState, FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { useAuth } from "../components/AuthProvider";
import type { LoginChallenge } from "../api";
import MatrixRain from "../components/MatrixRain";

export default function Login() {
  const navigate = useNavigate();
  const { login, respondChallenge } = useAuth();

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // Invited users land here on first sign-in: Cognito answers login with
  // NEW_PASSWORD_REQUIRED and we swap the form to a password-setup step.
  const [challenge, setChallenge] = useState<LoginChallenge | null>(null);
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");

  function translateError(msg: string): string {
    switch (msg) {
      case "invalid_credentials":
        return "Invalid credentials.";
      case "user_not_confirmed":
        return "Account not confirmed. Contact an administrator.";
      case "password_reset_required":
        return "Password reset required. Contact an administrator.";
      case "invalid_password":
        return "Password doesn't meet the policy (min 8 chars, include upper, lower, number, symbol).";
      case "challenge_required":
        return "Additional sign-in step required but not supported. Contact an administrator.";
      default:
        return "Login service unavailable. Try again in a moment.";
    }
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      const result = await login(email, password);
      if (result.kind === "challenge") {
        setChallenge(result.challenge);
      } else {
        navigate("/", { replace: true });
      }
    } catch (err) {
      setError(translateError(err instanceof Error ? err.message : ""));
    } finally {
      setSubmitting(false);
    }
  }

  async function onSubmitNewPassword(e: FormEvent) {
    e.preventDefault();
    setError(null);
    if (newPassword !== confirmPassword) {
      setError("Passwords do not match.");
      return;
    }
    if (!challenge) return;
    setSubmitting(true);
    try {
      await respondChallenge(challenge, newPassword);
      navigate("/", { replace: true });
    } catch (err) {
      setError(translateError(err instanceof Error ? err.message : ""));
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

        {challenge ? (
          <form
            onSubmit={onSubmitNewPassword}
            className="bg-surface-1/90 backdrop-blur border border-line rounded p-6 flex flex-col gap-4"
          >
            <div className="font-mono text-[11px] text-ink-2 tracking-mech">
              FIRST SIGN-IN · SET A NEW PASSWORD FOR {challenge.email.toUpperCase()}
            </div>
            <label className="flex flex-col gap-1">
              <span className="eyebrow">NEW PASSWORD</span>
              <input
                type="password"
                autoComplete="new-password"
                required
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                className="input"
                disabled={submitting}
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="eyebrow">CONFIRM PASSWORD</span>
              <input
                type="password"
                autoComplete="new-password"
                required
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
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
              {submitting ? "SETTING PASSWORD…" : "SET PASSWORD & SIGN IN"}
            </button>
          </form>
        ) : (
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
        )}
      </div>
    </div>
  );
}
