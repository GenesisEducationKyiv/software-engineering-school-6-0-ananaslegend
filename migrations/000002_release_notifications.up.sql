CREATE TABLE release_notifications (
    id              BIGSERIAL PRIMARY KEY,
    subscription_id BIGINT NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    repository_id   BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    release_tag     TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at         TIMESTAMPTZ
);

-- Partial index: only pending rows (sent_at IS NULL), ordered by created_at for FIFO processing
CREATE INDEX idx_release_notifications_pending
    ON release_notifications (created_at)
    WHERE sent_at IS NULL;
