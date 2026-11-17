import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'

import { VPNGateRequest } from '@/bridge/vpngate'

import { useVPNGateStore } from '@/stores/vpngate'

import type { CoreRelease, CoreStatus } from '@/types/vpngate'

type InstalledCore = NonNullable<CoreStatus['installedVersions']>[number]
const bareVersion = (version: string) => version.replace(/^v/, '').split('+')[0] || ''
const isAlphaVersion = (version: string) => bareVersion(version).includes('-')
const compareVersions = (left: string, right: string) =>
  bareVersion(left).localeCompare(bareVersion(right), 'en', { numeric: true })

// Both original branch rows share metadata and the one runtime-owned install job.
export const useCoreManagerStore = defineStore('core-manager', () => {
  const vpn = useVPNGateStore()
  const releases = ref<CoreRelease[]>([])
  const releasesLoading = ref(false)
  const releasesError = ref('')
  let releasesLoaded = false
  let releaseRequest: Promise<boolean> | undefined
  const installation = ref<{
    isAlpha: boolean
    version: string
    previousJobs: string[]
    jobId?: string
    accepted: boolean
  }>()
  const installPending = ref(false)
  const installJob = computed(() => {
    const selection = installation.value
    return selection
      ? vpn.state?.jobs.find(
          (job) =>
            job.kind === 'core' &&
            (selection.jobId
              ? job.id === selection.jobId
              : !selection.previousJobs.includes(job.id)),
        )
      : undefined
  })
  watch(
    () => installJob.value?.id,
    (id) => {
      if (id && installation.value) installation.value.jobId = id
    },
    { flush: 'sync' },
  )
  const downloadingBranch = computed(() =>
    installation.value &&
    (installPending.value ||
      (installation.value.accepted && !installation.value.jobId) ||
      installJob.value?.status === 'running')
      ? installation.value.isAlpha
      : undefined,
  )

  const loadReleases = (force = false): Promise<boolean> => {
    if (releaseRequest) return releaseRequest
    if (releasesLoaded && !force) return Promise.resolve(true)
    releasesLoading.value = true
    releasesError.value = ''
    releaseRequest = (async () => {
      try {
        const result = await VPNGateRequest('core.releases', undefined)
        releases.value = result.releases
        releasesLoaded = true
        return true
      } catch (cause) {
        releasesError.value = cause instanceof Error ? cause.message : String(cause)
        return false
      } finally {
        releasesLoading.value = false
        releaseRequest = undefined
      }
    })()
    return releaseRequest
  }

  const installedCores = (isAlpha: boolean): InstalledCore[] =>
    [...(vpn.coreStatus?.installedVersions || [])]
      .filter((item) => isAlphaVersion(item.version) === isAlpha)
      .sort((left, right) => compareVersions(right.version, left.version))

  const localCore = (isAlpha: boolean): InstalledCore | undefined => {
    const status = vpn.coreStatus
    const installed = installedCores(isAlpha)
    if (status?.installed && isAlphaVersion(status.version) === isAlpha) {
      return (
        installed.find((item) => bareVersion(item.version) === bareVersion(status.version)) || {
          version: status.version,
          path: status.path,
          selected: true,
        }
      )
    }
    return installed.find((item) => item.selected) || installed[0]
  }
  const rollbackVersion = (isAlpha: boolean) => {
    const current = localCore(isAlpha)
    return current
      ? installedCores(isAlpha).find((item) => compareVersions(item.version, current.version) < 0)
          ?.version || ''
      : ''
  }
  const useVersion = async (version: string) => {
    const success = await vpn.run('core.use', { version })
    if (success) await vpn.refreshCore()
    return success
  }
  const rollbackCore = (isAlpha: boolean) => {
    const version = rollbackVersion(isAlpha)
    return version ? useVersion(version) : Promise.resolve(false)
  }
  const installCore = async (isAlpha: boolean, version: string) => {
    if (
      !version ||
      vpn.busy ||
      downloadingBranch.value !== undefined ||
      vpn.activeJobs.some((job) => job.kind === 'core')
    )
      return false
    installation.value = {
      isAlpha,
      version,
      previousJobs: vpn.state?.jobs.map((job) => job.id) || [],
      accepted: false,
    }
    installPending.value = true
    try {
      const success = await vpn.run('core.install', { version })
      if (!success) installation.value = undefined
      else if (installation.value) installation.value.accepted = true
      return success
    } finally {
      installPending.value = false
    }
  }
  const cancelInstall = () => {
    const job = installJob.value
    return job?.status === 'running'
      ? vpn.run('jobs.cancel', { id: job.id })
      : Promise.resolve(false)
  }

  return {
    releases,
    releasesLoading,
    releasesError,
    loadReleases,
    installedCores,
    localCore,
    rollbackVersion,
    rollbackCore,
    useVersion,
    installation,
    installJob,
    downloadingBranch,
    installCore,
    cancelInstall,
  }
})
