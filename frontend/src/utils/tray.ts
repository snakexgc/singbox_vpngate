import { EventsOn, EventsOff, ShowMainWindow, UpdateTrayAndMenus } from '@/bridge'
import { OS } from '@/enums/app'
import i18n from '@/lang'
import { useAppSettingsStore, useEnvStore } from '@/stores'
import { useVPNGateStore } from '@/stores/vpngate'
import { debounce, exitApp, APP_TITLE, APP_VERSION } from '@/utils'

import { canConnectVPNGate } from './vpngate'
import { getVPNGateText } from './vpngateLabels'

const withEvents = (menus: App.MenuItem[]) => {
  const handlers: Record<string, () => unknown> = {}
  EventsOff('onMenuItemClick')
  EventsOn('onMenuItemClick', (id) => handlers[id]?.())
  return menus.map((menu, index) => {
    if (!menu.event) return menu
    const id = `vpngate_${index}`
    handlers[id] = menu.event as () => unknown
    return { ...menu, event: id }
  })
}
export const updateTrayAndMenus = debounce(async () => {
  const text = (value: string) => getVPNGateText(value, i18n.global.locale.value)
  const vpn = useVPNGateStore()
  const env = useEnvStore()
  const settings = useAppSettingsStore()
  const linux = env.env.os === OS.Linux
  const icon = `data/.cache/${linux ? 'imgs' : 'icons'}/tray_normal_${settings.themeMode}${linux ? '.png' : '.ico'}`
  const menus: App.MenuItem[] = [
    { type: 'item', text: text('显示 VPNGate · Show VPNGate'), event: ShowMainWindow },
    { type: 'item', text: `VPNGate: ${text(vpn.state?.runtime.status || '不可用 · Unavailable')}` },
    {
      type: 'item',
      text: text('启动内核 · Start core'),
      hidden: vpn.coreStatus?.installed === false || !canConnectVPNGate(vpn.state, vpn.busy),
      event: () => vpn.run('connect', undefined),
    },
    {
      type: 'item',
      text: text('停止 · Stop'),
      hidden: !vpn.state || vpn.state.runtime.status === 'stopped' || !!vpn.busy,
      event: () => vpn.run('stop', undefined),
    },
    { type: 'separator' },
    {
      type: 'item',
      text: text('退出软件（停止连接） · Exit application (stop connection)'),
      event: exitApp,
    },
  ]
  await UpdateTrayAndMenus(
    { icon, title: APP_TITLE, tooltip: `${APP_TITLE} ${APP_VERSION}` },
    withEvents(menus) as any,
  )
}, 500)
