<script setup lang="ts">
import { computed, onActivated, onDeactivated, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { destroyWebsocket, initWebsocket } from '@/api/kernel'
import { useVPNGateStore } from '@/stores/vpngate'
import { canConnectVPNGate, isVPNGateCoreRunning } from '@/utils/vpngate'
import { useVPNGateText } from '@/utils/vpngateText'

import StateBadge from '@/components/_common/StateBadge.vue'

import ConnectionsController from './components/ConnectionsController.vue'
import OverView from './components/OverView.vue'

const { t } = useI18n()
const router = useRouter()
const text = useVPNGateText()
const vpn = useVPNGateStore()
const showConnections = ref(false)
const pageActive = ref(false)

const runtime = computed(() => vpn.state?.runtime)
const status = computed(() => runtime.value?.status)
const running = computed(() => isVPNGateCoreRunning(status.value))
const settings = computed(() => vpn.state?.settings)
const busy = computed(
  () =>
    !!vpn.busy || status.value === 'starting' || vpn.activeJobs.some((job) => job.kind === 'core'),
)
const canStart = computed(
  () =>
    vpn.coreStatus?.installed !== false &&
    !busy.value &&
    (running.value || canConnectVPNGate(vpn.state, vpn.busy)),
)
const startLabel = computed(() =>
  running.value ? t('home.overview.restart') : text('启动内核 · Start core'),
)
const connecting = computed(() => {
  if (status.value === 'connecting') return true
  if (!settings.value || settings.value.routingPreset === 'direct') return false
  if (vpn.busy === 'nodes.use' && (running.value || status.value === 'starting')) return true
  return !!vpn.selectedNode && (vpn.busy === 'connect' || status.value === 'starting')
})
const vpnBadge = computed(() => {
  if (connecting.value) {
    return { label: text('VPNGate 连接中 · VPNGate connecting'), tone: 'info' } as const
  }
  switch (status.value) {
    case 'connected':
      return { label: text('VPNGate 已连接 · VPNGate connected'), tone: 'success' } as const
    case 'direct':
      return { label: text('VPNGate 未启用 · VPNGate bypassed'), tone: 'neutral' } as const
    case 'starting':
      return { label: text('内核启动中 · Core starting'), tone: 'info' } as const
    case 'offline':
      if (!vpn.selectedNode) {
        return {
          label: text('VPNGate 未选节点 · VPNGate: No node selected'),
          tone: 'danger',
          to: '/vpngate',
        } as const
      }
      return {
        label: text('VPNGate 无法连接 · VPNGate unavailable'),
        tone: 'danger',
        to: '/logs',
      } as const
    case 'error':
    case 'protected':
    case 'degraded':
      return {
        label: text('VPNGate 无法连接 · VPNGate unavailable'),
        tone: 'danger',
        to: '/logs',
      } as const
    case 'stopped':
      return { label: text('VPNGate 已停止 · VPNGate stopped'), tone: 'neutral' } as const
    default:
      return undefined
  }
})

type CaptureSetting = 'systemProxyEnabled' | 'tunEnabled'
const captureSwitches = [
  { key: 'systemProxyEnabled', label: 'home.overview.systemProxy' },
  { key: 'tunEnabled', label: 'home.overview.tunMode' },
] satisfies { key: CaptureSetting; label: string }[]
const pending = ref<CaptureSetting>()
const tunPending = computed(
  () => running.value && settings.value?.tunEnabled && !runtime.value?.tunEnabled,
)
const setCapture = async (key: CaptureSetting, enabled: boolean) => {
  const current = settings.value
  if (!current || busy.value || current[key] === enabled) return
  pending.value = key
  try {
    await vpn.saveSettings({ ...current, [key]: enabled })
  } finally {
    pending.value = undefined
  }
}

const telemetrySession = computed(() =>
  running.value ? runtime.value?.startedAt || 'running' : '',
)
watch([pageActive, telemetrySession], ([active, session]) => {
  destroyWebsocket()
  if (active && session) initWebsocket()
  else showConnections.value = false
})
onActivated(() => {
  pageActive.value = true
})
onDeactivated(() => {
  pageActive.value = false
})
onUnmounted(destroyWebsocket)
</script>

<template>
  <div class="overview-page">
    <div
      class="kernel-status rounded-8"
      role="group"
      :aria-label="text('内核控制 · Core controls')"
    >
      <Button
        type="text"
        size="small"
        icon="settings"
        class="overview-action"
        :aria-label="text('连接设置 · Connection settings')"
        :title="text('连接设置 · Connection settings')"
        @click="router.push('/settings')"
      />
      <Switch
        v-for="item in captureSwitches"
        :key="item.key"
        :model-value="!!settings?.[item.key]"
        :disabled="!settings || busy"
        :aria-label="t(item.label)"
        :aria-busy="pending === item.key || undefined"
        :title="
          item.key === 'tunEnabled' && tunPending
            ? text(
                'TUN 已请求开启，当前未接管流量 · TUN was requested and is not capturing traffic yet',
              )
            : t(item.label)
        "
        :tabindex="settings && !busy ? 0 : -1"
        size="small"
        border="square"
        class="capture-switch"
        @update:model-value="setCapture(item.key, $event)"
        @keydown.space.prevent="setCapture(item.key, !settings?.[item.key])"
        @keydown.enter.prevent="setCapture(item.key, !settings?.[item.key])"
      >
        {{ t(item.label) }}
      </Switch>
      <Button
        type="text"
        size="small"
        icon="log"
        class="overview-action ml-auto"
        :aria-label="text('查看日志 · View logs')"
        :title="text('查看日志 · View logs')"
        @click="router.push('/logs')"
      />
      <Button
        type="text"
        size="small"
        class="overview-action"
        :icon="running ? 'restart' : 'play'"
        :aria-label="startLabel"
        :title="startLabel"
        :disabled="!canStart"
        :loading="vpn.busy === 'connect' || status === 'starting'"
        @click="vpn.run('connect', undefined)"
      />
      <Button
        type="text"
        size="small"
        icon="stop"
        class="overview-action"
        :aria-label="t('home.overview.stop')"
        :title="t('home.overview.stop')"
        :disabled="busy || !status || status === 'stopped'"
        :loading="vpn.busy === 'stop'"
        @click="vpn.run('stop', undefined)"
      />
    </div>

    <OverView :ready="running" :session="telemetrySession" @connections="showConnections = true">
      <template #traffic-status>
        <span role="status" aria-atomic="true" class="traffic-status">
          <RouterLink v-if="vpnBadge?.to" :to="vpnBadge.to" class="traffic-status-link">
            <StateBadge :tone="vpnBadge.tone">{{ vpnBadge.label }}</StateBadge>
          </RouterLink>
          <StateBadge v-else-if="vpnBadge" :tone="vpnBadge.tone">{{ vpnBadge.label }}</StateBadge>
        </span>
      </template>
    </OverView>

    <div v-if="tunPending && !runtime?.requiresAdmin" class="overview-hint" role="status">
      {{ text('TUN 尚未接管流量。 · TUN is not capturing traffic yet.') }}
      <RouterLink to="/logs">{{ text('查看日志 · View logs') }}</RouterLink>
    </div>
    <Modal
      v-model:open="showConnections"
      :title="t('home.overview.connections')"
      :submit="false"
      mask-closable
      cancel-text="common.close"
      width="90"
      height="90"
    >
      <ConnectionsController v-if="showConnections && running" />
    </Modal>
  </div>
</template>

<style scoped lang="less">
.overview-page {
  display: flex;
  flex: 1;
  flex-direction: column;
  padding-bottom: 4px;
}
.kernel-status {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  background: var(--card-bg);
}
.overview-action {
  min-width: 32px;
  min-height: 28px;
  color: var(--ui-accent);
}
.overview-action :deep(svg path) {
  fill: currentColor;
}
.capture-switch {
  margin-inline-end: 4px;
  &.on :deep(.dot) {
    left: 4px;
  }
  &.off :deep(.dot) {
    left: calc(100% - 8px);
  }
  &.on :deep(.slot) {
    margin-left: 12px;
    margin-right: 8px;
  }
  &.off :deep(.slot) {
    margin-left: 8px;
    margin-right: 12px;
  }
  &[aria-disabled='true'] {
    cursor: default;
    opacity: 0.7;
  }
}
.traffic-status-link {
  display: inline-flex;
  border-radius: 999px;
  &:hover {
    text-decoration: underline;
  }
}
.overview-hint {
  display: flex;
  justify-content: center;
  flex-wrap: wrap;
  gap: 2px 8px;
  color: var(--card-color);
  font-size: 12px;
  a {
    color: var(--ui-accent);
    &:hover {
      text-decoration: underline;
    }
  }
}
</style>
