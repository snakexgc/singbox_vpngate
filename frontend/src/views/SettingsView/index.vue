<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useVPNGateStore } from '@/stores/vpngate'
import { useVPNGateText } from '@/utils/vpngateText'

import VPNGateRepair from '@/components/_common/VPNGateRepair.vue'
import VPNGateStatus from '@/components/_common/VPNGateStatus.vue'

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
  { value: 'managed', label: 'VPNGate' },
  { value: 'core', label: text('内核 · Core') },
  { value: 'appearance', label: text('外观 · Appearance') },
])
</script>
<template>
  <div class="settings-view flex flex-col gap-12 h-full">
    <div class="settings-status"><VPNGateStatus /></div>
    <div class="settings-toolbar">
      <div class="settings-tabs">
        <Button
          v-for="tab in tabs"
          :key="tab.value"
          :type="active === tab.value ? 'primary' : 'normal'"
          @click="selectTab(tab.value)"
          >{{ tab.label }}</Button
        >
      </div>
      <div v-if="active === 'managed'" class="settings-actions">
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
          >{{ text('保存设置 · Save settings') }}</Button
        >
        <Button :disabled="!managedSettings?.dirty" @click="managedSettings?.reset()">{{
          text('撤销修改 · Reset draft')
        }}</Button>
        <span v-if="managedSettings?.saved" role="status">{{ text('已保存 · Saved') }}</span>
        <span v-else-if="managedSettings?.dirty" role="status">{{
          text('尚未保存 · Unsaved changes')
        }}</span>
      </div>
    </div>
    <div class="settings-content">
      <template v-if="active === 'managed'">
        <Card :title="text('修复软件 · Repair application')"><VPNGateRepair /></Card>
        <VPNGateSettings ref="managedSettings" />
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
.settings-status {
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  gap: 12px;
  &:empty {
    display: none;
  }
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
.settings-actions {
  margin-left: auto;
  span {
    font-size: 12px;
    white-space: nowrap;
  }
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
</style>
