-- Admin users
CREATE TABLE IF NOT EXISTS admins (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Multi-domain Status Pages with Preset Themes & Optional Protection
CREATE TABLE IF NOT EXISTS status_pages (
    id TEXT PRIMARY KEY,
    slug TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    domain TEXT UNIQUE,
    description TEXT,
    logo_url TEXT,
    theme TEXT NOT NULL DEFAULT 'openstatus-slate',
    accent_color TEXT NOT NULL DEFAULT '#10b981',
    password TEXT,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    history_days INTEGER NOT NULL DEFAULT 60,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Monitors with Authentication & Custom Request Payload
CREATE TABLE IF NOT EXISTS monitors (
    id TEXT PRIMARY KEY,
    status_page_id TEXT REFERENCES status_pages(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    protocol TEXT NOT NULL DEFAULT 'https',
    target TEXT NOT NULL,
    method TEXT NOT NULL DEFAULT 'GET',
    
    auth_type TEXT NOT NULL DEFAULT 'none',
    auth_token TEXT,
    auth_user TEXT,
    auth_pass TEXT,
    headers_json TEXT,
    body TEXT,
    skip_tls_verify BOOLEAN NOT NULL DEFAULT FALSE,
    
    interval_sec INTEGER NOT NULL DEFAULT 60,
    timeout_sec INTEGER NOT NULL DEFAULT 10,
    expected_code INTEGER NOT NULL DEFAULT 200,
    keyword TEXT,
    retry_count INTEGER NOT NULL DEFAULT 2,
    max_days INTEGER NOT NULL DEFAULT 60,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    
    current_status TEXT NOT NULL DEFAULT 'pending',
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    last_checked_at TIMESTAMPTZ,
    last_latency_ms INTEGER NOT NULL DEFAULT 0,
    
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Check heartbeats & Deep Failure Diagnostics
CREATE TABLE IF NOT EXISTS check_logs (
    id BIGSERIAL PRIMARY KEY,
    monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    status_code INTEGER,
    latency_ms INTEGER NOT NULL,
    is_success BOOLEAN NOT NULL,
    error_type TEXT,
    error_message TEXT,
    failure_details TEXT,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_check_logs_monitor_time ON check_logs(monitor_id, checked_at DESC);

-- System & Audit Logging
CREATE TABLE IF NOT EXISTS system_logs (
    id BIGSERIAL PRIMARY KEY,
    level TEXT NOT NULL,
    source TEXT NOT NULL,
    monitor_id TEXT REFERENCES monitors(id) ON DELETE SET NULL,
    message TEXT NOT NULL,
    context_json TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_system_logs_time ON system_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_system_logs_level ON system_logs(level);

-- Mapping Status Pages to Monitors
CREATE TABLE IF NOT EXISTS status_page_monitors (
    status_page_id TEXT NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    display_order INTEGER NOT NULL DEFAULT 0,
    group_name TEXT NOT NULL DEFAULT 'Services',
    PRIMARY KEY(status_page_id, monitor_id)
);

-- Incident Reports & Scheduled Maintenance
CREATE TABLE IF NOT EXISTS incidents (
    id TEXT PRIMARY KEY,
    status_page_id TEXT REFERENCES status_pages(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    status TEXT NOT NULL,
    severity TEXT NOT NULL,
    is_maintenance BOOLEAN NOT NULL DEFAULT FALSE,
    scheduled_start TIMESTAMPTZ,
    scheduled_end TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS incident_updates (
    id BIGSERIAL PRIMARY KEY,
    incident_id TEXT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    status TEXT NOT NULL,
    message TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Alert Webhooks & Trigger Rules
CREATE TABLE IF NOT EXISTS alert_webhooks (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    method TEXT NOT NULL DEFAULT 'POST',
    headers_json TEXT,
    payload_template TEXT,
    
    trigger_on_down BOOLEAN NOT NULL DEFAULT TRUE,
    trigger_on_degraded BOOLEAN NOT NULL DEFAULT TRUE,
    trigger_on_up BOOLEAN NOT NULL DEFAULT TRUE,
    trigger_on_tls_warning BOOLEAN NOT NULL DEFAULT TRUE,
    trigger_on_incident BOOLEAN NOT NULL DEFAULT TRUE,
    
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Admin Console IP Blacklist
CREATE TABLE IF NOT EXISTS admin_blacklist (
    ip TEXT PRIMARY KEY,
    reason TEXT NOT NULL,
    failed_attempts INTEGER NOT NULL DEFAULT 1,
    blacklisted BOOLEAN NOT NULL DEFAULT FALSE,
    blacklisted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_admin_blacklist_status ON admin_blacklist(blacklisted);

-- Admin Persistent Sessions
CREATE TABLE IF NOT EXISTS admin_sessions (
    token TEXT PRIMARY KEY,
    admin_id TEXT NOT NULL,
    username TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_admin_sessions_expires ON admin_sessions(expires_at);

-- System Settings
CREATE TABLE IF NOT EXISTS system_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Programmatic API Access Tokens
CREATE TABLE IF NOT EXISTS api_tokens (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    token_prefix TEXT NOT NULL,
    token_hash TEXT UNIQUE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(token_hash);
