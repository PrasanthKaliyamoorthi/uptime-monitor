-- Admin users
CREATE TABLE IF NOT EXISTS admins (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Monitors with Authentication & Custom Request Payload
CREATE TABLE IF NOT EXISTS monitors (
    id TEXT PRIMARY KEY,
    status_page_id TEXT REFERENCES status_pages(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    protocol TEXT NOT NULL DEFAULT 'https',   -- 'http', 'https', 'tcp', 'smtp'
    target TEXT NOT NULL,                    -- URL or host:port
    method TEXT NOT NULL DEFAULT 'GET',      -- 'GET', 'HEAD', 'POST', 'PUT'
    
    -- Authentication & Headers
    auth_type TEXT NOT NULL DEFAULT 'none',  -- 'none', 'bearer', 'basic', 'header'
    auth_token TEXT,                         -- Bearer token or API key
    auth_user TEXT,                          -- Basic auth username
    auth_pass TEXT,                          -- Basic auth password
    headers_json TEXT,                       -- JSON string of key-value headers
    body TEXT,                               -- Request payload for POST/PUT (e.g. JSON)
    skip_tls_verify BOOLEAN NOT NULL DEFAULT 0,
    
    -- Timing & Thresholds
    interval_sec INTEGER NOT NULL DEFAULT 60,
    timeout_sec INTEGER NOT NULL DEFAULT 10,
    expected_code INTEGER NOT NULL DEFAULT 200,
    keyword TEXT,
    retry_count INTEGER NOT NULL DEFAULT 2,  -- Retries before marking down
    max_days INTEGER NOT NULL DEFAULT 60,     -- Display & retention days (max 60)
    is_active BOOLEAN NOT NULL DEFAULT 1,
    
    -- Status & State
    current_status TEXT NOT NULL DEFAULT 'pending', -- 'up', 'degraded', 'down', 'pending'
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    last_checked_at DATETIME,
    last_latency_ms INTEGER NOT NULL DEFAULT 0,
    
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Check heartbeats & Deep Failure Diagnostics
CREATE TABLE IF NOT EXISTS check_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    status_code INTEGER,
    latency_ms INTEGER NOT NULL,
    is_success BOOLEAN NOT NULL,
    error_type TEXT,
    error_message TEXT,
    failure_details TEXT,                  -- JSON string: DiagnosticDetail
    checked_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_check_logs_monitor_time ON check_logs(monitor_id, checked_at DESC);

-- System & Audit Logging
CREATE TABLE IF NOT EXISTS system_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    level TEXT NOT NULL,                   -- 'INFO', 'WARN', 'ERROR'
    source TEXT NOT NULL,                  -- 'checker', 'scheduler', 'auth', 'admin', 'notifier'
    monitor_id TEXT REFERENCES monitors(id) ON DELETE SET NULL,
    message TEXT NOT NULL,
    context_json TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_system_logs_time ON system_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_system_logs_level ON system_logs(level);

-- Multi-domain Status Pages with 6 Preset Themes & Optional Protection
CREATE TABLE IF NOT EXISTS status_pages (
    id TEXT PRIMARY KEY,
    slug TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    domain TEXT UNIQUE,                      -- e.g. 'status.git.com', 'status.terminal.in'
    description TEXT,
    logo_url TEXT,                           -- Logo image URL or data URI
    theme TEXT NOT NULL DEFAULT 'openstatus-slate', 
    accent_color TEXT NOT NULL DEFAULT '#10b981',
    password TEXT,                           -- Optional password protection (bcrypt)
    is_default BOOLEAN NOT NULL DEFAULT 0,
    history_days INTEGER NOT NULL DEFAULT 60,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

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
    status TEXT NOT NULL,                  -- 'investigating', 'identified', 'monitoring', 'resolved'
    severity TEXT NOT NULL,                -- 'minor', 'major', 'critical', 'maintenance'
    is_maintenance BOOLEAN NOT NULL DEFAULT 0,
    scheduled_start DATETIME,
    scheduled_end DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at DATETIME
);

CREATE TABLE IF NOT EXISTS incident_updates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    incident_id TEXT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    status TEXT NOT NULL,
    message TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Fully Customizable Generic Webhooks & Trigger Rules
CREATE TABLE IF NOT EXISTS alert_webhooks (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    method TEXT NOT NULL DEFAULT 'POST',
    headers_json TEXT,
    payload_template TEXT,
    
    trigger_on_down BOOLEAN NOT NULL DEFAULT 1,
    trigger_on_degraded BOOLEAN NOT NULL DEFAULT 1,
    trigger_on_up BOOLEAN NOT NULL DEFAULT 1,
    trigger_on_tls_warning BOOLEAN NOT NULL DEFAULT 1,
    trigger_on_incident BOOLEAN NOT NULL DEFAULT 1,
    
    is_active BOOLEAN NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Admin Console IP Blacklist (Brute-Force & Excessive Failed Login Protection)
CREATE TABLE IF NOT EXISTS admin_blacklist (
    ip TEXT PRIMARY KEY,
    reason TEXT NOT NULL,
    failed_attempts INTEGER NOT NULL DEFAULT 1,
    blacklisted BOOLEAN NOT NULL DEFAULT 0,
    blacklisted_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_admin_blacklist_status ON admin_blacklist(blacklisted);

-- Admin Persistent Sessions (Survives binary restarts)
CREATE TABLE IF NOT EXISTS admin_sessions (
    token TEXT PRIMARY KEY,
    admin_id TEXT NOT NULL,
    username TEXT NOT NULL,
    expires_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_admin_sessions_expires ON admin_sessions(expires_at);

-- System Settings (Key-Value configuration including admin allowed domain)
CREATE TABLE IF NOT EXISTS system_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
