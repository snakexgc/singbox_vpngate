<script setup lang="ts">
import { ref } from 'vue'

import { useVPNGateStore } from '@/stores/vpngate'
import { routingOptions } from '@/utils/vpngate'
import { useVPNGateText } from '@/utils/vpngateText'

import type { RoutingPreset } from '@/types/vpngate'

const vpn = useVPNGateStore()
const text = useVPNGateText()
const pending = ref<RoutingPreset>()
const descriptions: Record<RoutingPreset, string> = {
  direct: '直接连接所有流量 · Directly connect all traffic',
  'bypass-cn': '按中国大陆域名规则直连 · Direct routing for mainland China domains',
  blacklist: '仅代理命中黑名单规则的流量 · Proxy only traffic matching blacklist rules',
  global: '默认通过 VPNGate 转发流量 · Route traffic through VPNGate by default',
}
const choose = async (value: RoutingPreset) => {
  if (!vpn.state || vpn.busy || vpn.state.settings.routingPreset === value) return
  pending.value = value
  try {
    await vpn.saveSettings({ ...vpn.state.settings, routingPreset: value })
  } finally {
    pending.value = undefined
  }
}
</script>

<template>
  <div class="routing-mode-switch" role="group" :aria-label="text('路由模式 · Routing mode')">
    <button
      v-for="option in routingOptions"
      :key="option.value"
      type="button"
      class="routing-mode"
      :class="{ 'is-selected': vpn.state?.settings.routingPreset === option.value }"
      :aria-pressed="vpn.state?.settings.routingPreset === option.value"
      :aria-busy="pending === option.value || undefined"
      :disabled="!vpn.state || !!vpn.busy"
      @click="choose(option.value)"
    >
      <span class="mode-copy">
        <strong>{{ text(option.label) }}</strong>
        <span class="mode-description">{{ text(descriptions[option.value]) }}</span>
      </span>
      <Icon
        v-if="pending === option.value || vpn.state?.settings.routingPreset === option.value"
        :icon="pending === option.value ? 'loading' : 'selected'"
        :class="{ rotation: pending === option.value }"
        class="mode-marker"
        :size="20"
        color="currentColor"
        aria-hidden="true"
      />
    </button>
  </div>
</template>

<style scoped lang="less">
.routing-mode-switch {
  display: grid;
  flex: 1;
  grid-auto-rows: minmax(min-content, 1fr);
  gap: 7px;
}
.routing-mode {
  position: relative;
  min-width: 0;
  min-height: 48px;
  padding: 4px 25px 4px 8px;
  border: 0;
  border-radius: 8px;
  color: var(--card-color);
  background: var(--card-bg);
  text-align: start;
  font: inherit;
  font-size: 12px;
  transition: background 150ms;
  &:hover:not(:disabled) {
    background: var(--card-hover-bg);
  }
  &:active:not(:disabled) {
    background: var(--card-active-bg);
  }
  &:disabled {
    opacity: 0.6;
  }
}
.mode-copy {
  display: grid;
  strong {
    font-size: 16px;
    line-height: 1.35;
  }
}
.mode-description {
  line-height: 1.5;
}
.mode-marker {
  position: absolute;
  bottom: 5px;
  inset-inline-end: 7px;
  color: var(--ui-accent);
}
body[feature-no-rounded='true'] .routing-mode {
  border-radius: 0;
}
</style>
