const levelMap: Record<string, string> = {
  debug: '调试',
  info: '信息',
  warn: '警告',
  warning: '警告',
  error: '错误',
}

const categoryMap: Record<string, string> = {
  config: '配置',
  application: '应用服务',
  upstream: '应用服务',
  site: '站点',
  certificate: '证书',
  auth: '认证',
  system: '系统',
  dns: 'DNS',
}

const auditActionMap: Record<string, string> = {
  login: '登录',
  logout: '退出登录',
  change_password: '修改密码',
  create_site: '创建站点',
  update_site: '更新站点',
  delete_site: '删除站点',
  enable_site: '启用站点',
  disable_site: '禁用站点',
  import_site: '导入站点',
  create_application: '创建应用服务',
  update_application: '更新应用服务',
  delete_application: '删除应用服务',
  create_upstream: '创建应用服务',
  update_upstream: '更新应用服务',
  delete_upstream: '删除应用服务',
  add_application_node: '添加节点',
  update_application_node: '更新节点',
  delete_application_node: '删除节点',
  add_upstream_node: '添加节点',
  update_upstream_node: '更新节点',
  delete_upstream_node: '删除节点',
  issue_certificate: '申请证书',
  upload_certificate: '上传证书',
  delete_certificate: '删除证书',
  renew_certificate: '续期证书',
  create_dns_provider: '创建 DNS 服务商',
  update_dns_provider: '更新 DNS 服务商',
  delete_dns_provider: '删除 DNS 服务商',
  delete_acme_account: '删除 ACME 账户',
  create_api_token: '创建 API 令牌',
  delete_api_token: '吊销 API 令牌',
  revoke_session: '吊销会话',
  revoke_other_sessions: '退出其他会话',
  update_settings: '修改系统设置',
  restart_engine: '重启代理引擎',
  backup: '下载备份',
  restore: '恢复备份',
  rollback_config: '回滚配置',
  delete_config_version: '删除配置版本',
}

const resourceMap: Record<string, string> = {
  user: '用户',
  session: '会话',
  site: '站点',
  application: '应用服务',
  upstream: '应用服务',
  certificate: '证书',
  dns_provider: 'DNS 服务商',
  acme_account: 'ACME 账户',
  api_token: 'API 令牌',
  system: '系统',
  config: '配置',
}

export function levelLabel(level: string): string {
  return levelMap[level?.toLowerCase()] || level || '-'
}

export function levelTagType(level: string): 'danger' | 'warning' | 'info' | 'success' {
  if (level === 'error') return 'danger'
  if (level === 'warn' || level === 'warning') return 'warning'
  if (level === 'debug') return 'info'
  return 'success'
}

export function categoryLabel(category: string): string {
  return categoryMap[category?.toLowerCase()] || category || '-'
}

export function auditActionLabel(action: string): string {
  return auditActionMap[action] || action || '-'
}

export function resourceLabel(resource: string): string {
  return resourceMap[resource] || resource || '-'
}

export function resultLabel(result: string): string {
  return ({ success: '成功', failure: '失败' } as Record<string, string>)[result] || result || '-'
}

export function messageLabel(message: string): string {
  return (message || '').replaceAll('上游节点', '节点').replaceAll('上游服务组', '应用服务').replaceAll('上游服务', '应用服务')
}
