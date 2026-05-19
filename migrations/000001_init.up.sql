CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE repositories (
    id              BIGSERIAL PRIMARY KEY,
    owner           TEXT        NOT NULL,
    name            TEXT        NOT NULL,
    last_seen_tag   TEXT,
    last_checked_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT repositories_owner_name_key UNIQUE (owner, name)
);

-- Scanner вибирає репо порціями: ORDER BY last_checked_at NULLS FIRST LIMIT N
CREATE INDEX idx_repositories_last_checked_at
    ON repositories (last_checked_at NULLS FIRST);

CREATE TABLE subscriptions (
    id                       BIGSERIAL PRIMARY KEY,
    email                    CITEXT      NOT NULL,
    repository_id            BIGINT      NOT NULL
        REFERENCES repositories (id) ON DELETE CASCADE,
    confirmed_at             TIMESTAMPTZ,
    confirm_token            TEXT,
    confirm_token_expires_at TIMESTAMPTZ,
    unsubscribe_token        TEXT        NOT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT subscriptions_email_repository_key
        UNIQUE (email, repository_id),
    CONSTRAINT subscriptions_unsubscribe_token_key
        UNIQUE (unsubscribe_token),
    -- confirmed_at IS NULL ↔ confirm_token IS NOT NULL (pending стан)
    -- confirmed_at IS NOT NULL ↔ confirm_token IS NULL (підтверджено)
    CONSTRAINT subscriptions_confirm_state_check
        CHECK ((confirm_token IS NULL) = (confirmed_at IS NOT NULL))
);

-- GET /api/subscriptions?email=
CREATE INDEX idx_subscriptions_email
    ON subscriptions (email);

-- GET /api/confirm/{token}: partial бо після підтвердження confirm_token = NULL
CREATE UNIQUE INDEX idx_subscriptions_confirm_token
    ON subscriptions (confirm_token)
    WHERE confirm_token IS NOT NULL;

-- Notifier: SELECT email WHERE repository_id = $1 AND confirmed_at IS NOT NULL
CREATE INDEX idx_subscriptions_repository_id_confirmed
    ON subscriptions (repository_id)
    WHERE confirmed_at IS NOT NULL;
