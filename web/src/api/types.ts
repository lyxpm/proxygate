export interface User {
  id: number
  username: string
  role: 'admin' | 'viewer'
  enabled: boolean
  last_login_at?: string
}

export interface Page<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}

export interface SiteOptions {
  stripPrefix: string
  addPrefix: string
  requestHeaders: HeaderRule[]
  responseHeaders: HeaderRule[]
  upstreamSni: string
  upstreamTlsVerify?: boolean
  keepAlive?: boolean
  useUpstreamHost: boolean
  forwardClientIp?: boolean
  bufferSize: number
  ipMode: '' | 'allow' | 'deny'
  ipList: string[]
}

export interface HeaderRule {
  name: string
  value: string
}

export interface Site {
  id: number
  name: string
  enabled: boolean
  listen_port: number
  domains: string[]
  protocol: 'http' | 'https'
  application_id: number
  application_name: string
  certificate_id?: number | null
  certificate_name: string
  certificate_expires_at?: string | null
  redirect_https: boolean
  proxy_timeout: number
  max_body_size: number
  remark: string
  options: SiteOptions
  created_at: string
  updated_at: string
}

export interface SiteInput {
  name: string
  enabled: boolean
  listen_port: number
  domains: string[]
  protocol: 'http' | 'https'
  application_id: number
  certificate_id?: number | null
  redirect_https: boolean
  proxy_timeout: number
  max_body_size: number
  remark: string
  options: SiteOptions
}

export interface Node {
  id: number
  application_id: number
  address: string
  weight: number
  priority: number
  enabled: boolean
  tls_verify: boolean
  healthy: boolean
  active_connections: number
  total_requests: number
}

export interface Application {
  id: number
  name: string
  strategy: string
  enabled: boolean
  health_check_enabled: boolean
  health_check_type: string
  health_check_path: string
  health_check_interval: number
  health_check_timeout: number
  healthy_threshold: number
  unhealthy_threshold: number
  passive_failure_limit: number
  passive_cooldown: number
  nodes: Node[]
  created_at: string
  updated_at: string
}

export interface ApplicationInput {
  name: string
  strategy: string
  enabled: boolean
  health_check_enabled: boolean
  health_check_type: string
  health_check_path: string
  health_check_interval: number
  health_check_timeout: number
  healthy_threshold: number
  unhealthy_threshold: number
  passive_failure_limit: number
  passive_cooldown: number
  nodes?: NodeInput[]
}

export interface NodeInput {
  address: string
  weight: number
  priority: number
  enabled: boolean
  tls_verify: boolean
}

export interface Certificate {
  id: number
  name: string
  domains: string[]
  issuer: string
  certificate_type: 'acme' | 'upload'
  challenge_type: string
  status: string
  auto_renew: boolean
  expires_at?: string | null
  days_remaining?: number | null
  last_renew_at?: string | null
  next_renew_at?: string | null
  renew_attempts: number
  last_error: string
  created_at: string
  updated_at: string
}

export interface DNSProvider {
  id: number
  name: string
  provider_type: string
  enabled: boolean
  meta: {
    name: string
    title: string
    id_label: string
    secret_label: string
    extra?: { key: string; label: string; placeholder?: string }[]
    doc_url?: string
  }
  created_at: string
  updated_at: string
}

export interface ACMEAccount {
  id: number
  email: string
  ca_url: string
  status: string
  created_at: string
}

export interface HostStats {
  cpu_percent: number
  memory_used: number
  memory_total: number
  memory_percent: number
  sampled_at: string
}

export interface DashboardOverview {
  sites: { total: number; healthy: number }
  enabled_sites: number
  applications: { nodes_total: number; nodes_healthy: number; groups: number }
  certificates: { total: number; expiring: number }
  connections: number
  host: HostStats
  today: {
    requests: number
    inbound_bytes: number
    outbound_bytes: number
    avg_ms: number
    p95_ms: number
    p99_ms: number
  }
  status_codes: Record<string, number>
  recent_events: SystemLog[]
}

export interface TrafficPoint {
  time: string
  requests: number
  inbound_bytes: number
  outbound_bytes: number
  status_2xx: number
  status_3xx: number
  status_4xx: number
  status_5xx: number
  avg_ms: number
}

export interface Totals {
  requests: number
  inbound_bytes: number
  outbound_bytes: number
  status_2xx: number
  status_3xx: number
  status_4xx: number
  status_5xx: number
  avg_ms: number
  max_ms: number
  p95_ms: number
  p99_ms: number
}

export interface SystemLog {
  ID: number
  Level: string
  Category: string
  Message: string
  Fields: string
  CreatedAt: string
}

export interface AccessLog {
  time: string
  site_id: number
  site_name: string
  domain: string
  client_ip: string
  method: string
  path: string
  protocol: string
  status: number
  duration_ms: number
  application: string
  request_bytes: number
  response_bytes: number
  user_agent: string
  request_id: string
}

export interface AuditLog {
  ID: number
  UserID?: number | null
  Action: string
  ResourceType: string
  ResourceID: string
  ClientIP: string
  Result: string
  Detail: string
  CreatedAt: string
}

export interface EngineStatus {
  running: boolean
  started_at: string
  config_version: number
  last_apply: string
  last_error?: string
  active_connections: number
  ports: { port: number; tls: boolean; sites: number }[]
  applications: {
    id: number
    name: string
    strategy: string
    nodes: { id: number; address: string; healthy: boolean; active_connections: number; requests: number }[]
  }[]
}

export interface ConfigVersion {
  ID: number
  Version: number
  Checksum: string
  Description: string
  CreatedAt: string
}

export interface APIToken {
  id: number
  name: string
  scopes: string[]
  expires_at?: string | null
  last_used_at?: string | null
  created_at: string
}

export interface UserSession {
  id: string
  client_ip: string
  user_agent: string
  created_at: string
  last_seen_at?: string | null
  idle_expires_at?: string
  expires_at: string
  current: boolean
}
