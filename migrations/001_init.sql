CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TYPE account_status AS ENUM ('active', 'suspended');
CREATE TYPE user_role AS ENUM ('platform_admin', 'account_admin', 'account_operator', 'account_viewer');
CREATE TYPE payment_provider AS ENUM ('alipay', 'wechat');
CREATE TYPE payment_scene AS ENUM ('page', 'wap', 'native', 'jsapi');
CREATE TYPE bill_status AS ENUM ('pending', 'paid', 'closed', 'failed', 'refunding', 'refunded');
CREATE TYPE refund_status AS ENUM ('pending', 'succeeded', 'failed');

CREATE TABLE accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(120) NOT NULL,
    merchant_no VARCHAR(64) NOT NULL UNIQUE,
    status account_status NOT NULL DEFAULT 'active',
    api_secret_ciphertext TEXT NOT NULL,
    callback_secret_ciphertext TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID REFERENCES accounts(id) ON DELETE CASCADE,
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name VARCHAR(120) NOT NULL,
    role user_role NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT platform_admin_has_no_account CHECK ((role = 'platform_admin' AND account_id IS NULL) OR (role <> 'platform_admin' AND account_id IS NOT NULL))
);

CREATE TABLE payment_channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    provider payment_provider NOT NULL,
    display_name VARCHAR(120) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    config_ciphertext TEXT NOT NULL DEFAULT '',
    webhook_token VARCHAR(80) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(account_id, provider)
);

CREATE TABLE bills (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES payment_channels(id),
    platform_order_no VARCHAR(64) NOT NULL UNIQUE,
    merchant_order_no VARCHAR(128) NOT NULL,
    subject VARCHAR(256) NOT NULL,
    description VARCHAR(512) NOT NULL DEFAULT '',
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency CHAR(3) NOT NULL DEFAULT 'CNY',
    provider payment_provider NOT NULL,
    scene payment_scene NOT NULL,
    status bill_status NOT NULL DEFAULT 'pending',
    provider_transaction_id VARCHAR(128),
    provider_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    notify_url TEXT NOT NULL,
    return_url TEXT,
    metadata TEXT,
    expires_at TIMESTAMPTZ,
    paid_at TIMESTAMPTZ,
    closed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(account_id, merchant_order_no)
);

CREATE TABLE refunds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    bill_id UUID NOT NULL REFERENCES bills(id),
    refund_order_no VARCHAR(128) NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    reason VARCHAR(256) NOT NULL DEFAULT '',
    status refund_status NOT NULL DEFAULT 'pending',
    provider_refund_id VARCHAR(128),
    provider_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(account_id, refund_order_no)
);

CREATE TABLE webhook_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES payment_channels(id),
    provider payment_provider NOT NULL,
    event_key VARCHAR(200) NOT NULL,
    verified BOOLEAN NOT NULL DEFAULT FALSE,
    payload JSONB NOT NULL,
    processed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(channel_id, event_key)
);

CREATE TABLE audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
    actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action VARCHAR(100) NOT NULL,
    target_type VARCHAR(64) NOT NULL,
    target_id VARCHAR(128) NOT NULL,
    request_id VARCHAR(64) NOT NULL,
    detail JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX bills_account_created_idx ON bills(account_id, created_at DESC);
CREATE INDEX bills_account_status_idx ON bills(account_id, status);
CREATE INDEX refunds_bill_idx ON refunds(bill_id, created_at DESC);
CREATE INDEX webhook_events_channel_idx ON webhook_events(channel_id, created_at DESC);
CREATE INDEX audit_logs_account_created_idx ON audit_logs(account_id, created_at DESC);
