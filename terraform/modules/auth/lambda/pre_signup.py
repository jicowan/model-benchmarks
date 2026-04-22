"""Cognito pre-signup trigger.

Rejects signups unless the email address belongs to one of the allowed
domains (provided via the ALLOWED_EMAIL_DOMAINS env var, comma-separated).
Auto-confirms the user and auto-verifies their email so trusted-domain
signups can log in immediately without a confirmation email.
"""

import os


def _allowed_domains() -> list[str]:
    raw = os.environ.get("ALLOWED_EMAIL_DOMAINS", "")
    return [d.strip().lower() for d in raw.split(",") if d.strip()]


def handler(event, context):
    email = (event.get("request", {}).get("userAttributes", {}) or {}).get("email", "")
    email = email.strip().lower()

    if "@" not in email:
        raise Exception("A valid email address is required to sign up.")

    domain = email.rsplit("@", 1)[1]
    allowed = _allowed_domains()

    if allowed and domain not in allowed:
        raise Exception(
            f"Sign-up is restricted to: {', '.join(allowed)}. "
            f"The address '{email}' is not permitted."
        )

    # Trusted domain — skip the confirmation-code email and the email-verify step.
    event["response"]["autoConfirmUser"] = True
    event["response"]["autoVerifyEmail"] = True
    return event
