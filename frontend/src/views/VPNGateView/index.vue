<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useVPNGateStore } from '@/stores/vpngate'
import {
  filterVPNGateNodes,
  formatVPNGateEndpoint,
  formatVPNGateNodeCountry,
  formatVPNGateRegion,
} from '@/utils/vpngate'
import { useVPNGateText } from '@/utils/vpngateText'

import StateBadge from '@/components/_common/StateBadge.vue'

import type { Column } from '@/components/Table/index.vue'
import type { VPNGateNode } from '@/types/vpngate'

const text = useVPNGateText()
const { locale } = useI18n()
const vpn = useVPNGateStore()
const search = ref('')
const status = ref('')
const selected = ref<string[]>([])
const backfillingEgress = ref(false)
const nodes = computed(() => filterVPNGateNodes(vpn.state?.nodes || [], search.value, status.value))
const egressCacheTTL = 7 * 24 * 60 * 60 * 1000
const egressIsFresh = (node: Pick<VPNGateNode, 'egressIP' | 'egressCheckedAt'>) => {
  const checkedAt = Date.parse(node.egressCheckedAt || '')
  const now = Date.now()
  return !!node.egressIP && checkedAt <= now && now - checkedAt < egressCacheTTL
}
const egressBackfillIds = computed(() =>
  nodes.value
    .filter((node) => node.status === 'available' && !egressIsFresh(node))
    .map((node) => node.id),
)
const allSelected = computed(
  () => nodes.value.length > 0 && nodes.value.every((node) => selected.value.includes(node.id)),
)
const someSelected = computed(() => nodes.value.some((node) => selected.value.includes(node.id)))
const poolSummaryStates = ['available', 'failed', 'untested'] as const
const nodeCounts = computed(() => {
  const counts = { available: 0, failed: 0, untested: 0, testing: 0, canceled: 0 }
  for (const node of vpn.state?.nodes || []) counts[node.status]++
  return counts
})
const testing = computed(() => vpn.activeJobs.some((job) => job.kind === 'test'))
const backfillEgress = async () => {
  const ids = egressBackfillIds.value
  if (!ids.length || vpn.busy || testing.value) return
  backfillingEgress.value = true
  try {
    await vpn.run('tests.start', { ids })
  } finally {
    backfillingEgress.value = false
  }
}
const nodeStatusText = (state: string) => {
  if (state === 'untested') return text('待测试 · Untested')
  if (state === 'failed') return text('失效 · Failed')
  return text(state)
}
const statusOptions = computed(() => [
  { label: text('全部状态 · All states'), value: '' },
  ...[...poolSummaryStates, 'testing', 'canceled'].map((value) => ({
    label: nodeStatusText(value),
    value,
  })),
])
const nodeCountry = (node: Recordable) => formatVPNGateNodeCountry(node, locale.value)
const columns = computed<Column[]>(() => [
  { title: '', key: 'select' },
  {
    title: text('国家 · Country'),
    key: 'egressCountry',
    sort: (a, b) => nodeCountry(a).localeCompare(nodeCountry(b), locale.value),
  },
  {
    title: text('地区 · Region'),
    key: 'egressRegion',
    minWidth: '130px',
    sort: (a, b) =>
      formatVPNGateRegion(a.egressRegion, a.egressCity).localeCompare(
        formatVPNGateRegion(b.egressRegion, b.egressCity),
      ),
  },
  {
    title: 'IP',
    key: 'ip',
    minWidth: '180px',
    sort: (a, b) => a.ip.localeCompare(b.ip, undefined, { numeric: true }),
  },
  { title: text('状态 · State'), key: 'status', sort: (a, b) => a.status.localeCompare(b.status) },
  {
    title: text('延迟 · Latency'),
    key: 'latencyMs',
    sort: (a, b) => (a.latencyMs ?? Infinity) - (b.latencyMs ?? Infinity),
  },
  { title: text('操作 · Actions'), key: 'actions', align: 'right' },
])
watch(
  () => vpn.state?.nodes,
  (currentNodes) => {
    const ids = new Set(currentNodes?.map((node) => node.id))
    selected.value = selected.value.filter((id) => ids.has(id))
  },
)
const toggleAll = () => {
  selected.value = allSelected.value
    ? selected.value.filter((id) => !nodes.value.some((node) => node.id === id))
    : [...new Set([...selected.value, ...nodes.value.map((node) => node.id)])]
}
const latencyTone = (latency: number | null) =>
  latency == null ? 'neutral' : latency < 300 ? 'success' : latency < 1000 ? 'warning' : 'danger'
