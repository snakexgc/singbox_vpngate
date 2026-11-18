<script setup lang="ts">
import { computed } from 'vue'

import { useVPNGateStore } from '@/stores/vpngate'
import { useVPNGateText } from '@/utils/vpngateText'

import StateBadge from './StateBadge.vue'
import VPNGateRepair from './VPNGateRepair.vue'
const text = useVPNGateText()
const vpn = useVPNGateStore()
const hasError = computed(
  () => !!(vpn.error || vpn.state?.repair.needed || vpn.state?.runtime.status === 'error'),
)
const errorDetails = computed(() => [
  ...new Set([vpn.error, vpn.state?.repair.message, vpn.state?.runtime.message].filter(Boolean)),
])
const errorAnnouncement = computed(() =>
  errorDetails.value.map((detail) => text(detail || '')).join(' '),
)
const errorSummary = computed(() =>
  vpn.error
    ? text('操作失败 · Action failed')
    : vpn.state?.runtime.status === 'error'
      ? text('连接异常 · Connection error')
      : text('需要修复 · Repair needed'),
)
const noticeSummary = computed(() => {
  if (
    vpn.notice ===
    '正在以管理员身份重启，请在重启后重试原操作。 · Restarting as administrator. Retry your action after restart.'
  )
    return { label: text('正在重启 · Restarting'), tone: 'info' as const }
  if (
    vpn.notice === '修复完成：已停止连接并恢复本应用的网络设置，节点和设置已保留' ||
    vpn.notice ===
      '已停止连接并恢复本应用的网络设置，节点和配置已保留。 · Connection stopped and this application’s network settings restored. Nodes and settings retained.'
  )
    return { label: text('修复完成 · Repair complete'), tone: 'success' as const }
  return { label: text('操作反馈 · Update'), tone: 'info' as const }
})
</script>
<template>
  <div v-if="hasError || vpn.notice || vpn.activeJobs.length" class="status-stack">
    <div v-if="hasError" class="status-banner">
      <div class="status-copy">
        <div role="alert" aria-atomic="true" class="status-line">
          <StateBadge
            :tone="vpn.error || vpn.state?.runtime.status === 'error' ? 'danger' : 'warning'"
          >
            {{ errorSummary }}
          </StateBadge>
          <span class="status-sr-only">{{ errorAnnouncement }}</span>
        </div>
        <details v-if="errorDetails.length" class="ui-details status-details">
          <summary>{{ text('查看原因 · View details') }}</summary>
          <p v-for="detail in errorDetails" :key="detail" class="select-text">
            {{ text(detail || '') }}
          </p>
        </details>
      </div>
      <div class="status-actions">
        <VPNGateRepair
          v-if="vpn.state?.repair.needed || vpn.state?.runtime.status === 'error'"
          compact
        />
        <button type="button" class="status-button" :disabled="vpn.loading" @click="vpn.refresh">
          <Icon
            :icon="vpn.loading ? 'loading' : 'refresh'"
            :class="{ rotation: vpn.loading }"
            color="currentColor"
            aria-hidden="true"
          />
          {{ text('刷新 · Refresh') }}
        </button>
      </div>
    </div>

    <div v-if="vpn.notice" class="status-banner">
      <div class="status-copy">
        <div role="status" aria-atomic="true">
          <StateBadge :tone="noticeSummary.tone">{{ noticeSummary.label }}</StateBadge>
          <span class="status-sr-only">{{ text(vpn.notice) }}</span>
        </div>
        <details class="ui-details status-details">
          <summary>{{ text('查看详情 · View details') }}</summary>
          <p class="select-text">{{ text(vpn.notice) }}</p>
        </details>
      </div>
      <button
        type="button"
        class="status-button status-dismiss"
        :aria-label="text('关闭通知 · Dismiss notification')"
        @click="vpn.notice = ''"
      >
        <Icon icon="close" color="currentColor" aria-hidden="true" />
      </button>
    </div>

    <div v-for="job in vpn.activeJobs" :key="job.id" class="status-banner status-banner--job">
      <div class="status-copy">
        <div class="status-line">
          <strong>{{ text(job.kind) }}</strong>
          <StateBadge state="running">{{ text('进行中 · Running') }}</StateBadge>
          <span class="job-count">{{ job.completed }} / {{ job.total }}</span>
        </div>
        <Progress
          :percent="job.total ? (job.completed / job.total) * 100 : 0"
          :aria-label="text(job.kind)"
        />
        <details v-if="job.message" class="ui-details status-details">
          <summary>{{ text('任务详情 · Task details') }}</summary>
          <p class="select-text">{{ text(job.message) }}</p>
        </details>
      </div>
      <button
        type="button"
        class="status-button"
        :disabled="!!vpn.busy"
        @click="vpn.run('jobs.cancel', { id: job.id })"
      >
        {{ text('取消 · Cancel') }}
      </button>
    </div>
  </div>
</template>
<style scoped lang="less">
.status-stack {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.status-sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
.status-banner {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  border: 1px solid var(--ui-border);
  border-radius: 10px;
  color: var(--ui-text);
  background: var(--ui-surface);
  min-width: 0;
}
.status-copy {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.status-line {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  font-size: 12px;
  strong {
    font-weight: 500;
  }
}
.job-count {
  margin-left: auto;
  color: var(--ui-muted);
  font-variant-numeric: tabular-nums;
}
.status-details {
  padding: 0;
  border: 0;
  border-radius: 0;
  background: transparent;
  font-size: 12px;
  color: var(--ui-muted);
  > summary {
    padding: 0;
    color: var(--ui-muted);
    font-weight: 400;
  }
  &[open] > summary {
    padding: 0;
    border: 0;
    margin: 0;
  }
  p {
    margin: 8px 0 0;
    line-height: 1.6;
    overflow-wrap: anywhere;
    white-space: pre-wrap;
  }
}
.status-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
.status-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  gap: 5px;
  min-height: 32px;
  padding: 5px 10px;
  border: 1px solid var(--ui-border);
  border-radius: 6px;
  color: var(--ui-text);
  background: var(--ui-surface-soft);
  font: inherit;
  font-size: 12px;
  cursor: pointer;
  &:hover:not(:disabled) {
    color: var(--ui-accent);
    border-color: var(--ui-accent);
  }
  &:focus-visible {
    outline: 2px solid var(--ui-accent);
    outline-offset: 2px;
  }
  &:disabled {
    cursor: default;
    opacity: 0.5;
  }
}
.status-dismiss {
  min-width: 32px;
  padding: 5px;
  border-color: transparent;
  background: transparent;
}
@media (max-width: 550px) {
  .status-banner {
    flex-wrap: wrap;
  }
  .status-actions {
    width: 100%;
  }
}
</style>
