-- +goose Up

-- events: append-only audit log + source of truth for webhook fanout
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    occurred_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    type TEXT NOT NULL,
    actor_type TEXT NOT NULL,
    actor_id TEXT,
    operator_id TEXT,
    account_id TEXT,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    payload TEXT
);
CREATE INDEX idx_events_occurred_at ON events(occurred_at);
CREATE INDEX idx_events_type ON events(type);
CREATE INDEX idx_events_operator_id ON events(operator_id);
CREATE INDEX idx_events_resource ON events(resource_type, resource_id);

-- webhook_subscriptions: operator-scoped HTTP receivers
CREATE TABLE webhook_subscriptions (
    id TEXT PRIMARY KEY,
    operator_id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    url TEXT NOT NULL,
    encrypted_secret TEXT NOT NULL,
    event_types TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    disabled_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (operator_id) REFERENCES operators(id) ON DELETE CASCADE,
    UNIQUE(operator_id, name)
);
CREATE INDEX idx_webhook_subscriptions_operator_id ON webhook_subscriptions(operator_id);
CREATE INDEX idx_webhook_subscriptions_enabled ON webhook_subscriptions(enabled);

-- webhook_deliveries: durable retry queue
CREATE TABLE webhook_deliveries (
    id TEXT PRIMARY KEY,
    subscription_id TEXT NOT NULL,
    event_id TEXT NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    next_attempt_at TIMESTAMP NOT NULL,
    last_error TEXT NOT NULL DEFAULT '',
    last_response_code INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP,
    FOREIGN KEY (subscription_id) REFERENCES webhook_subscriptions(id) ON DELETE CASCADE,
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
);
CREATE INDEX idx_webhook_deliveries_status_next ON webhook_deliveries(status, next_attempt_at);
CREATE INDEX idx_webhook_deliveries_subscription_id ON webhook_deliveries(subscription_id);
CREATE INDEX idx_webhook_deliveries_event_id ON webhook_deliveries(event_id);

-- +goose Down

DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS webhook_subscriptions;
DROP TABLE IF EXISTS events;
