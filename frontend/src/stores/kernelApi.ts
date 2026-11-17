import { defineStore } from 'pinia'
import { computed } from 'vue'

import { onLogs, onMemory, onConnections, onTraffic } from '@/api/kernel'
import { useVPNGateStore } from '@/stores/vpngate'

export type ProxyType = 'mixed' | 'http' | 'socks'
export type ProxyEndpoint = {
  schema: 'http' | 'socks5'
  host: string
  port: number
  username: string
  password: string
  proxyType: ProxyType
}

// The desktop manager owns processes and configuration; this store only reads its state.
export const useKernelApiStore = defineStore('kernelApi', () => {
  const vpn = useVPNGateStore()
  const running = computed(() => ['connected', 'direct'].includes(vpn.state?.runtime.status || ''))
  const starting = computed(
    () => vpn.state?.runtime.status === 'starting' || vpn.busy === 'connect',
  )
  const stopping = computed(() => vpn.busy === 'stop')
  const restarting = computed(() => false)

  const getProxyEndpoint = (): ProxyEndpoint | undefined => {
    const port = vpn.state?.settings.mixedPort
    if (!running.value || !Number.isInteger(port) || !port || port < 1 || port > 65535) return

    return {
      schema: 'http',
      host: '127.0.0.1',
      port,
      username: '',
      password: '',
      proxyType: 'mixed',
    }
  }

  return {
    running,
    starting,
    stopping,
    restarting,
    getProxyEndpoint,
    onLogs,
    onMemory,
    onTraffic,
    onConnections,
  }
})
