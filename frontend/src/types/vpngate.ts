export type RoutingPreset = 'direct' | 'bypass-cn' | 'blacklist' | 'global'
export type UpstreamProtocol = 'socks4' | 'socks4a' | 'socks5' | 'http'
export interface VPNGateSettings {
  upstream: {
    host: string
    port: number
    protocol: UpstreamProtocol
    username: string
    password: string
    processPaths: string[]
  }
  poolCapacity: number
  refreshMinutes: number
  routingPreset: RoutingPreset
  mixedPort: number
  tunEnabled: boolean
  systemProxyEnabled: boolean
  testURL: string
  testConcurrency: number
  testTimeoutSeconds: number
  rulesAutoUpdate: boolean
  rulesUpdateHours: number
  cnRulesURL: string
  gfwRulesURL: string
  corePath: string
}
export interface VPNGateNode {
  id: string
  hostname: string
  ip: string
  country: string
  countryCode: string
  port: number
  protocol: string
  score: number
  speed: number
  configHash: string
  status: 'untested' | 'testing' | 'available' | 'failed' | 'canceled'
  latencyMs: number | null
  handshakeMs: number | null
  lastTested: string
  lastSeen: string
  error: string
  failureKind: string
  pathFingerprint: string
}
export interface VPNGateJob {
  id: string
  kind: 'refresh' | 'test' | 'rules' | 'core'
  status: 'running' | 'completed' | 'canceled' | 'failed'
  total: number
  completed: number
  available: number
  failed: number
  message: string
  startedAt: string
  finishedAt: string
}
export interface VPNGateState {
  settings: VPNGateSettings
  nodes: VPNGateNode[]
  selectedNodeId: string
  runtime: {
    status: 'stopped' | 'starting' | 'connected' | 'protected' | 'error' | 'direct'
    message: string
    protected: boolean
    requiresAdmin: boolean
    connectedNodeId: string
    startedAt: string
    coreVersion: string
    upstreamOwner: string
  }
  jobs: VPNGateJob[]
  repair: { needed: boolean; requiresAdmin: boolean; message: string }
  rules: {
    cn: { ready: boolean; updatedAt: string; error: string }
    gfw: { ready: boolean; updatedAt: string; error: string }
  }
  service: {
    installed: boolean
    running: boolean
    protected: boolean
    requiresAdmin: boolean
    message: string
  }
}
export interface CoreStatus {
  path: string
  version: string
  installed: boolean
  error: string
  installedVersions?: { version: string; path: string; selected: boolean }[]
}
export interface CoreRelease {
  version: string
  prerelease: boolean
  publishedAt: string
}
export interface VPNGateMethods {
  'core.status': { payload: undefined; result: CoreStatus }
  'core.releases': { payload: undefined; result: { releases: CoreRelease[] } }
  'core.install': { payload: { version: string }; result: VPNGateJob }
  'core.use': { payload: { version: string }; result: VPNGateState }
  state: { payload: undefined; result: VPNGateState }
  'settings.update': { payload: VPNGateSettings; result: VPNGateState }
  'nodes.refresh': { payload: { manual: boolean }; result: VPNGateJob }
  'nodes.use': { payload: { id: string }; result: VPNGateState }
  'nodes.deleteFailed': { payload: undefined; result: VPNGateState }
  'nodes.delete': { payload: { id: string }; result: VPNGateState }
  connect: { payload: undefined; result: VPNGateState }
  stop: { payload: undefined; result: VPNGateState }
  'tests.start': { payload: { ids: string[] }; result: VPNGateJob }
  'jobs.cancel': { payload: { id: string }; result: VPNGateState }
  'rules.update': { payload: undefined; result: VPNGateState }
  repair: { payload: undefined; result: VPNGateState }
  'runtime.elevate': { payload: undefined; result: { restarting: boolean } }
}
