import { Request } from '@/api/request'
import { WebSockets } from '@/api/websocket'
import { HttpGet } from '@/bridge'

import type {
  CoreApiConfig,
  CoreApiProxies,
  CoreApiConnections,
  CoreApiWsDataMap,
} from '@/types/kernel'

type WsKey = keyof CoreApiWsDataMap
type WsChannel<K extends WsKey> = {
  url: string
  params?: Recordable
  handlers: Array<(data: CoreApiWsDataMap[K]) => void>
  isActive: boolean
  connect?: () => void
  disconnect?: () => void
}

export enum Api {
  Configs = '/configs',
  Memory = '/memory',
  Proxies = '/proxies',
  ProxyDelay = '/proxies/{0}/delay',
  Connections = '/connections',
  Traffic = '/traffic',
  Logs = '/logs',
}

const setupCoreApi = (protocol: 'http' | 'ws') => {
  const base = `${protocol}://127.0.0.1:20123`
  const bearer = ''

  if (protocol === 'http') {
    request.base = base
    request.bearer = bearer
  } else {
    websocket.base = base
    websocket.bearer = bearer
  }
}

const request = new Request({ beforeRequest: () => setupCoreApi('http'), timeout: 60 * 1000 })
const websocket = new WebSockets({ beforeConnect: () => setupCoreApi('ws') })

const wsChannels: {
  [K in WsKey]: WsChannel<K>
} = {
  logs: { url: Api.Logs, isActive: false, handlers: [], params: { level: 'debug' } },
  memory: { url: Api.Memory, isActive: false, handlers: [] },
  traffic: { url: Api.Traffic, isActive: false, handlers: [] },
  connections: { url: Api.Connections, isActive: false, handlers: [] },
}

const createCoreWSHandlerRegister = <K extends WsKey>(key: K) => {
  const channel = wsChannels[key]

  return (cb: (data: CoreApiWsDataMap[K]) => void) => {
    channel.handlers.push(cb)

    if (!channel.isActive && channel.connect) {
      channel.connect()
      channel.isActive = true
    }

    const unregister = () => {
      const idx = channel.handlers.indexOf(cb)
      idx !== -1 && channel.handlers.splice(idx, 1)
      if (channel.isActive && channel.disconnect && channel.handlers.length === 0) {
        channel.disconnect()
        channel.isActive = false
      }
    }
    return unregister
  }
}

// restful api
export const probeApiAvailability = () => request.get('/version')
export const getConfigs = () => request.get<CoreApiConfig>(Api.Configs)
export const setConfigs = (body = {}) => request.patch<null>(Api.Configs, body)
export const getProxies = () => request.get<CoreApiProxies>(Api.Proxies)
export const getConnections = () => request.get<CoreApiConnections>(Api.Connections)
export const deleteConnection = (id: string) => request.delete<null>(Api.Connections + '/' + id)
export const useProxy = (group: string, proxy: string) => {
  return request.put<null>(Api.Proxies + '/' + group, { name: proxy })
}
export const getProxyDelay = async (proxy: string, url: string, timeout: number, cancelId = '') => {
  setupCoreApi('http')
  const query = new URLSearchParams({ url, timeout: String(timeout) }).toString()
  const path = Api.ProxyDelay.replace('{0}', proxy)
  const headers: Record<string, string> = {}
  if (request.bearer) {
    headers['Authorization'] = `Bearer ${request.bearer}`
  }
  const { status, body } = await HttpGet<any>(`${request.base}${path}?${query}`, headers, {
    Proxy: '',
    Timeout: request.timeout / 1000,
    CancelId: cancelId,
  })
  if (status === 204) {
    return null as unknown as Record<string, number>
  }
  if ([504, 401, 503].includes(status)) {
    throw body?.message ?? body
  }
  return body as Record<string, number>
}

// websocket api
export const onLogs = createCoreWSHandlerRegister('logs')
export const onMemory = createCoreWSHandlerRegister('memory')
export const onTraffic = createCoreWSHandlerRegister('traffic')
export const onConnections = createCoreWSHandlerRegister('connections')
export const initWebsocket = () => {
  Object.values(wsChannels).forEach((channel) => {
    const { connect, disconnect } = websocket.createWS({
      url: channel.url,
      params: channel.params,
      cb: (data) => channel.handlers.forEach((cb) => cb(data)),
    })
    channel.connect = connect
    channel.disconnect = disconnect
    channel.isActive = false
    if (channel.handlers.length > 0) {
      channel.connect()
      channel.isActive = true
    }
  })
}
export const destroyWebsocket = () => {
  Object.values(wsChannels).forEach((channel) => {
    channel.disconnect?.()
    channel.connect = undefined
    channel.disconnect = undefined
    channel.isActive = false
  })
}
