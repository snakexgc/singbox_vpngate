import type {
  VPNGateSettings,
  VPNGateState,
  VPNGateNode,
  RoutingPreset,
  UpstreamProtocol,
} from '../types/vpngate'

export const upstreamProtocolOptions = [
  { label: 'SOCKS5', value: 'socks5' },
  { label: 'HTTP', value: 'http' },
  { label: 'SOCKS4', value: 'socks4' },
  { label: 'SOCKS4a', value: 'socks4a' },
] satisfies { label: string; value: UpstreamProtocol }[]

export const upstreamProtocolHints: Record<UpstreamProtocol, string> = {
  http: 'HTTP 代理使用 CONNECT 承载 OpenVPN，也支持 HTTPS 目标；无需另选 HTTPS 协议。 · HTTP CONNECT carries OpenVPN and HTTPS destinations; no separate HTTPS protocol is needed.',
  socks4:
    'SOCKS4 使用 IPv4，本机解析目标域名；仅适用于未启用密码认证的 mixed 端口。用户 ID 不是登录认证，不支持密码。 · SOCKS4 uses IPv4 and resolves target hostnames locally. The mixed listener must have password authentication disabled. User ID is not login authentication; passwords are unsupported.',
  socks4a:
    'SOCKS4a 将目标域名交给代理解析；仅适用于未启用密码认证的 mixed 端口。用户 ID 不是登录认证，不支持密码。 · SOCKS4a lets the proxy resolve target hostnames. The mixed listener must have password authentication disabled. User ID is not login authentication; passwords are unsupported.',
  socks5:
    'SOCKS5 支持代理端解析域名，可按需填写用户名和密码。 · SOCKS5 supports proxy-side hostname resolution and optional username/password authentication.',
}

// Settings has no routing control: its draft must preserve the latest mode from
// Overview. A successful RPC only commits this draft if no edit followed
// its immutable snapshot.
export const submitVPNGateSettingsDraft = async (
  draft: VPNGateSettings,
  latestPreset: RoutingPreset,
  editRevision: () => number,
  submit: (snapshot: VPNGateSettings) => Promise<boolean>,
): Promise<boolean> => {
  const revision = editRevision()
  const snapshot: VPNGateSettings = JSON.parse(JSON.stringify(draft))
  snapshot.routingPreset = latestPreset
  return (await submit(snapshot)) && editRevision() === revision
}

export const defaultVPNGateSettings = (): VPNGateSettings => ({
  upstream: {
    host: '127.0.0.1',
    port: 10808,
    protocol: 'http',
    username: '',
    password: '',
    processPaths: [],
  },
  poolCapacity: 200,
  refreshMinutes: 30,
  routingPreset: 'bypass-cn',
  mixedPort: 20122,
  tunEnabled: false,
  systemProxyEnabled: false,
  testURL: 'https://www.gstatic.com/generate_204',
  testConcurrency: 3,
  testTimeoutSeconds: 45,
  rulesAutoUpdate: true,
  rulesUpdateHours: 12,
  cnRulesURL: 'https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/cn.srs',
  gfwRulesURL:
    'https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/gfw.srs',
  corePath: '',
})

type VPNGateStateInput = Omit<Partial<VPNGateState>, 'runtime' | 'repair'> & {
  runtime?: Partial<VPNGateState['runtime']>
  repair?: Partial<VPNGateState['repair']>
}

export const normalizeVPNGateState = (value: VPNGateStateInput): VPNGateState => {
  const defaults = defaultVPNGateSettings()
  return {
    ...value,
    logLevel: value.logLevel || 'info',
    settings: {
      ...defaults,
      ...value.settings,
      upstream: {
        ...defaults.upstream,
        ...value.settings?.upstream,
        processPaths: value.settings?.upstream?.processPaths || [],
      },
    },
    nodes: value.nodes || [],
    selectedNodeId: value.selectedNodeId || '',
    jobs: value.jobs || [],
    runtime: {
      status: 'stopped',
      logLevel: '',
      message: '',
      protected: false,
      requiresAdmin: false,
      tunEnabled: false,
      systemProxyEnabled: false,
      connectedNodeId: '',
      startedAt: '',
      coreVersion: '',
      upstreamOwner: '',
      ...value.runtime,
    },
    repair: { needed: false, requiresAdmin: false, message: '', ...value.repair },
    rules: value.rules || {
      cn: { ready: false, updatedAt: '', error: '' },
      gfw: { ready: false, updatedAt: '', error: '' },
    },
    service: value.service || {
      installed: false,
      running: false,
      protected: false,
      requiresAdmin: false,
      message: '',
    },
  }
}

export const isVPNGateCoreRunning = (status: VPNGateState['runtime']['status'] | undefined) =>
  status === 'connecting' || status === 'connected' || status === 'direct' || status === 'offline'

export const canConnectVPNGate = (state: VPNGateState | undefined, busy = '') =>
  !!state &&
  !busy &&
  !state.jobs.some((job) => job.kind === 'core' && job.status === 'running') &&
  state.runtime.status !== 'starting' &&
  !isVPNGateCoreRunning(state.runtime.status)