const egressCacheTitle = (checkedAt?: string) => {
  if (!checkedAt || Number.isNaN(Date.parse(checkedAt))) return undefined
  return `${text('出口查询时间 · Egress checked')}: ${new Date(checkedAt).toLocaleString(locale.value)} · ${text('缓存 7 天 · Cached for 7 days')}`
}
const clearFilters = () => {
  search.value = ''
  status.value = ''
}
</script>

<template>
  <div class="nodes-page">
    <div class="page-heading">
      <div>
        <h1 class="page-title">{{ text('节点 · Nodes') }}</h1>
        <p class="page-subtitle">
          {{ text('选择出口，独立测速 · Choose an exit and test connectivity') }}
        </p>
      </div>
      <div class="pool-summary">
        <StateBadge v-for="state in poolSummaryStates" :key="state" :state="state"
          >{{ nodeCounts[state] }} {{ nodeStatusText(state) }}</StateBadge
        >
        <span
          >{{ vpn.state?.nodes.length || 0 }} / {{ vpn.state?.settings.poolCapacity || 200 }}</span
        >
      </div>
    </div>

    <section class="node-pool" :aria-label="text('节点池 · Node pool')">
      <div class="node-toolbar">
        <label class="node-search">
          <Icon icon="search" :size="16" aria-hidden="true" />
          <input
            v-model="search"
            type="search"
            :aria-label="text('搜索国家、地区、主机或 IP · Search country, region, host or IP')"
            :placeholder="text('搜索国家、地区、主机或 IP · Search country, region, host or IP')"
          />
        </label>
        <select
          v-model="status"
          class="ui-select"
          :aria-label="text('筛选节点状态 · Filter node state')"
        >
          <option v-for="option in statusOptions" :key="option.value" :value="option.value">
            {{ option.label }}
          </option>
        </select>
        <div class="node-toolbar-actions">
          <Button
            icon="refresh"
            :disabled="!!vpn.busy || vpn.activeJobs.some((job) => job.kind === 'refresh')"
            :loading="vpn.busy === 'nodes.refresh'"
            @click="vpn.run('nodes.refresh', { manual: true })"
            >{{ text('获取节点 · Fetch nodes') }}</Button
          >
          <Button
            v-if="egressBackfillIds.length"
            icon="speedTest"
            :title="
              text(
                '测试当前结果中缺少有效出口缓存的可用节点 · Retest available nodes without a current egress measurement',
              )
            "
            :disabled="!!vpn.busy || testing"
            :loading="backfillingEgress"
            @click="backfillEgress"
            >{{ text('补测出口 · Retest egress') }} ({{ egressBackfillIds.length }})</Button
          >
          <Button
            type="primary"
            icon="speedTest"
            :disabled="!!vpn.busy || !vpn.state?.nodes.length || testing"
            :loading="vpn.busy === 'tests.start' && !backfillingEgress"
            @click="vpn.run('tests.start', { ids: [...selected] })"
            >{{
              selected.length
                ? `${text('测试所选 · Test selected')} (${selected.length})`
                : text('测试全部 · Test all')
            }}</Button
          >
        </div>
      </div>
      <div class="selection-toolbar">
        <label class="select-visible"
          ><input
            type="checkbox"
            :checked="allSelected"
            :indeterminate="someSelected && !allSelected"
            :disabled="!nodes.length"
            @change="toggleAll"
          />{{ text('全选当前结果 · Select visible') }}</label
        >
        <span v-if="selected.length" class="selection-count"
          >{{ text('已选 · Selected') }} {{ selected.length
          }}<button type="button" class="clear-selection" @click="selected = []">
            {{ text('清空 · Clear') }}
          </button></span
        >
        <span v-else class="results-count">{{ nodes.length }} {{ text('个结果 · results') }}</span>
        <Button
          size="small"
          type="text"
          icon="clear"
          :disabled="!!vpn.busy || !nodeCounts.failed"
          @click="vpn.run('nodes.deleteFailed', undefined)"
          >{{ text('清理失败节点 · Clear failed') }}</Button
        >
      </div>

      <Table v-if="nodes.length" :columns="columns" :data-source="nodes" class="node-table">
        <template #select="{ record }"
          ><input
            v-model="selected"
            type="checkbox"
            :value="record.id"
            :aria-label="`${text('选择节点 · Select node')} ${record.hostname}`"
        /></template>
        <template #egressCountry="{ record }">
          <span class="node-country">{{ nodeCountry(record) || '—' }}</span>
        </template>
        <template #egressRegion="{ record }">
          <span class="node-region">{{
            formatVPNGateRegion(record.egressRegion, record.egressCity) || '—'
          }}</span>
        </template>
        <template #ip="{ record }">
          <div class="node-identity" :title="`${text('主机 · Host')}: ${record.hostname}`">
            <div
              class="node-address"
              role="group"
              :aria-label="text('VPNGate 官方 IP · Official VPNGate IP')"
            >
              <span class="node-address-label">{{ text('官方 · Official') }}</span>
              <span class="node-host">{{ formatVPNGateEndpoint(record.ip, record.port) }}</span>
              <span class="protocol-label">{{ record.protocol }}</span>
            </div>
            <div
              class="node-address"
              role="group"
              :aria-label="text('出口 IP · Egress IP')"
              :title="egressCacheTitle(record.egressCheckedAt)"
            >
              <span class="node-address-label">{{ text('出口 · Egress') }}</span>
              <span class="node-endpoint">{{ record.egressIP || '—' }}</span>
              <span v-if="record.egressIP && !egressIsFresh(record)" class="node-measurement-state">
                {{ text('出口缓存已过期 · Egress cache expired') }}
              </span>
              <details v-if="record.egressError" class="node-diagnostic">
                <summary>{{ text('查询失败 · Lookup failed') }}</summary>
                <div class="select-text">{{ record.egressError }}</div>
              </details>
              <span v-else-if="!record.egressIP" class="node-measurement-state">{{
                record.status === 'testing'
                  ? text('检测中 · Measuring')
                  : record.status === 'failed'
                    ? text('未获取出口 · Egress unavailable')
                    : record.status === 'available'
                      ? text('待补测出口 · Egress retest needed')
                      : text('待检测出口 · Egress not measured')
              }}</span>
            </div>
          </div>
          <StateBadge
            v-if="
              record.id === vpn.state?.runtime.connectedNodeId &&
              ['connected', 'starting'].includes(vpn.state?.runtime.status || '')
            "
            state="connected"
            class="node-active-connection"
            >{{ text('当前连接 · Active connection') }}</StateBadge
          >
        </template>
        <template #status="{ record }">
          <div class="node-status">
            <StateBadge :state="record.status">{{ nodeStatusText(record.status) }}</StateBadge>
            <details v-if="record.error || record.failureKind" class="node-diagnostic">
              <summary>{{ text('原因 · Details') }}</summary>
              <div class="select-text">
                <strong v-if="record.failureKind">{{ record.failureKind }}</strong>
                <p v-if="record.error">{{ record.error }}</p>
              </div>
            </details>
          </div>
        </template>
        <template #latencyMs="{ record }">
          <div class="node-latency" :class="`node-latency--${latencyTone(record.latencyMs)}`">
            {{ record.latencyMs == null ? '—' : `${record.latencyMs} ms` }}
          </div>
          <span v-if="record.handshakeMs != null" class="handshake"
            >{{ text('握手 · Handshake') }} {{ record.handshakeMs }} ms</span
          >
        </template>
        <template #actions="{ record }">
          <div class="node-actions">
            <Button
              size="small"
              :type="vpn.state?.selectedNodeId === record.id ? 'text' : 'normal'"
              :class="{ 'node-current': vpn.state?.selectedNodeId === record.id }"
              :icon="vpn.state?.selectedNodeId === record.id ? 'selected' : undefined"
              :disabled="
                !!vpn.busy || record.status === 'testing' || vpn.state?.selectedNodeId === record.id
              "
              @click="vpn.run('nodes.use', { id: record.id })"
              >{{
                vpn.state?.selectedNodeId === record.id
                  ? text('已选 · Selected')
                  : text('使用 · Use')
              }}</Button
            >
            <Button
              size="small"
              type="text"
              icon="delete"
              :aria-label="`${text('删除节点 · Delete node')} ${record.hostname}`"
              :title="text('删除节点 · Delete node')"
              :disabled="!!vpn.busy || record.id === vpn.state?.runtime.connectedNodeId"
              @click="vpn.run('nodes.delete', { id: record.id })"
            />
          </div>
        </template>
      </Table>
      <div v-else class="node-empty">
        <Icon
          :icon="vpn.state?.nodes.length ? 'search' : 'subscriptions'"
          :size="32"
          aria-hidden="true"
        />
        <strong>{{
          vpn.state?.nodes.length
            ? text('没有匹配的节点 · No matching nodes')
            : text('节点池还是空的 · Your node pool is empty')
        }}</strong>
        <p>
          {{
            vpn.state?.nodes.length
              ? text('尝试其他关键词或状态筛选 · Try another search or state filter')
              : text('配置上游后，点击“获取节点” · Configure the upstream, then fetch nodes')
          }}
        </p>
        <button v-if="search || status" type="button" class="empty-action" @click="clearFilters">
          {{ text('清除筛选 · Clear filters') }}
        </button>
        <RouterLink v-else to="/settings" class="ui-link"
          >{{ text('配置上游 · Configure upstream') }} →</RouterLink
        >
      </div>
      <div class="node-pool-footer">
        <Icon icon="messageInfo" :size="14" aria-hidden="true" />
        <span>
          {{
            text(
              '测速前显示 VPN Gate 官方国家，获取出口信息后显示实测位置。出口信息缓存 7 天。 · Shows the VPN Gate country until the egress location is measured. Egress details are cached for 7 days.',
            )
          }}
          {{
            text(
              '测速不影响当前连接；点击列标题排序 · Tests keep the active connection intact. Click headers to sort.',
            )
          }}
        </span>
      </div>
    </section>
  </div>
