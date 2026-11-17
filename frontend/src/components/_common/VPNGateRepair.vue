<script setup lang="ts">
import { useVPNGateStore } from '@/stores/vpngate'
import { useVPNGateText } from '@/utils/vpngateText'

const { compact = false } = defineProps<{ compact?: boolean }>()
const vpn = useVPNGateStore()
const text = useVPNGateText()
const repairScope =
  '停止当前连接并恢复本应用的网络设置，保留节点与配置。 · Stops the current connection and restores this application’s network settings. Keeps nodes and settings.'
</script>

<template>
  <div class="repair-controls">
    <p v-if="!compact" class="text-12">{{ text(repairScope) }}</p>
    <p
      v-if="!compact && vpn.state?.repair.needed && vpn.state.repair.message"
      class="text-12 select-text"
    >
      {{ text(vpn.state.repair.message) }}
    </p>
    <div class="flex flex-wrap gap-8">
      <Button
        size="small"
        :title="text(repairScope)"
        :disabled="!!vpn.busy"
        :loading="vpn.busy === 'repair'"
        @click="vpn.run('repair', undefined)"
        >{{ text('修复软件 · Repair application') }}</Button
      >
    </div>
  </div>
</template>

<style scoped>
.repair-controls {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
}
</style>
