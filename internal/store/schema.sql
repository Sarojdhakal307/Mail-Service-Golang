CREATE TABLE IF NOT EXISTS api_keys (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT        NOT NULL,
    address      TEXT        NOT NULL DEFAULT '',
    key_prefix   TEXT        NOT NULL,
    key_hash     BYTEA       NOT NULL UNIQUE,
    allowed_ips  TEXT[]      NOT NULL DEFAULT '{*}',
    is_super     BOOLEAN     NOT NULL DEFAULT FALSE,
    limit_hour   INTEGER     NOT NULL DEFAULT 0 CHECK (limit_hour >= 0),
    limit_day    INTEGER     NOT NULL DEFAULT 0 CHECK (limit_day >= 0),
    limit_week   INTEGER     NOT NULL DEFAULT 0 CHECK (limit_week >= 0),
    limit_month  INTEGER     NOT NULL DEFAULT 0 CHECK (limit_month >= 0),
    active       BOOLEAN     NOT NULL DEFAULT TRUE,
    created_ip   TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    last_used_ip TEXT
);

-- One row per authenticated request. Accepted rows are what the rate limits count.
CREATE TABLE IF NOT EXISTS request_logs (
    id         BIGSERIAL PRIMARY KEY,
    api_key_id BIGINT      NOT NULL REFERENCES api_keys (id) ON DELETE CASCADE,
    ip         TEXT        NOT NULL,
    method     TEXT        NOT NULL,
    path       TEXT        NOT NULL,
    user_agent TEXT        NOT NULL DEFAULT '',
    mail_count INTEGER     NOT NULL DEFAULT 0,
    status     TEXT        NOT NULL,
    message    TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS request_logs_accepted_idx
    ON request_logs (api_key_id, created_at) WHERE status = 'accepted';
CREATE INDEX IF NOT EXISTS request_logs_created_idx
    ON request_logs (created_at DESC);

-- Encrypted copy of the key so the super user can reveal it later. NULL for keys created before this existed.
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_encrypted BYTEA;

-- API key requests submitted from the public site, reviewed by the super user.
CREATE TABLE IF NOT EXISTS key_requests (
    id              BIGSERIAL PRIMARY KEY,
    name            TEXT        NOT NULL,
    email           TEXT        NOT NULL,
    organization    TEXT        NOT NULL DEFAULT '',
    address         TEXT        NOT NULL DEFAULT '',
    use_case        TEXT        NOT NULL,
    expected_volume TEXT        NOT NULL DEFAULT '',
    caller_ips      TEXT        NOT NULL DEFAULT '',
    ip              TEXT        NOT NULL,
    user_agent      TEXT        NOT NULL DEFAULT '',
    status          TEXT        NOT NULL DEFAULT 'pending',
    api_key_id      BIGINT      REFERENCES api_keys (id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS key_requests_created_idx ON key_requests (created_at DESC);

ALTER TABLE key_requests ADD COLUMN IF NOT EXISTS phone TEXT NOT NULL DEFAULT '';
ALTER TABLE key_requests ADD COLUMN IF NOT EXISTS message TEXT NOT NULL DEFAULT '';
