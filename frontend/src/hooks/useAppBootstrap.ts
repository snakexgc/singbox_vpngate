import { onUnmounted, ref, watch } from 'vue'

import { useAppSettingsStore, useEnvStore } from '@/stores'
import { useVPNGateStore } from '@/stores/vpngate'
import { updateTrayAndMenus } from '@/utils/tray'

// The desktop manager owns the core and scheduler. Elevation/repair are explicit user actions.
export const useAppBootstrap = () => {
  const loading = ref(true)
  const percent = ref(0)
  const hasError = ref(false)
  const vpn = useVPNGateStore()
  watch(
    () => [vpn.state?.runtime.status, vpn.state?.runtime.requiresAdmin, vpn.busy],
    updateTrayAndMenus,
  )
  const env = useEnvStore()
  const settings = useAppSettingsStore()
  const initialize = async () => {
    try {
      await env.setupEnv()
      percent.value = 50
      await settings.setupAppSettings()
      await vpn.refresh()
      await vpn.startCoreMonitoring()
      vpn.startPolling()
    } catch {
      hasError.value = true
      vpn.error = 'Cannot initialize the desktop application.'
    } finally {
      percent.value = 100
      loading.value = false
    }
  }
  void initialize()
  onUnmounted(vpn.stopPolling)
  return { loading, percent, hasError }
}
