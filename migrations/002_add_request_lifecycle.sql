ALTER TABLE service_requests
    ADD COLUMN IF NOT EXISTS status VARCHAR(16) NOT NULL DEFAULT 'new'
        CHECK (status IN ('new', 'read', 'replied')),
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS replied_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS service_requests_status_created_at_idx
    ON service_requests (status, created_at DESC);
