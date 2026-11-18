import { DefaultPluginHubSources } from '@/constant/app'
import { RequestProxyMode, WindowStartState } from '@/enums/app'
import { useEnvStore } from '@/stores'

export const migrateAppSettings = async (
  settings: App.AppSettings,
  save: () => Promise<string>,
) => {
  let needSync = false
  const managedPages = ['Overview', 'VPNGate', 'Settings']
  if (JSON.stringify(settings.pages) !== JSON.stringify(managedPages)) {
    settings.pages = managedPages
    needSync = true
  }
  for (const key of [
    'autoStartKernel',
    'autoRestartKernel',
    'closeKernelOnExit',
    'autoSetSystemProxy',
    'autoSetSystemDNS',
  ] as const) {
    if (settings[key]) {
      settings[key] = false
      needSync = true
    }
  }

  const { os } = useEnvStore().env

  if ('darwinSystemProxyServices' in settings) {
    settings.systemProxyServices = settings.darwinSystemProxyServices as string[]
    delete settings.darwinSystemProxyServices
    needSync = true
  }
  if (
    !settings.systemProxyServices ||
    (os === 'linux' && settings.systemProxyServices.join(',') === 'Ethernet,Wi-Fi')
  ) {
    settings.systemProxyServices = os === 'darwin' ? ['Ethernet', 'Wi-Fi'] : []
    needSync = true
  }
  if (settings.autoSetSystemDNS === undefined) {
    settings.autoSetSystemDNS = false
    needSync = true
  }
  if (settings.systemProxyDNS === undefined) {
    settings.systemProxyDNS = ''
    needSync = true
  }
  if (settings.systemDefaultDNS === undefined) {
    settings.systemDefaultDNS = ''
    needSync = true
  }
  if (!settings.requestProxyMode) {
    settings.requestProxyMode = RequestProxyMode.System
    needSync = true
  }
  if (settings.customProxy === undefined) {
    settings.customProxy = ''
    needSync = true
  }
  if (settings.githubDownloadAcceleration === undefined) {
    settings.githubDownloadAcceleration = false
    needSync = true
  }
  if (settings.githubDownloadMirror === undefined) {
    settings.githubDownloadMirror = ''
    needSync = true
  }
  if (!settings.plugins) {
    settings.plugins = {
      sources: DefaultPluginHubSources(),
    }
    needSync = true
  }
  for (const source of settings.plugins.sources) {
    const name = ['generic', 'gfc', 'gfs'].find(
      (name) =>
        source.url ===
        `https://raw.githubusercontent.com/GUI-for-Cores/Plugin-Hub/main/plugins/${name}.json`,
    )
    if (name) {
      source.url = `https://api.github.com/repos/GUI-for-Cores/Plugin-Hub/contents/plugins/${name}.json?ref=main`
      needSync = true
    }
  }
  if (settings.debugUsePointer === undefined) {
    settings.debugUsePointer = false
    needSync = true
  }
  if (settings.debugModalSideBySide === undefined) {
    settings.debugModalSideBySide = false
    needSync = true
  }
  if (settings.systemTitleBar === undefined) {
    settings.systemTitleBar = false
    needSync = true
  }
  if (typeof settings.windowStartState === 'number') {
    settings.windowStartState =
      settings.windowStartState === 2 ? WindowStartState.Minimised : WindowStartState.Normal
    needSync = true
  }
  for (const core of [settings.kernel.main, settings.kernel.alpha]) {
    const legacy = JSON.stringify(core)
    const migrated = legacy
      .replaceAll('$APP_BASE_PATH/$CORE_BASE_PATH', '$APP_DATA_PATH/sing-box')
      .replaceAll('$APP_BASE_PATH/data/', '$APP_DATA_PATH/')
    if (migrated !== legacy) {
      Object.assign(core, JSON.parse(migrated))
      needSync = true
    }
  }

  if (needSync) await save()
}
