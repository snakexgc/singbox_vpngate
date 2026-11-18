<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  state?: string
  tone?: 'success' | 'warning' | 'danger' | 'info' | 'neutral'
}>()

const tone = computed(() => {
  if (props.tone) return props.tone
  if (['connected', 'available', 'completed', 'ready'].includes(props.state || '')) return 'success'
  if (['error', 'failed'].includes(props.state || '')) return 'danger'
  if (
    ['starting', 'testing', 'running', 'offline', 'protected', 'degraded'].includes(
      props.state || '',
    )
  )
    return 'warning'
  if (props.state === 'direct' || props.state === 'connecting') return 'info'
  return 'neutral'
})
</script>

<template>
  <span class="state-badge" :class="`state-badge--${tone}`">
    <span class="state-badge__dot" aria-hidden="true" />
    <slot />
  </span>
</template>

<style scoped lang="less">
.state-badge {
  --badge-color: var(--ui-muted);
  --badge-bg: var(--ui-surface-soft);
  display: inline-flex;
  align-items: center;
  gap: 6px;
  width: fit-content;
  flex-shrink: 0;
  padding: 4px 9px;
  border-radius: 999px;
  color: var(--badge-color);
  background: var(--badge-bg);
  font-size: 12px;
  font-weight: 600;
  line-height: 1.5;
  white-space: nowrap;
}
.state-badge__dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}
.state-badge--success {
  --badge-color: var(--ui-success);
  --badge-bg: var(--ui-success-bg);
}
.state-badge--warning {
  --badge-color: var(--ui-warning);
  --badge-bg: var(--ui-warning-bg);
}
.state-badge--danger {
  --badge-color: var(--ui-danger);
  --badge-bg: var(--ui-danger-bg);
}
.state-badge--info {
  --badge-color: var(--ui-info);
  --badge-bg: var(--ui-info-bg);
}
</style>
