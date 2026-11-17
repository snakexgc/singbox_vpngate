import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { VPNGateRequest } from '@/bridge/vpngate'

import { normalizeVPNGateState, validateVPNGateSettings } from '@/utils/vpngate'

import type { CoreStatus, VPNGateMethods, VPNGateSettings, VPNGateState } from '@/types/vpngate'

export const useVPNGateStore = defineStore('vpngate', () => {
  const state = ref<VPNGateState>()
  const error = ref('')
  const notice = ref('')
  const busy = ref('')
  const loading = ref(false)
  const coreStatus = ref<CoreStatus>()
  const coreStatusLoading = ref(false)
  let coreRequest: Promise<void> | undefined
  let monitorCore = false
  let coreRefreshQueued = false
  let coreStateKey = ''
  let timer: ReturnType<typeof setTimeout> | undefined
  let polling = false
  let generation = 0
  let revision = 0
  let operationError = false
  let stateRequest: Promise<void> | undefined
  let foregroundRefreshes = 0
  const selectedNode = computed(() =>
    state.value?.nodes.find((node) => node.id === state.value?.selectedNodeId),
  )
  const activeJobs = computed(
    () => state.value?.jobs.filter((job) => job.status === 'running') || [],
  )
  const coreKey = () =>
    JSON.stringify([
      state.value?.settings.corePath,
      state.value?.jobs
        .filter((job) => job.kind === 'core' && job.status !== 'running')
        .map((job) => `${job.id}:${job.status}:${job.finishedAt}`)
        .sort(),
    ])
  const refreshCore = () => {
    if (coreRequest) return coreRequest
    const current = generation
    coreStatusLoading.value = true
    coreRequest = (async () => {
      try {
        const result = await VPNGateRequest('core.status', undefined)
        if (current === generation) coreStatus.value = result
      } catch (cause) {
        if (current === generation)
          coreStatus.value = {
            path: state.value?.settings.corePath || '',
            version: '',
            installed: false,
            error: cause instanceof Error ? cause.message : 'Cannot inspect sing-box core.',
          }
      } finally {
        coreRequest = undefined
        if (current === generation) coreStatusLoading.value = false
        if (current === generation && monitorCore && coreRefreshQueued) {
          coreRefreshQueued = false
          void refreshCore()
        }
      }
    })()
    return coreRequest
  }
  const startCoreMonitoring = () => {
    monitorCore = true
    coreStateKey = coreKey()
    return refreshCore()
  }
  const syncCoreStatus = () => {
    if (!monitorCore) return
    const key = coreKey()
    if (key === coreStateKey) return
    coreStateKey = key
    if (coreRequest) {
      coreRefreshQueued = true
      return
    }
    void refreshCore()
  }
  const readState = () => {
    if (stateRequest) return stateRequest
    const current = generation
    const priorRevision = revision
    stateRequest = (async () => {
      try {
        const result = await VPNGateRequest('state', undefined)
        if (current === generation && priorRevision === revision) {
          state.value = normalizeVPNGateState(result)
          syncCoreStatus()
          if (!operationError) error.value = ''
        }
      } catch (cause) {
        if (current === generation && priorRevision === revision && !operationError)
          error.value = cause instanceof Error ? cause.message : 'Cannot read VPNGate state.'
      } finally {
        stateRequest = undefined
      }
    })()
    return stateRequest
  }
  // Polls and synchronization after an operation update data without animating
  // unrelated refresh buttons. They never wait for an obsolete in-flight poll.
  const refreshQuietly = async () => {
    if (stateRequest) return
    await readState()
  }
  const refresh = async () => {
    foregroundRefreshes++
    loading.value = true
    try {
      // An explicit click can join a background read without another RPC, while
      // keeping its own indicator visible until that request actually finishes.
      await readState()
    } finally {
      loading.value = --foregroundRefreshes > 0
    }
  }
  const run = async <M extends keyof VPNGateMethods>(
    method: M,
    payload: VPNGateMethods[M]['payload'],
  ) => {
    if (busy.value) return false
    busy.value = method
    revision++
    operationError = false
    error.value = ''
    notice.value = ''
    try {
      const result = await VPNGateRequest(method, payload)
      // Polls started during a mutation can also contain the previous state.
      revision++
      if ('settings' in result) state.value = normalizeVPNGateState(result)
      syncCoreStatus()
      if (method === 'repair' && 'settings' in result)
        notice.value =
          result.repair?.message ||
          '已停止连接并恢复本应用的网络设置，节点和配置已保留。 · Connection stopped and this application’s network settings restored. Nodes and settings retained.'
      if (method === 'runtime.elevate' && 'restarting' in result) {
        if (!result.restarting) throw new Error('Administrator restart was not started. Try again.')
        notice.value =
          '正在以管理员身份重启，请在重启后重试原操作。 · Restarting as administrator. Retry your action after restart.'
        return true
      }
      error.value = ''
      await refreshQuietly()
      return true
    } catch (cause) {
      operationError = true
      error.value = cause instanceof Error ? cause.message : 'VPNGate operation failed.'
      // A rejected repair may reveal legacy cleanup that needs elevation.
      await refreshQuietly()
      return false
    } finally {
      busy.value = ''
    }
  }
  const saveSettings = async (settings: VPNGateSettings) => {
    const invalid = validateVPNGateSettings(settings)
    if (invalid) {
      operationError = true
      error.value = invalid
      return false
    }
    return run('settings.update', settings)
  }
  const startPolling = () => {
    if (polling) return
    polling = true
    const current = ++generation
    const tick = async () => {
      await refreshQuietly()
      if (polling && current === generation)
        timer = setTimeout(tick, activeJobs.value.length ? 1000 : 2500)
    }
    void tick()
  }
  const stopPolling = () => {
    polling = false
    generation++
    monitorCore = false
    coreRefreshQueued = false
    coreStatusLoading.value = false
    clearTimeout(timer)
    timer = undefined
  }
  return {
    state,
    error,
    notice,
    busy,
    loading,
    coreStatus,
    coreStatusLoading,
    refreshCore,
    startCoreMonitoring,
    selectedNode,
    activeJobs,
    refresh,
    run,
    saveSettings,
    startPolling,
    stopPolling,
  }
})
