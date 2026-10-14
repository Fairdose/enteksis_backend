-- V0.1 uygulama oturumları: kanonik domain şemasını değiştirmeyen erişim katmanı.
ALTER TABLE accounts ADD CONSTRAINT ux_accounts_id_ws UNIQUE (id, workspace_id);

CREATE TABLE auth_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    account_id UUID NOT NULL,
    access_hash CHAR(64) NOT NULL UNIQUE,
    refresh_hash CHAR(64) NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    refresh_expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (account_id, workspace_id) REFERENCES accounts(id, workspace_id)
);

CREATE INDEX ix_auth_sessions_account ON auth_sessions(account_id) WHERE revoked_at IS NULL;
CREATE INDEX ix_auth_sessions_refresh ON auth_sessions(refresh_hash) WHERE revoked_at IS NULL;