</template>

<style scoped lang="less">
.nodes-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding-bottom: 16px;
}
.pool-summary {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  > span:last-child:not(.state-badge) {
    color: var(--ui-muted);
    font-size: 12px;
    margin-left: 4px;
  }
}
.node-pool {
  min-width: 0;
  border: 1px solid var(--ui-border);
  border-radius: 12px;
  background: var(--ui-surface);
  overflow: hidden;
}
.node-toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  padding: 16px;
}
.node-search {
  flex: 1;
  min-width: 200px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 10px;
  border: 1px solid var(--ui-border);
  border-radius: 6px;
  color: var(--ui-muted);
  &:focus-within {
    outline: 2px solid var(--ui-accent);
    outline-offset: 2px;
  }
  input {
    width: 100%;
    min-height: 34px;
    min-width: 0;
    border: 0;
    outline: none;
    color: var(--ui-text);
    background: transparent;
    font-size: 12px;
  }
}
.node-toolbar-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-left: auto;
}
.selection-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  padding: 0 16px 12px;
  color: var(--ui-muted);
  font-size: 12px;
  > .gui-button {
    margin-left: auto;
  }
}
.select-visible {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
}
.selection-count {
  color: var(--ui-accent);
}
.clear-selection {
  border: 0;
  padding: 2px 6px;
  color: var(--ui-accent);
  background: transparent;
  font: inherit;
  text-decoration: underline;
}
.node-table {
  :deep(table) {
    min-width: 850px;
  }
  :deep(th) {
    padding: 10px 12px;
    background: var(--ui-surface-soft);
  }
  :deep(td) {
    padding: 13px 12px;
  }
  :deep(tr:has(.node-current)) {
    background: var(--ui-accent-bg);
  }
  :deep(td:first-child) {
    width: 24px;
  }
}
.node-country {
  font-size: 13px;
  font-weight: 500;
}
.node-identity {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
}
.node-address {
  display: flex;
  align-items: baseline;
  gap: 8px;
}
.node-address-label {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--ui-muted);
}
.node-active-connection {
  margin-top: 4px;
}
.node-host {
  max-width: 240px;
  overflow-wrap: anywhere;
  white-space: normal;
  font-size: 13px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.node-region {
  display: inline-block;
  max-width: 180px;
  white-space: normal;
  overflow-wrap: anywhere;
  font-size: 12px;
}
.node-measurement-state {
  font-size: 11px;
  color: var(--ui-muted);
}
.node-endpoint {
  max-width: 240px;
  overflow-wrap: anywhere;
  white-space: normal;
  font-size: 11px;
  color: var(--ui-muted);
  font-variant-numeric: tabular-nums;
}
.protocol-label {
  margin-left: 4px;
  font-size: 10px;
  text-transform: uppercase;
}
.node-status {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
}
.node-diagnostic {
  max-width: 280px;
  font-size: 11px;
  color: var(--ui-muted);
  summary {
    cursor: pointer;
  }
  > div {
    padding: 8px 0 0;
    white-space: normal;
    overflow-wrap: anywhere;
  }
  p {
    margin: 4px 0 0;
  }
}
.node-latency {
  font-size: 13px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.node-latency--success {
  color: var(--ui-success);
}
.node-latency--warning {
  color: var(--ui-warning);
}
.node-latency--danger {
  color: var(--ui-danger);
}
.node-latency--neutral,
.handshake {
  color: var(--ui-muted);
}
.handshake {
  display: block;
  font-size: 10px;
  margin-top: 4px;
}
.node-actions {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 4px;
}
.node-current {
  color: var(--ui-accent);
  opacity: 1;
}
.node-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  min-height: 260px;
  padding: 24px;
  color: var(--ui-muted);
  text-align: center;
  strong {
    color: var(--ui-text);
    font-size: 15px;
  }
  p {
    margin: 0;
    font-size: 12px;
  }
}
.empty-action {
  color: var(--ui-accent);
  background: transparent;
  border: 0;
  padding: 6px;
  font-size: 12px;
}
.node-pool-footer {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 12px 16px;
  border-top: 1px solid var(--ui-border);
  color: var(--ui-muted);
  font-size: 11px;
}
@media (max-width: 700px) {
  .node-toolbar-actions {
    flex: 1;
  }
  .node-search {
    min-width: 170px;
  }
}
</style>