export const validateVPNGateSettings = (settings: VPNGateSettings): string | undefined => {
  const port = (value: number) => Number.isInteger(value) && value > 0 && value <= 65535
  if (!settings.upstream.host.trim()) return 'Enter the upstream proxy host.'
  if (!port(settings.upstream.port)) return 'Enter the upstream proxy port (1–65535).'
  if (!upstreamProtocolOptions.some((option) => option.value === settings.upstream.protocol))
    return '请选择 HTTP、SOCKS4、SOCKS4a 或 SOCKS5。 · Choose HTTP, SOCKS4, SOCKS4a or SOCKS5.'
  if (
    ['socks4', 'socks4a'].includes(settings.upstream.protocol) &&
    settings.upstream.password !== ''
  )
    return 'SOCKS4 / SOCKS4a 不支持密码，请显式清除密码或选择 HTTP / SOCKS5。 · SOCKS4 / SOCKS4a do not support passwords. Clear the password explicitly or select HTTP / SOCKS5.'
  if (!port(settings.mixedPort)) return 'Local mixed port must be between 1 and 65535.'
  if (
    settings.upstream.port === settings.mixedPort &&
    ['127.0.0.1', 'localhost', '::1'].includes(settings.upstream.host.trim())
  )
    return 'The upstream cannot point to the local mixed listener.'
  for (const [name, value, max] of [
    ['Pool capacity', settings.poolCapacity, 10000],
    ['Refresh minutes', settings.refreshMinutes, 10080],
    ['Test concurrency', settings.testConcurrency, 32],
    ['Test timeout', settings.testTimeoutSeconds, 600],
    ['Rule update hours', settings.rulesUpdateHours, 8760],
  ] as const) {
    if (!Number.isInteger(value) || value < 1 || value > max)
      return `${name} must be an integer from 1 to ${max}.`
  }
  for (const text of [settings.testURL, settings.cnRulesURL, settings.gfwRulesURL]) {
    try {
      const url = new URL(text)
      if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password)
        return 'Use HTTP(S) URLs without embedded credentials.'
    } catch {
      return 'Enter valid HTTP(S) URLs.'
    }
  }
}

const countryNames = {
  zh: new Intl.DisplayNames(['zh-CN'], { type: 'region', fallback: 'none' }),
  en: new Intl.DisplayNames(['en'], { type: 'region', fallback: 'none' }),
}

export const formatVPNGateCountry = (code?: string, country?: string, locale = 'zh-CN') => {
  const countryCode = code?.trim().toUpperCase()
  if (countryCode && /^[A-Z]{2}$/.test(countryCode)) {
    const names = countryNames[locale.startsWith('zh') ? 'zh' : 'en']
    return names.of(countryCode) || country?.trim() || countryCode
  }
  return country?.trim() || ''
}

type NodeCountry = Partial<
  Pick<VPNGateNode, 'country' | 'countryCode' | 'egressCountry' | 'egressCountryCode'>
>

export const formatVPNGateNodeCountry = (node: NodeCountry, locale = 'zh-CN') =>
  formatVPNGateCountry(node.egressCountryCode, node.egressCountry, locale) ||
  formatVPNGateCountry(node.countryCode, node.country, locale)

export const formatVPNGateRegion = (region?: string, city?: string) => {
  const locations = [region?.trim(), city?.trim()].filter((value): value is string => !!value)
  return locations
    .filter(
      (value, index) =>
        locations.findIndex((location) => location.toLowerCase() === value.toLowerCase()) === index,
    )
    .join(' / ')
}

export const formatVPNGateEndpoint = (ip: string, port: number) =>
  `${ip.includes(':') ? `[${ip}]` : ip}:${port}`

export const filterVPNGateNodes = (nodes: VPNGateNode[], query: string, status: string) => {
  const search = query.toLowerCase().trim()
  return nodes.filter(
    (node) =>
      (!status || node.status === status) &&
      [
        node.hostname,
        node.ip,
        node.country,
        node.countryCode,
        node.egressIP,
        node.egressCountry,
        node.egressCountryCode,
        node.egressRegion,
        node.egressCity,
        formatVPNGateCountry(node.egressCountryCode, node.egressCountry, 'zh-CN'),
        formatVPNGateCountry(node.egressCountryCode, node.egressCountry, 'en'),
        formatVPNGateCountry(node.countryCode, node.country, 'zh-CN'),
        formatVPNGateCountry(node.countryCode, node.country, 'en'),
      ].some((value) => value?.toLowerCase().includes(search)),
  )
}

export const routingOptions = [
  { label: '直连 · Direct', value: 'direct' },
  { label: '绕过大陆 · Bypass Mainland', value: 'bypass-cn' },
  { label: '黑名单 · Blacklist', value: 'blacklist' },
  { label: '全局 · Global', value: 'global' },
] satisfies { label: string; value: RoutingPreset }[]
