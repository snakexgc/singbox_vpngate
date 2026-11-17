<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import { useVPNGateStore } from '@/stores/vpngate'
import { filterVPNGateNodes } from '@/utils/vpngate'
import { useVPNGateText } from '@/utils/vpngateText'

import VPNGateStatus from '@/components/_common/VPNGateStatus.vue'

import type { Column } from '@/components/Table/index.vue'
const text = useVPNGateText()
const vpn = useVPNGateStore()
const search = ref('')
const status = ref('')
const selected = ref<string[]>([])
const nodes = computed(() => filterVPNGateNodes(vpn.state?.nodes || [], search.value, status.value))
const statusOptions = computed(() => [
  { label: text('全部状态 · All states'), value: '' },
  ...['untested', 'testing', 'available', 'failed', 'canceled'].map((value) => ({
    label: text(value),
    value,
  })),
])
const columns = computed<Column[]>(() => [
  { title: text('选择 · Select'), key: 'select' },
  {
    title: text('国家 · Country'),
    key: 'country',
    sort: (a, b) => a.country.localeCompare(b.country),
  },
  {
    title: text('节点 · Host'),
    key: 'hostname',
    sort: (a, b) => a.hostname.localeCompare(b.hostname),
  },
  { title: 'IP / TCP', key: 'endpoint' },
  { title: text('状态 · State'), key: 'status', sort: (a, b) => a.status.localeCompare(b.status) },
  {
    title: text('延迟 · Latency'),
    key: 'latencyMs',
    sort: (a, b) => (a.latencyMs ?? Infinity) - (b.latencyMs ?? Infinity),
  },
  { title: text('握手 · Handshake'), key: 'handshakeMs' },
  { title: text('操作 · Actions'), key: 'actions' },
])
watch(
  () => vpn.state?.nodes,
  (nodes) => {
    const ids = new Set(nodes?.map((node) => node.id))
    selected.value = selected.value.filter((id) => ids.has(id))
  },
)
const toggleAll = () => {
  selected.value = nodes.value.every((node) => selected.value.includes(node.id))
    ? selected.value.filter((id) => !nodes.value.some((node) => node.id === id))
    : [...new Set([...selected.value, ...nodes.value.map((node) => node.id)])]
}
</script>
<template>
  <div class="flex flex-col gap-12 pb-16">
    <VPNGateStatus />
    <Card :title="text('VPNGate 节点池 · Node pool')">
      <template #extra
        >{{ vpn.state?.nodes.length || 0 }} /
        {{ vpn.state?.settings.poolCapacity || 200 }}</template
      >
      <div class="flex flex-wrap gap-8 my-8">
        <Input v-model="search" :placeholder="text('搜索国家 / 主机 / IP · Search')" clearable />
        <Select v-model="status" :options="statusOptions" />
        <Button
          :disabled="!!vpn.busy || vpn.activeJobs.some((job) => job.kind === 'refresh')"
          :loading="vpn.busy === 'nodes.refresh'"
          @click="vpn.run('nodes.refresh', { manual: true })"
          >{{ text('刷新节点 · Refresh') }}</Button
        >
        <Button
          :disabled="
            !!vpn.busy || !selected.length || vpn.activeJobs.some((job) => job.kind === 'test')
          "
          @click="vpn.run('tests.start', { ids: [...selected] })"
          >{{ text('测试所选 · Test selected') }} ({{ selected.length }})</Button
        >
        <Button
          :disabled="
            !!vpn.busy ||
            !vpn.state?.nodes.length ||
            vpn.activeJobs.some((job) => job.kind === 'test')
          "
          @click="vpn.run('tests.start', { ids: [] })"
          >{{ text('测试全部 · Test all') }}</Button
        >
        <Button
          :disabled="!!vpn.busy || !vpn.state?.nodes.some((node) => node.status === 'failed')"
          @click="vpn.run('nodes.deleteFailed', undefined)"
          >{{ text('删除失败 · Delete failed') }}</Button
        >
      </div>
      <Button size="small" class="mb-8" :disabled="!nodes.length" @click="toggleAll">{{
        text('全选 / 取消当前筛选 · Toggle visible')
      }}</Button>
      <Table :columns="columns" :data-source="nodes">
        <template #select="{ record }"
          ><CheckBox
            v-model="selected"
            :options="[
              {
                label: selected.includes(record.id) ? '✓' : text('选择 · Select'),
                value: record.id,
              },
            ]"
        /></template>
        <template #endpoint="{ record }"
          >{{ record.ip }}:{{ record.port }} · {{ record.protocol }}</template
        >
        <template #status="{ record }"
          ><span :title="record.error"
            >{{ text(record.status)
            }}<span v-if="record.failureKind"> · {{ record.failureKind }}</span></span
          ></template
        >
        <template #latencyMs="{ record }">{{
          record.latencyMs == null ? '—' : `${record.latencyMs} ms`
        }}</template>
        <template #handshakeMs="{ record }">{{
          record.handshakeMs == null ? '—' : `${record.handshakeMs} ms`
        }}</template>
        <template #actions="{ record }">
          <Button
            size="small"
            :type="vpn.state?.selectedNodeId === record.id ? 'primary' : 'normal'"
            :disabled="!!vpn.busy || record.status === 'testing'"
            @click="vpn.run('nodes.use', { id: record.id })"
            >{{
              vpn.state?.selectedNodeId === record.id
                ? text('已选择 · Selected')
                : text('使用 · Use')
            }}</Button
          >
          <Button
            size="small"
            type="text"
            :disabled="!!vpn.busy || record.id === vpn.state?.runtime.connectedNodeId"
            @click="vpn.run('nodes.delete', { id: record.id })"
            >{{ text('删除 · Delete') }}</Button
          >
        </template>
      </Table>
      <p v-if="!nodes.length" class="my-24 text-center">
        {{
          text(
            '无匹配节点。先在设置中填写外部上游并刷新节点。 · No matching nodes. Configure the upstream and refresh.',
          )
        }}
      </p>
      <p class="text-12">
        {{
          text(
            '刷新和探测独立于生产连接。点击列标题排序。 · Probes do not change the active connection. Click column headers to sort.',
          )
        }}
      </p>
    </Card>
  </div>
</template>
