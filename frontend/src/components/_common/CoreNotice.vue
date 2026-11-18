<script setup lang="ts">
import { useVPNGateStore } from '@/stores/vpngate'
import { useVPNGateText } from '@/utils/vpngateText'

const vpn = useVPNGateStore()
const text = useVPNGateText()
</script>

<template>
  <div v-if="vpn.coreStatus && !vpn.coreStatus.installed" class="core-notice" role="alert">
    <Icon icon="messageWarn" :size="20" aria-hidden="true" />
    <div class="core-notice-copy">
      <strong>{{ text('需要下载内核 · Core required') }}</strong>
      <p>
        {{ text('先配置上游，再下载 sing-box。 · Configure upstream, then download sing-box.') }}
      </p>
    </div>
    <RouterLink class="core-notice-action" :to="{ path: '/settings', query: { tab: 'core' } }">
      {{ text('下载内核 · Download core') }} →
    </RouterLink>
  </div>
</template>

<style scoped lang="less">
.core-notice {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 12px;
  margin: 8px 8px 0;
  padding: 10px 12px;
  border: 1px solid rgba(250, 173, 20, 0.4);
  border-radius: 8px;
  color: var(--color);
  background: rgba(250, 173, 20, 0.1);
  > * {
    flex-shrink: 0;
  }
}
.core-notice-copy {
  flex: 1;
  min-width: 200px;
  font-size: 12px;
  line-height: 1.5;
  p {
    margin: 2px 0 0;
    opacity: 0.8;
  }
}
.core-notice-action {
  color: var(--ui-accent);
  font-size: 12px;
  font-weight: 600;
  padding: 6px 0;
}
</style>
