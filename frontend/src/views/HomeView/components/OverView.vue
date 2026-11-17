<script setup lang="ts">
import { ref, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'

import { onMemory, onTraffic, onConnections } from '@/api/kernel'
import { useVPNGateStore } from '@/stores/vpngate'
import { formatBytes } from '@/utils'
import { routingOptions } from '@/utils/vpngate'
import { useVPNGateText } from '@/utils/vpngateText'

import type { RoutingPreset } from '@/types/vpngate'
const props = defineProps<{ ready: boolean }>()
const emit = defineEmits<{ connections: [] }>()
const { t } = useI18n()
const text = useVPNGateText()
const vpn = useVPNGateStore()
const trafficHistory = ref<[number[], number[]]>([[], []])
const statistics = ref({
  upload: 0,
  download: 0,
  downloadTotal: 0,
  uploadTotal: 0,
  connections: 0,
  inuse: 0,
})
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
const mode = (value: string) => {
  if (vpn.state && !vpn.busy)
    void vpn.saveSettings({ ...vpn.state.settings, routingPreset: value as RoutingPreset })
}
onUnmounted(() => {
  offMemory()
  offTraffic()
  offConnections()
})
</script>
<template>
  <div class="home-overview">
    <div class="home-stats stats-grid gap-12">
      <Card
        :title="t('home.overview.realtimeTraffic')"
        class="home-stat-card home-stat-card--realtime"
      >
        <div class="home-stat-card__value flex gap-8 py-8 text-12">
          <span class="metric metric--upload"
            >↑ {{ props.ready ? formatBytes(statistics.upload) + '/s' : '—' }}</span
          ><span class="metric metric--download"
            >↓ {{ props.ready ? formatBytes(statistics.download) + '/s' : '—' }}</span
          >
        </div>
      </Card>
      <Card
        :title="text('本次运行流量 · Traffic this run')"
        class="home-stat-card home-stat-card--total"
      >
        <div class="home-stat-card__value flex gap-8 py-8 text-12">
          <span class="metric metric--upload"
            >↑ {{ props.ready ? formatBytes(statistics.uploadTotal) : '—' }}</span
          ><span class="metric metric--download"
            >↓ {{ props.ready ? formatBytes(statistics.downloadTotal) : '—' }}</span
          >
        </div>
      </Card>
      <Card
        :title="t('home.overview.connections')"
        class="home-stat-card home-stat-card--connections cursor-pointer"
        @click="emit('connections')"
        ><div class="home-stat-card__value py-8 text-12">
          {{ props.ready ? statistics.connections : '—' }}
        </div></Card
      >
      <Card :title="t('home.overview.memory')" class="home-stat-card home-stat-card--memory"
        ><div class="home-stat-card__value py-8 text-12">
          {{ props.ready ? formatBytes(statistics.inuse) : '—' }}
        </div></Card
      >
    </div>
    <div class="home-dashboard dashboard-grid gap-12 mt-12">
      <Card :title="t('home.overview.traffic')" class="traffic-panel">
        <TrafficChart
          :height="170"
          :series="trafficHistory"
          :legend="[t('home.overview.transmit'), t('home.overview.receive')]"
        />
      </Card>
      <div class="mode-panel flex flex-col gap-8">
        <Card
          v-for="option in routingOptions"
          :key="option.value"
          :title="text(option.label)"
          :selected="vpn.state?.settings.routingPreset === option.value"
          class="home-mode-card cursor-pointer"
          @click="mode(option.value)"
        >
          <div class="py-2 text-12">
            {{
              option.value === 'direct'
                ? text('业务直连 · Direct business traffic')
                : option.value === 'bypass-cn'
                  ? text('大陆域名外走 VPN · VPN outside mainland domains')
                  : option.value === 'blacklist'
                    ? text('黑名单走 VPN · VPN for blacklisted traffic')
                    : text('全部业务走 VPN · VPN for all business traffic')
            }}
          </div>
        </Card>
      </div>
    </div>
  </div>
</template>
<style scoped lang="less">
.stats-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
}
.dashboard-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.8fr) minmax(190px, 1fr);
}
@media (max-width: 900px) {
  .mode-panel {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    align-content: start;
  }
}
@media (max-width: 700px) {
  .stats-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .dashboard-grid {
    grid-template-columns: minmax(0, 1fr);
  }
  .mode-panel {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
