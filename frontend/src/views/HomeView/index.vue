<script setup lang="ts">
import { computed, onActivated, onDeactivated, onUnmounted, ref, watch } from 'vue'

import { initWebsocket, destroyWebsocket } from '@/api/kernel'
import { useVPNGateStore } from '@/stores/vpngate'
import { canConnectVPNGate } from '@/utils/vpngate'
import { useVPNGateText } from '@/utils/vpngateText'

import VPNGateRepair from '@/components/_common/VPNGateRepair.vue'
import VPNGateStatus from '@/components/_common/VPNGateStatus.vue'

import ConnectionsController from './components/ConnectionsController.vue'
import LogsController from './components/LogsController.vue'
import OverView from './components/OverView.vue'
const text = useVPNGateText()
const vpn = useVPNGateStore()
const panel = ref('')
let active = false
const connected = computed(() => ['connected', 'direct'].includes(vpn.state?.runtime.status || ''))
watch(connected, (ready) => {
  destroyWebsocket()
  if (active && ready) initWebsocket()
})
onActivated(() => {
  active = true
  destroyWebsocket()
  if (connected.value) initWebsocket()
})
onDeactivated(() => {
  active = false
  panel.value = ''
  destroyWebsocket()
})
onUnmounted(destroyWebsocket)
const canConnect = computed(() => canConnectVPNGate(vpn.state, vpn.busy))
</script>
<template>
  <div class="overview-page flex flex-col gap-8 pb-16">
    <VPNGateStatus />
    <Card class="runtime-card">
      <div v-if="vpn.state" class="runtime-content">
        <div class="runtime-topline">
          <div class="runtime-identity">
            <strong class="runtime-state">{{ text(vpn.state.runtime.status) }}</strong>
            <span class="runtime-node" :title="vpn.selectedNode?.hostname">{{
              vpn.selectedNode
                ? `${vpn.selectedNode.country} · ${vpn.selectedNode.hostname}`
                : text('尚未选择节点 · No node selected')
            }}</span>
          </div>
          <div class="runtime-actions">
            <Button
              type="primary"
              size="small"
              :disabled="
                !!vpn.busy ||
                !canConnect ||
                vpn.state.runtime.status === 'starting' ||
                ['connected', 'direct'].includes(vpn.state.runtime.status)
              "
              :loading="vpn.busy === 'connect'"
              @click="vpn.run('connect', undefined)"
              >{{ text('连接 · Connect') }}</Button
            >
            <Button
              size="small"
              :disabled="!!vpn.busy || vpn.state.runtime.status === 'stopped'"
              :loading="vpn.busy === 'stop'"
              @click="vpn.run('stop', undefined)"
              >{{ text('停止 · Stop') }}</Button
            >
            <Button size="small" type="text" :loading="vpn.loading" @click="vpn.refresh">{{
              text('刷新 · Refresh')
            }}</Button>
          </div>
        </div>
        <div class="runtime-details">
          <span
            >{{ text('核心 · Core') }}:
            {{
              vpn.coreStatus?.version ||
              vpn.state.runtime.coreVersion ||
              text('未检测 · Not detected')
            }}</span
          >
          <span
            >{{ text('节点池 · Pool') }}: {{ vpn.state.nodes.length }}/{{
              vpn.state.settings.poolCapacity
            }}</span
          >
          <RouterLink to="/vpngate"
            ><Button size="small" type="link">{{ text('选择节点 · Nodes') }}</Button></RouterLink
          >
          <RouterLink to="/settings"
            ><Button size="small" type="link">{{ text('配置上游 · Upstream') }}</Button></RouterLink
          >
          <VPNGateRepair compact />
        </div>
        <div
          v-if="vpn.state.runtime.message"
          class="runtime-message select-text"
          :title="vpn.state.runtime.message"
        >
          {{ vpn.state.runtime.message }}
        </div>
      </div>
      <div v-else class="runtime-content">
        {{ text('正在读取运行状态 · Reading runtime state…') }}
      </div>
    </Card>
    <OverView :ready="connected" @connections="panel = connected ? 'connections' : ''" />
    <Card v-if="vpn.state" :title="text('最近任务 · Recent jobs')">
      <p
        v-for="job in vpn.state.jobs
          .filter((item) => item.status !== 'running')
          .slice(-5)
          .reverse()"
        :key="job.id"
      >
        {{ text(job.kind) }} · {{ text(job.status) }} · {{ job.completed }}/{{ job.total }} ·
        {{ job.message }}
      </p>
      <p v-if="!vpn.state.jobs.length">{{ text('暂无任务 · No jobs yet') }}</p>
    </Card>
    <Card v-if="connected" :title="text('运行监控 · Runtime monitor')">
      <div class="flex gap-8 my-8">
        <Button
          :type="panel === 'connections' ? 'primary' : 'normal'"
          @click="panel = panel === 'connections' ? '' : 'connections'"
          >{{ text('连接 · Connections') }}</Button
        >
        <Button
          :type="panel === 'logs' ? 'primary' : 'normal'"
          @click="panel = panel === 'logs' ? '' : 'logs'"
          >{{ text('日志 · Logs') }}</Button
        >
      </div>
      <div v-if="panel" style="height: 360px" class="overflow-auto">
        <ConnectionsController v-if="panel === 'connections'" /><LogsController v-else />
      </div>
    </Card>
  </div>
</template>

<style scoped lang="less">
.runtime-content {
  padding: 12px 4px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.runtime-topline {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}
.runtime-identity {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 12px;
}
.runtime-state {
  font-size: 22px;
  white-space: nowrap;
}
.runtime-node {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  opacity: 0.8;
}
.runtime-actions {
  display: flex;
  gap: 6px;
  flex-shrink: 0;
}
.runtime-details {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  font-size: 12px;
}
.runtime-message {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 11px;
  opacity: 0.7;
}
@media (max-width: 600px) {
  .runtime-identity {
    gap: 6px;
  }
  .runtime-state {
    font-size: 18px;
  }
  .runtime-topline {
    flex-wrap: wrap;
  }
  .runtime-details {
    gap: 8px;
  }
}
</style>
