-- PRD-32: Audit log for Configuration page changes.
--
-- Every write endpoint under /api/v1/config/* appends a row. Only the
-- action + a short summary are stored — token values, full request
-- payloads, and any secret material are NEVER recorded.
--
-- Until auth lands, `actor` is always NULL. The log is a post-hoc forensic
-- tool, not a preventive control.

CREATE TABLE IF NOT EXISTS config_audit_log (
    id       BIGSERIAL PRIMARY KEY,
    at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    action   TEXT NOT NULL,
    actor    TEXT,
    summary  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_config_audit_log_at ON config_audit_log (at DESC);
