import i18n from '@/lang'
import { useVPNGateStore } from '@/stores/vpngate'
import { exitApp, reloadApp } from '@/utils'

import { getVPNGateText } from './vpngateLabels'
type Command = { label: string; cmd: string; desc?: string; handler: () => unknown }
export const getCommands = (): Command[] => {
  const text = (value: string) => getVPNGateText(value, i18n.global.locale.value)
  const vpn = useVPNGateStore()
  return [
    {
      label: text('修复软件 · Repair application'),
      cmd: 'VPNGate Repair',
      handler: () => vpn.run('repair', undefined),
    },
    {
      label: text('VPNGate：连接 · VPNGate: Connect'),
      cmd: 'VPNGate Connect',
      handler: () => vpn.run('connect', undefined),
    },
    {
      label: text('VPNGate：停止 · VPNGate: Stop'),
      cmd: 'VPNGate Stop',
      handler: () => vpn.run('stop', undefined),
    },
    {
      label: text('VPNGate：刷新节点 · VPNGate: Refresh nodes'),
      cmd: 'VPNGate Refresh',
      handler: () => vpn.run('nodes.refresh', { manual: true }),
    },
    {
      label: text('VPNGate：更新规则 · VPNGate: Update rules'),
      cmd: 'VPNGate Rules',
      handler: () => vpn.run('rules.update', undefined),
    },
    { label: text('重新加载窗口 · Reload window'), cmd: 'Reload', handler: reloadApp },
    {
      label: text('退出软件（停止连接） · Exit application (stop connection)'),
      cmd: 'Exit',
      handler: exitApp,
    },
  ]
}
