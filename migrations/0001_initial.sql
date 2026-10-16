CREATE TABLE users (
    id BIGINT PRIMARY KEY CHECK (id > 0),
    display_name TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE expenses (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    amount NUMERIC(12, 2) NOT NULL CHECK (amount >= 0.01 AND amount <= 9999999999.99),
    category TEXT NOT NULL CHECK (category IN (
        'food', 'transport', 'housing', 'entertainment', 'health', 'shopping', 'other'
    )),
    spent_at DATE NOT NULL,
    comment TEXT NULL CHECK (comment IS NULL OR char_length(comment) <= 500),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0)
);

CREATE INDEX expenses_user_spent_idx ON expenses (user_id, spent_at DESC, id DESC);
CREATE INDEX expenses_user_created_idx ON expenses (user_id, created_at DESC, id DESC);
CREATE INDEX expenses_spent_idx ON expenses (spent_at DESC, id DESC);

CREATE TABLE bot_updates (
    update_id BIGINT PRIMARY KEY,
    user_id BIGINT NULL,
    chat_id BIGINT NULL,
    message_id BIGINT NULL,
    message_at TIMESTAMPTZ NULL,
    reference_date DATE NULL,
    text TEXT NULL,
    status TEXT NOT NULL CHECK (status IN (
        'pending', 'processing', 'ready_to_send', 'delivered', 'ignored', 'delivery_failed'
    )),
    processing_attempts INTEGER NOT NULL DEFAULT 0 CHECK (processing_attempts >= 0),
    delivery_attempts INTEGER NOT NULL DEFAULT 0 CHECK (delivery_attempts >= 0),
    next_attempt_at TIMESTAMPTZ NULL,
    result JSONB NULL,
    reply_text TEXT NULL,
    last_error_code TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX bot_updates_work_idx ON bot_updates (status, next_attempt_at, update_id);

CREATE TABLE bot_sessions (
    user_id BIGINT NOT NULL,
    chat_id BIGINT NOT NULL,
    pending_action JSONB NULL,
    expires_at TIMESTAMPTZ NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, chat_id)
);

CREATE TABLE bot_polling_state (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    next_offset BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO bot_polling_state (singleton, next_offset) VALUES (TRUE, 0);
