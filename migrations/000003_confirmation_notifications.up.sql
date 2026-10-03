CREATE TABLE confirmation_notifications (
    id              BIGSERIAL PRIMARY KEY,
    subscription_id BIGINT NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at         TIMESTAMPTZ
);

-- Partial index: only pending rows (sent_at IS NULL), ordered by created_at for FIFO processing
CREATE INDEX idx_confirmation_notifications_pending
    ON confirmation_notifications (created_at)
    WHERE sent_at IS NULL;
