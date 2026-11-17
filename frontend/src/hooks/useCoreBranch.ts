import { computed, watch } from 'vue'

import { BrowserOpenURL, OpenDir } from '@/bridge'
import { Branch } from '@/enums/app'
import { useAppSettingsStore } from '@/stores/appSettings'
import { useCoreManagerStore } from '@/stores/coreManager'
import { useVPNGateStore } from '@/stores/vpngate'
import { confirm, message } from '@/utils/interaction'

const displayVersion = (version = '') => version.replace(/^v/, '')
const branchOf = (version: string) =>
  version.split('+')[0]?.includes('-') ? Branch.Alpha : Branch.Main

// Branch cards activate an already installed version; release discovery is never needed here.
export const chooseCoreBranch = async (isAlpha: boolean) => {
  const manager = useCoreManagerStore()
  const vpn = useVPNGateStore()
  const settings = useAppSettingsStore()
  if (!vpn.coreStatus) await vpn.refreshCore()
  const installed = manager.installedCores(isAlpha)[0]
  if (!installed) {
    // The compatible bundled core is already selected, even without managed installations.
    if (
      vpn.coreStatus?.installed &&
      branchOf(vpn.coreStatus.version) === (isAlpha ? Branch.Alpha : Branch.Main)
    ) {
      settings.app.kernel.branch = isAlpha ? Branch.Alpha : Branch.Main
      return true
    }
    message.error('请先下载该分支的内核。 · Download this branch before selecting it.')
    return false
  }
  if (!(await manager.useVersion(installed.version))) {
    if (vpn.error) message.error(vpn.error)
    return false
  }
  settings.app.kernel.branch = isAlpha ? Branch.Alpha : Branch.Main
  return true
}

export const useCoreBranch = (isAlpha = false) => {
  const manager = useCoreManagerStore()
  const vpn = useVPNGateStore()
  const settings = useAppSettingsStore()
  const local = computed(() => manager.localCore(isAlpha))
  const localVersion = computed(() => displayVersion(local.value?.version))
  const remoteVersion = computed(() =>
    displayVersion(manager.releases.find((release) => release.prerelease === isAlpha)?.version),
  )
  const downloading = computed(() => manager.downloadingBranch === isAlpha)
  const downloadProgress = computed(() => {
    const job = manager.installJob
    if (!job) return 'Loading'
    return job.total > 0
      ? `${job.message} ${Math.min(100, (job.completed / job.total) * 100).toFixed(2)}%`
      : job.message
  })
  const cancelDownload = computed(() =>
    downloading.value && manager.installJob?.status === 'running'
      ? () => manager.cancelInstall()
      : undefined,
  )
  const refreshLocalVersion = async (showTips = false) => {
    await vpn.refreshCore()
    if (showTips && vpn.coreStatus?.error) message.error(vpn.coreStatus.error)
  }
  const refreshRemoteVersion = async (showTips = false) => {
    const success = await manager.loadReleases(true)
    if (!success && showTips) message.error(manager.releasesError)
  }
  const downloadCore = async () => {
    if (!(await manager.installCore(isAlpha, remoteVersion.value)) && vpn.error)
      message.error(vpn.error)
  }
  const rollbackCore = async () => {
    try {
      await confirm('common.warning', 'settings.kernel.rollback')
    } catch {
      return
    }
    if (await manager.rollbackCore(isAlpha)) message.success('common.success')
    else if (vpn.error) message.error(vpn.error)
  }
  const openFileLocation = async () => {
    const path = local.value?.path || vpn.coreStatus?.path
    if (!path) return
    const directory = path.replace(/[\\/][^\\/]+$/, '')
    try {
      await OpenDir(directory === path ? '.' : directory)
    } catch (cause) {
      message.error(cause)
    }
  }

  watch(
    () => vpn.coreStatus?.version,
    (version) => {
      if (version && vpn.coreStatus?.installed) settings.app.kernel.branch = branchOf(version)
    },
    { immediate: true },
  )
  watch(
    () => manager.installJob?.status,
    (status, previous) => {
      if (manager.installation?.isAlpha !== isAlpha || status === previous) return
      if (status === 'completed') message.success('common.success')
      else if (status === 'failed') message.error(manager.installJob?.message)
    },
  )

  void refreshLocalVersion()
  void manager.loadReleases(true)

  return {
    // Installing selects the checked executable while stopped; no extra restart is needed.
    restartable: computed(() => false),
    updatable: computed(() => !!remoteVersion.value && localVersion.value !== remoteVersion.value),
    grantable: computed(() => false),
    rollbackable: computed(() => !!manager.rollbackVersion(isAlpha)),
    versionDetail: computed(() =>
      localVersion.value ? `sing-box version ${localVersion.value}` : '',
    ),
    localVersion,
    localVersionLoading: computed(() => vpn.coreStatusLoading),
    remoteVersion,
    remoteVersionLoading: computed(() => manager.releasesLoading),
    downloading,
    downloadProgress,
    cancelDownload,
    refreshLocalVersion,
    refreshRemoteVersion,
    downloadCore,
    restartCore: async () => undefined,
    rollbackCore,
    grantCorePermission: async () => undefined,
    openReleasePage: () =>
      BrowserOpenURL(
        isAlpha
          ? 'https://github.com/SagerNet/sing-box/releases'
          : 'https://github.com/SagerNet/sing-box/releases/latest',
      ),
    openFileLocation,
  }
}
