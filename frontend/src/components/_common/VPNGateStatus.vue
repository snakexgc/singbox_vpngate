<script setup lang="ts">
import { useVPNGateStore } from '@/stores/vpngate'
import { useVPNGateText } from '@/utils/vpngateText'

import VPNGateRepair from './VPNGateRepair.vue'
const text = useVPNGateText()
const vpn = useVPNGateStore()
</script>
<template>
  <div
    v-if="vpn.error || vpn.state?.repair.needed || vpn.state?.runtime.status === 'error'"
    class="status-banner"
    role="alert"
  >
    <div class="status-copy">
      <strong class="select-text text-12">{{
        text(
          vpn.error ||
            vpn.state?.runtime.message ||
            text('网络状态需要修复 · Network state needs repair'),
        )
      }}</strong>
      <VPNGateRepair compact />
    </div>
    <Button size="small" :loading="vpn.loading" @click="vpn.refresh">{{
      text('刷新 · Refresh')
    }}</Button>
  </div>
  <div v-if="vpn.notice" class="status-banner text-12 select-text" role="status">
    {{ text(vpn.notice) }}
  </div>
  <div v-for="job in vpn.activeJobs" :key="job.id" class="status-banner">
    <div class="status-copy">
      <div class="status-message" :title="job.message">
        {{ text(job.kind) }} · {{ job.completed }}/{{ job.total }} · {{ job.message }}
      </div>
      <Progress :percent="job.total ? (job.completed / job.total) * 100 : 0" />
    </div>
    <Button size="small" :disabled="!!vpn.busy" @click="vpn.run('jobs.cancel', { id: job.id })">{{
      text('取消 · Cancel')
    }}</Button>
  </div>
</template>
<style scoped lang="less">
.status-banner {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  border-radius: 8px;
  color: var(--card-color);
  background: var(--card-bg);
  min-width: 0;
}
.status-copy {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.status-message {
  display: block;
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
}
.status-detail {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 11px;
  opacity: 0.7;
}
.status-banner > .gui-button {
  flex-shrink: 0;
}
</style>
