CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS service_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL CHECK (char_length(name) BETWEEN 2 AND 100),
    email VARCHAR(254) NOT NULL CHECK (char_length(email) BETWEEN 3 AND 254),
    service_type VARCHAR(64) NOT NULL CHECK (
        service_type IN (
            'web-design',
            'software-development',
            'digital-consulting',
            'support-maintenance'
        )
    ),
    description VARCHAR(2000) NOT NULL CHECK (char_length(description) BETWEEN 10 AND 2000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS service_requests_created_at_idx
    ON service_requests (created_at DESC);

ALTER TABLE service_requests ENABLE ROW LEVEL SECURITY;

REVOKE ALL ON TABLE service_requests FROM PUBLIC;
