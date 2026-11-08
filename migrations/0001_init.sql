PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    last_login_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON users(username);

CREATE TABLE IF NOT EXISTS sessions (
    token_hash TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at DATETIME NOT NULL,
    client_ip TEXT,
    user_agent TEXT,
    created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);

CREATE TABLE IF NOT EXISTS upstreams (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    strategy TEXT NOT NULL DEFAULT 'round_robin',
    enabled INTEGER NOT NULL DEFAULT 1,
    health_check_enabled INTEGER NOT NULL DEFAULT 1,
    health_check_type TEXT NOT NULL DEFAULT 'http',
    health_check_path TEXT NOT NULL DEFAULT '/',
    health_check_interval INTEGER NOT NULL DEFAULT 10,
    health_check_timeout INTEGER NOT NULL DEFAULT 3,
    healthy_threshold INTEGER NOT NULL DEFAULT 2,
    unhealthy_threshold INTEGER NOT NULL DEFAULT 3,
    passive_failure_limit INTEGER NOT NULL DEFAULT 3,
    passive_cooldown INTEGER NOT NULL DEFAULT 30,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_upstreams_name ON upstreams(name);

CREATE TABLE IF NOT EXISTS upstream_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    upstream_id INTEGER NOT NULL REFERENCES upstreams(id) ON DELETE CASCADE,
    address TEXT NOT NULL,
    weight INTEGER NOT NULL DEFAULT 1,
    priority INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1,
    healthy INTEGER NOT NULL DEFAULT 1,
    tls_verify INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_nodes_application ON upstream_nodes(upstream_id);

CREATE TABLE IF NOT EXISTS certificates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    domains TEXT NOT NULL DEFAULT '[]',
    issuer TEXT,
    certificate_type TEXT NOT NULL,
    challenge_type TEXT,
    certificate_path TEXT NOT NULL,
    private_key_path TEXT NOT NULL,
    expires_at DATETIME,
    auto_renew INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'pending',
    acme_account_id INTEGER REFERENCES acme_accounts(id) ON DELETE SET NULL,
    dns_provider_id INTEGER REFERENCES dns_providers(id) ON DELETE SET NULL,
    last_renew_at DATETIME,
    renew_attempts INTEGER NOT NULL DEFAULT 0,
    next_renew_at DATETIME,
    last_error TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_certificates_name ON certificates(name);
CREATE INDEX IF NOT EXISTS idx_certificates_expires ON certificates(expires_at);

CREATE TABLE IF NOT EXISTS acme_accounts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    email TEXT NOT NULL,
    ca_url TEXT NOT NULL,
    account_key_path TEXT NOT NULL,
    registration TEXT,
    status TEXT NOT NULL DEFAULT 'valid',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS dns_providers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    provider_type TEXT NOT NULL,
    credentials_encrypted TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_dns_providers_name ON dns_providers(name);

CREATE TABLE IF NOT EXISTS sites (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    listen_port INTEGER NOT NULL DEFAULT 80,
    domains TEXT NOT NULL DEFAULT '[]',
    protocol TEXT NOT NULL DEFAULT 'http',
    upstream_id INTEGER NOT NULL REFERENCES upstreams(id) ON DELETE RESTRICT,
    certificate_id INTEGER REFERENCES certificates(id) ON DELETE SET NULL,
    redirect_https INTEGER NOT NULL DEFAULT 0,
    proxy_timeout INTEGER NOT NULL DEFAULT 30,
    max_body_size INTEGER NOT NULL DEFAULT 0,
    extra_config TEXT NOT NULL DEFAULT '{}',
    remark TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sites_name ON sites(name);
CREATE INDEX IF NOT EXISTS idx_sites_enabled ON sites(enabled);
CREATE INDEX IF NOT EXISTS idx_sites_application ON sites(upstream_id);
CREATE INDEX IF NOT EXISTS idx_sites_certificate ON sites(certificate_id);

CREATE TABLE IF NOT EXISTS config_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    version INTEGER NOT NULL,
    snapshot TEXT NOT NULL,
    checksum TEXT NOT NULL,
    operator_id INTEGER,
    description TEXT,
    created_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_config_versions_version ON config_versions(version);

CREATE TABLE IF NOT EXISTS traffic_stats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    site_id INTEGER NOT NULL,
    interval_type TEXT NOT NULL,
    bucket_time DATETIME NOT NULL,
    request_count INTEGER NOT NULL DEFAULT 0,
    inbound_bytes INTEGER NOT NULL DEFAULT 0,
    outbound_bytes INTEGER NOT NULL DEFAULT 0,
    status_2xx INTEGER NOT NULL DEFAULT 0,
    status_3xx INTEGER NOT NULL DEFAULT 0,
    status_4xx INTEGER NOT NULL DEFAULT 0,
    status_5xx INTEGER NOT NULL DEFAULT 0,
    response_time_sum INTEGER NOT NULL DEFAULT 0,
    response_time_max INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_traffic_bucket ON traffic_stats(site_id, interval_type, bucket_time);
CREATE INDEX IF NOT EXISTS idx_traffic_time ON traffic_stats(bucket_time);

CREATE TABLE IF NOT EXISTS audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT,
    client_ip TEXT,
    result TEXT NOT NULL,
    detail TEXT,
    created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_logs(user_id);

CREATE TABLE IF NOT EXISTS system_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    level TEXT NOT NULL,
    category TEXT NOT NULL,
    message TEXT NOT NULL,
    fields TEXT,
    created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_system_logs_created ON system_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_system_logs_level ON system_logs(level);

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT,
    updated_at DATETIME NOT NULL
);
