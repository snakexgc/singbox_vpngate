<script setup lang="ts">
import { ref, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { onMemory, onTraffic, onConnections } from '@/api/kernel'
import { formatBytes } from '@/utils'

import RoutingModeSwitch from '@/components/_common/RoutingModeSwitch.vue'

const props = defineProps<{ ready: boolean; session: string }>()
const emit = defineEmits<{ connections: [] }>()
const { t } = useI18n()
const trafficHistory = ref<[number[], number[]]>([[], []])
const emptyStatistics = () => ({
  upload: 0,
  download: 0,
  downloadTotal: 0,
  uploadTotal: 0,
  connections: 0,
  inuse: 0,
})
const statistics = ref(emptyStatistics())
watch(
  () => props.session,
  () => {
    statistics.value = emptyStatistics()
    trafficHistory.value = [[], []]
  },
)
const offMemory = onMemory((data) => {
  statistics.value.inuse = data.inuse
})
const offTraffic = onTraffic((data) => {
  statistics.value.upload = data.up
  statistics.value.download = data.down
  trafficHistory.value[0].push(data.up)
  trafficHistory.value[1].push(data.down)
  if (trafficHistory.value[0].length > 60) {
    trafficHistory.value[0].shift()
    trafficHistory.value[1].shift()
  }
})
const offConnections = onConnections((data) => {
  statistics.value.downloadTotal = data.downloadTotal
  statistics.value.uploadTotal = data.uploadTotal
  statistics.value.connections = data.connections?.length || 0
})
onUnmounted(() => {
  offMemory()
  offTraffic()
  offConnections()
})
</script>

<template>
  <div class="home-overview">
    <div class="statistics">
      <Card :title="t('home.overview.realtimeTraffic')" class="statistics-card">
        <div class="detail">
          <span v-if="ready">↑ {{ formatBytes(statistics.upload) }}/s</span>
          <span v-if="ready">↓ {{ formatBytes(statistics.download) }}/s</span>
          <span v-else>—</span>
        </div>
      </Card>
      <Card :title="t('home.overview.totalTraffic')" class="statistics-card">
        <div class="detail">
          <span v-if="ready">↑ {{ formatBytes(statistics.uploadTotal) }}</span>
          <span v-if="ready">↓ {{ formatBytes(statistics.downloadTotal) }}</span>
          <span v-else>—</span>
        </div>
      </Card>
      <Card :title="t('home.overview.connections')" class="statistics-card">
        <div class="detail">{{ ready ? statistics.connections : '—' }}</div>
        <button
          type="button"
          class="connections-target rounded-8"
          aria-haspopup="dialog"
          :aria-label="t('home.overview.connections')"
          :disabled="!ready"
          @click="emit('connections')"
        />
      </Card>
      <Card :title="t('home.overview.memory')" class="statistics-card">
        <div class="detail">{{ ready ? formatBytes(statistics.inuse) : '—' }}</div>
      </Card>
    </div>

    <div class="dashboard-row">
      <section class="traffic-panel" aria-labelledby="overview-traffic-label">
        <div class="dashboard-heading">
          <h2 id="overview-traffic-label">{{ t('home.overview.traffic') }}</h2>
          <slot name="traffic-status" />
        </div>
        <TrafficChart
          :series="trafficHistory"
          :legend="[t('home.overview.transmit'), t('home.overview.receive')]"
        />
      </section>
      <section class="mode-panel" aria-labelledby="overview-mode-label">
        <div class="dashboard-heading">
          <h2 id="overview-mode-label">{{ t('kernel.mode') }}</h2>
        </div>
        <RoutingModeSwitch />
      </section>
    </div>
  </div>
</template>

<style scoped lang="less">
.home-overview {
  display: flex;
  flex: 1;
  flex-direction: column;
  margin-top: 20px;
}
.statistics {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
}
.detail {
  display: flex;
  flex-wrap: wrap;
  column-gap: 5px;
  padding: 3px 0;
  font-size: 12px;
  line-height: 2;
  font-variant-numeric: tabular-nums;
  span {
    white-space: nowrap;
  }
}
.connections-target {
  position: absolute;
  inset: 0;
  border: 0;
  background: transparent;
}
.dashboard-row {
  display: grid;
  flex: 1;
  grid-template-columns: minmax(0, 1fr) clamp(192px, 25%, 264px);
  gap: 8px;
  > section {
    display: flex;
    min-width: 0;
    flex-direction: column;
  }
}
.dashboard-heading {
  display: flex;
  align-items: center;
  gap: 9px;
  min-height: 48px;
  h2 {
    margin: 0;
    color: var(--card-color);
    font-size: 16px;
  }
}
@media (max-width: 540px) {
  .statistics {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 460px) {
  .dashboard-row {
    grid-template-columns: 1fr;
  }
}
</style>
