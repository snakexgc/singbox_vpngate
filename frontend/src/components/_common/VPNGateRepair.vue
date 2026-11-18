<script setup lang="ts">
import { useId } from 'vue'

import { useVPNGateStore } from '@/stores/vpngate'
import { useVPNGateText } from '@/utils/vpngateText'

const { compact = false } = defineProps<{ compact?: boolean }>()
const vpn = useVPNGateStore()
const text = useVPNGateText()
const scopeId = useId()
const repairScope =
  '停止内核和任务，恢复本应用修改的网络设置。节点与配置会保留。 · Stops the core and tasks and restores network settings changed by this app. Keeps nodes and configuration.'
</script>

<template>
  <div class="repair-controls" :class="{ 'repair-controls--compact': compact }">
    <div v-if="!compact" class="repair-copy">
      <p :id="scopeId">{{ text(repairScope) }}</p>
      <p
        v-if="vpn.state?.repair.needed && vpn.state.repair.message"
        class="repair-reason select-text"
      >
        {{ text(vpn.state.repair.message) }}
      </p>
    </div>
    <Button
      :size="compact ? 'small' : 'default'"
      :icon="compact ? undefined : 'rollback'"
      :title="compact ? text(repairScope) : undefined"
      :aria-describedby="compact ? undefined : scopeId"
      :disabled="!!vpn.busy && vpn.busy !== 'repair'"
      :loading="vpn.busy === 'repair'"
      @click="vpn.run('repair', undefined)"
    >
      {{
        compact
          ? text('修复软件 · Repair application')
          : text('恢复应用网络设置 · Restore app network settings')
      }}
    </Button>
  </div>
</template>

<style scoped>
.repair-controls {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px 16px;
  min-width: 0;
}
.repair-controls:not(.repair-controls--compact) {
  padding-top: 2px;
}
.repair-copy {
  flex: 1 1 300px;
  min-width: 0;
  font-size: 12px;
  line-height: 1.65;
  color: var(--ui-muted);
}
.repair-copy p {
  max-width: 70ch;
  margin: 0;
  overflow-wrap: anywhere;
}
.repair-copy .repair-reason {
  margin-top: 4px;
  color: var(--ui-warning);
}
</style>
