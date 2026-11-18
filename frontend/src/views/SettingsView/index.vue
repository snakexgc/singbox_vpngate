<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useVPNGateStore } from '@/stores/vpngate'
import { useVPNGateText } from '@/utils/vpngateText'

import StateBadge from '@/components/_common/StateBadge.vue'
import VPNGateRepair from '@/components/_common/VPNGateRepair.vue'

import PersonalizationSettings from './components/components/PersonalizationSettings.vue'
import CoreSettings from './components/CoreSettings.vue'
import VPNGateSettings from './components/VPNGateSettings.vue'
const text = useVPNGateText()
const vpn = useVPNGateStore()
const active = ref('managed')
const route = useRoute()
const router = useRouter()
const selectTab = (tab: string) => {
  active.value = tab
  void router.replace({ query: { ...route.query, tab } })
}
watch(
  () => route.query.tab,
  (tab) => {
    if (typeof tab === 'string' && ['managed', 'core', 'appearance'].includes(tab))
      active.value = tab
  },
  { immediate: true },
)
const managedSettings = ref<InstanceType<typeof VPNGateSettings>>()
const tabs = computed(() => [
  { value: 'managed', label: text('连接 · Connection') },
  { value: 'core', label: text('内核 · Core') },
  { value: 'appearance', label: text('外观 · Appearance') },
])
</script>
<template>
  <div class="settings-view flex flex-col gap-12 h-full">
    <div class="page-heading">
      <h1 class="page-title">{{ text('设置 · Settings') }}</h1>
    </div>
    <div class="settings-toolbar">
      <nav class="settings-tabs" :aria-label="text('设置分类 · Settings categories')">
        <button
          v-for="tab in tabs"
          :key="tab.value"
          type="button"
          :class="{ 'is-active': active === tab.value }"
          :aria-pressed="active === tab.value"
          @click="selectTab(tab.value)"
        >
          {{ tab.label }}
        </button>
      </nav>
      <div v-if="active === 'managed'" class="settings-actions">
        <span class="save-status" role="status" aria-live="polite">
          <StateBadge v-if="managedSettings?.saved" tone="success">{{
            text('已保存 · Saved')
          }}</StateBadge>
          <StateBadge v-else-if="managedSettings?.dirty" tone="warning">{{
            text('未保存 · Unsaved')
          }}</StateBadge>
        </span>
        <Button :disabled="!managedSettings?.dirty" @click="managedSettings?.reset()">{{
          text('撤销 · Reset')
        }}</Button>
        <Button
          type="primary"
          :loading="vpn.busy === 'settings.update'"
          :disabled="
            !!vpn.busy ||
            !vpn.state ||
            !managedSettings ||
            vpn.activeJobs.some((job) => job.kind === 'core')
          "
          @click="managedSettings?.save()"
          >{{ text('保存 · Save') }}</Button
        >
      </div>
    </div>
    <div class="settings-content">
      <template v-if="active === 'managed'">
        <VPNGateSettings ref="managedSettings" />
        <Card :title="text('维护与修复 · Maintenance')">
          <VPNGateRepair />
        </Card>
      </template>
      <CoreSettings v-else-if="active === 'core'" />
      <PersonalizationSettings v-else />
    </div>
  </div>
</template>

<style scoped lang="less">
.settings-view {
  min-height: 0;
  overflow: hidden;
}
.settings-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  flex-shrink: 0;
  gap: 8px 12px;
}
.settings-tabs,
.settings-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.settings-tabs {
  padding: 4px;
  gap: 4px;
  border: 1px solid var(--ui-border);
  border-radius: 10px;
  background: var(--ui-surface-soft);
  button {
    min-height: 32px;
    padding: 6px 16px;
    border: 0;
    border-radius: 6px;
    color: var(--ui-muted);
    background: transparent;
    font: inherit;
    font-size: 13px;
    cursor: pointer;
    &:hover {
      color: var(--ui-text);
      background: var(--ui-surface);
    }
    &:focus-visible {
      outline: 2px solid var(--ui-accent);
      outline-offset: 2px;
    }
    &.is-active {
      color: var(--ui-text);
      background: var(--ui-surface);
      box-shadow: 0 1px 3px rgb(0 0 0 / 6%);
      font-weight: 600;
    }
  }
}
.settings-actions {
  margin-left: auto;
}
.save-status {
  display: inline-flex;
  align-items: center;
  min-height: 28px;
}
.settings-content {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 12px;
  min-height: 0;
  overflow-y: auto;
  padding-bottom: 16px;
  scrollbar-gutter: stable;
  > * {
    flex-shrink: 0;
  }
}
@media (max-width: 640px) {
  .settings-actions {
    width: 100%;
    margin-left: 0;
    justify-content: flex-end;
  }
  .save-status {
    margin-right: auto;
  }
}
</style>
