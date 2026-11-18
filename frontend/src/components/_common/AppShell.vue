<script setup lang="ts">
import { useRoute } from 'vue-router'

import { useAppSettingsStore, useAppStore } from '@/stores'

import AdminNotice from './AdminNotice.vue'
import CoreNotice from './CoreNotice.vue'
import ModalTabs from './ModalTabs.vue'
import NavigationBar from './NavigationBar.vue'
import TitleBar from './TitleBar.vue'
import VPNGateStatus from './VPNGateStatus.vue'

const route = useRoute()
const appStore = useAppStore()
const appSettings = useAppSettingsStore()
</script>

<template>
  <div class="flex flex-1 min-h-0">
    <div
      class="app-main flex flex-col w-full min-h-0"
      :class="{ 'app-main--overview': route.name === 'Overview' }"
    >
      <TitleBar v-if="!appSettings.systemTitleBarOnStartup" />
      <div class="app-content flex-1 min-h-0 overflow-hidden flex flex-col p-8">
        <div class="app-header">
          <NavigationBar />
          <AdminNotice />
          <CoreNotice />
        </div>
        <div class="app-global-status"><VPNGateStatus /></div>
        <div
          :class="{ 'app-page--overview': route.name === 'Overview' }"
          class="app-page flex flex-1 min-h-0 flex-col overflow-y-auto mt-8 px-8"
        >
          <RouterView #="{ Component }">
            <KeepAlive>
              <component :is="Component" />
            </KeepAlive>
          </RouterView>
        </div>
      </div>
    </div>
    <div v-show="appStore.modalSplitActive" class="app-modal-panel flex flex-col">
      <ModalTabs />
      <div id="app-modal-content" class="relative flex-1 overflow-hidden" />
    </div>
  </div>
</template>

<style lang="less" scoped>
.app-header {
  flex-shrink: 0;
}
.app-modal-panel {
  width: 50%;
}
</style>
