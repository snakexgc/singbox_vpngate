const labels: Record<string, string> = {
  stopped: '已停止 · Stopped',
  starting: '正在连接 · Starting',
  connecting: '连接中 · Connecting',
  connected: '已连接 · Connected',
  offline: 'VPNGate 不可用 · VPNGate unavailable',
  protected: '连接已停止 · Connection stopped',
  degraded: '连接异常 · Connection degraded',
  error: '错误 · Error',
  direct: '直连 · Direct',
  untested: '未测试 · Untested',
  testing: '测试中 · Testing',
  available: '可用 · Available',
  failed: '失败 · Failed',
  canceled: '已取消 · Canceled',
  running: '运行中 · Running',
  completed: '已完成 · Completed',
  refresh: '刷新节点 · Refresh nodes',
  test: '连通性测试 · Connectivity test',
  rules: '规则更新 · Rule update',
  core: '内核下载 · Core download',
}
export const getVPNGateText = (raw: string, locale: string) => {
  const value = labels[raw] || raw
  const separator = value.indexOf(' · ')
  if (separator < 0) return value
  return locale.startsWith('zh') ? value.slice(0, separator) : value.slice(separator + 3)
}
