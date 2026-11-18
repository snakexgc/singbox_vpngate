<script setup lang="ts">
import { computed, onActivated, onDeactivated, ref, watchEffect } from 'vue'
import { useI18n } from 'vue-i18n'

import { VPNGateRequest } from '@/bridge/vpngate'

import { useVPNGateStore } from '@/stores/vpngate'
import { useVPNGateText } from '@/utils/vpngateText'

import StateBadge from '@/components/_common/StateBadge.vue'

import type { CoreLogLevel, VPNGateLogEntry } from '@/types/vpngate'

const vpn = useVPNGateStore()
const text = useVPNGateText()
const { locale } = useI18n()
const active = ref(false)
const paused = ref(false)
const loaded = ref(false)
const search = ref('')
const source = ref('all')
const readError = ref('')
const appLogs = ref<VPNGateLogEntry[]>([])
const coreLogs = ref<VPNGateLogEntry[]>([])
const cursor = { app: 0, core: 0 }
const levels: CoreLogLevel[] = ['trace', 'debug', 'info', 'warn', 'error', 'fatal', 'panic']
const runtime = computed(() => vpn.state?.runtime)
const savedLevel = computed(() => vpn.state?.logLevel ?? 'info')
const pendingLevel = computed(
  () => !!runtime.value?.logLevel && runtime.value.logLevel !== savedLevel.value,
)
const readTime = (value?: string) => (value ? Date.parse(value) : NaN)
const formatTime = (formatter: Intl.DateTimeFormat, value?: string) => {
  const time = readTime(value)
  return Number.isNaN(time) ? '—' : formatter.format(time)
}
const jobs = computed(() =>
  (vpn.state?.jobs ?? [])
    .map((job) => ({ ...job, time: job.startedAt || job.finishedAt }))
    .sort((a, b) => (readTime(b.time) || 0) - (readTime(a.time) || 0)),
)

const clock = computed(
  () =>
    new Intl.DateTimeFormat(locale.value, {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    }),
)
const dateTime = computed(
  () => new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'medium' }),
)
const visibleLogs = computed(() => {
  const keyword = search.value.trim().toLowerCase()
  const entries = [
    ...(source.value === 'core' ? [] : appLogs.value.map((entry) => ({ ...entry, source: 'app' }))),
    ...(source.value === 'app'
      ? []
      : coreLogs.value.map((entry) => ({ ...entry, source: 'core' }))),
  ]
  return entries
    .filter((entry) => `${entry.level} ${entry.message}`.toLowerCase().includes(keyword))
    .sort((a, b) => (readTime(b.time) || 0) - (readTime(a.time) || 0) || b.id - a.id)
})

function applyLevel(event: Event) {
  const select = event.target as HTMLSelectElement
  const level = select.value as CoreLogLevel
  select.value = savedLevel.value
  void vpn.run('logs.level', { level })
}

function clearDisplay() {
  appLogs.value = []
  coreLogs.value = []
}

onActivated(() => {
  active.value = true
})
onDeactivated(() => {
  active.value = false
})

watchEffect((onCleanup) => {
  if (!active.value || paused.value) return
  let closed = false
  let timer: ReturnType<typeof setTimeout>
  async function read() {
    try {
      const entries = await VPNGateRequest('logs.read', { ...cursor })
      if (closed) return
      const results = { app: appLogs, core: coreLogs }
      for (const name of ['app', 'core'] as const) {
        const incoming = entries[name]
        const last = incoming.at(-1)
        if (!last) continue
        const stored = results[name]
        stored.value = [...stored.value, ...incoming].slice(-500)
        cursor[name] = last.id
      }
      readError.value = ''
    } catch (error) {
      if (!closed) readError.value = error instanceof Error ? error.message : String(error)
    } finally {
      if (!closed) {
        loaded.value = true
        timer = setTimeout(read, 1500)
      }
    }
  }
  onCleanup(() => {
    closed = true
    clearTimeout(timer)
  })
  void read()
})
</script>

<template>
  <div class="logs-page">
    <div class="page-heading">
      <h1 class="page-title">{{ text('日志 · Logs') }}</h1>
      <label class="log-level-setting">
        <span>{{ text('sing-box 日志等级 · sing-box log level') }}</span>
        <select
          class="ui-select"
          :value="savedLevel"
          :disabled="!vpn.state || !!vpn.busy"
          :aria-describedby="pendingLevel ? 'log-level-hint' : undefined"
          @change="applyLevel"
        >
          <option v-for="level in levels" :key="level" :value="level">{{ level }}</option>
        </select>
      </label>
    </div>
    <p v-if="pendingLevel" id="log-level-hint" class="level-hint" role="status">
      <span
        >{{ text('当前等级 · Active level') }} <strong>{{ runtime?.logLevel }}</strong></span
      >
      <span>{{
        text('重启内核后应用新等级。 · The selected level takes effect when sing-box restarts.')
      }}</span>
    </p>

    <section
      class="log-console"
      :aria-label="text('应用与 sing-box 日志 · Application and sing-box logs')"
    >
      <div class="log-toolbar">
        <select v-model="source" class="ui-select" :aria-label="text('日志来源 · Log source')">
          <option value="all">{{ text('所有来源 · All sources') }}</option>
          <option value="app">{{ text('应用 · App') }}</option>
          <option value="core">sing-box</option>
        </select>
        <label class="log-search">
          <Icon icon="search" :size="16" aria-hidden="true" />
          <input
            v-model="search"
            type="search"
            :aria-label="text('搜索日志 · Search logs')"
            :placeholder="text('搜索日志 · Search logs')"
          />
        </label>
        <div class="log-toolbar-actions">
          <Button :icon="paused ? 'play' : 'pause'" type="text" @click="paused = !paused">
            {{ paused ? text('继续 · Resume') : text('暂停 · Pause') }}
          </Button>
          <Button
            icon="clear"
            type="text"
            :disabled="!appLogs.length && !coreLogs.length"
            @click="clearDisplay"
          >
            {{ text('清空显示 · Clear view') }}
          </Button>
        </div>
      </div>

      <p v-if="readError" class="log-error" role="status">{{ readError }}</p>

      <div
        v-if="visibleLogs.length"
        class="log-console-scroll"
        tabindex="0"
        :aria-label="text('日志列表 · Log list')"
      >
        <table class="log-table">
          <thead>
            <tr>
              <th scope="col">{{ text('时间 · Time') }}</th>
              <th scope="col">{{ text('来源 · Source') }}</th>
              <th scope="col">{{ text('等级 · Level') }}</th>
              <th scope="col">{{ text('消息 · Message') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="entry in visibleLogs" :key="`${entry.source}:${entry.id}`">
              <td>
                <time :datetime="entry.time" :title="formatTime(dateTime, entry.time)">{{
                  formatTime(clock, entry.time)
                }}</time>
              </td>
              <td>{{ entry.source === 'app' ? text('应用 · App') : 'sing-box' }}</td>
              <td :class="`log-level log-level--${entry.level}`">{{ entry.level || '—' }}</td>
              <td class="log-message select-text">{{ entry.message }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-else-if="!readError" class="log-empty" role="status">
        <template v-if="!loaded">{{ text('正在读取日志… · Loading logs…') }}</template>
        <template v-else-if="search.trim()">{{
          text('暂无匹配的日志，请尝试其他关键词。 · No matching logs. Try another keyword.')
        }}</template>
        <template v-else>{{ text('当前来源暂无日志。 · No logs from this source yet.') }}</template>
      </p>
    </section>

    <details v-if="runtime && vpn.state" class="ui-details">
      <summary>{{ text('运行详情 · Runtime details') }}</summary>
      <div class="log-detail-body">
        <dl class="runtime-details">
          <div>
            <dt>{{ text('状态 · Status') }}</dt>
            <dd>
              <StateBadge :state="runtime.status">{{ text(runtime.status) }}</StateBadge>
            </dd>
          </div>
          <div>
            <dt>sing-box</dt>
            <dd>
              {{ runtime.coreVersion || vpn.coreStatus?.version || text('未检测 · Not detected') }}
            </dd>
          </div>
          <div>
            <dt>{{ text('系统代理 · System proxy') }}</dt>
            <dd>
              {{ runtime.systemProxyEnabled ? text('开启 · On') : text('关闭 · Off') }}
            </dd>
          </div>
          <div>
            <dt>{{ text('TUN 模式 · TUN mode') }}</dt>
            <dd>{{ runtime.tunEnabled ? text('开启 · On') : text('关闭 · Off') }}</dd>
          </div>
        </dl>
        <p v-if="runtime.message" class="runtime-message select-text">{{ runtime.message }}</p>
      </div>
    </details>

    <details class="ui-details task-history">
      <summary>{{ text('任务记录 · Task history') }}</summary>
      <ol v-if="jobs.length" class="history-list log-detail-body">
        <li v-for="job in jobs" :key="job.id">
          <div class="job-detail">
            <strong>{{ text(job.kind) }}</strong>
            <StateBadge :state="job.status">{{ text(job.status) }}</StateBadge>
            <span v-if="job.total" class="job-progress">{{ job.completed }} / {{ job.total }}</span>
            <time class="job-time" :datetime="job.time">{{ formatTime(dateTime, job.time) }}</time>
          </div>
          <p v-if="job.message" class="runtime-message select-text">{{ job.message }}</p>
        </li>
      </ol>
      <p v-else class="log-detail-body empty-history">
        {{ text('暂无任务记录。 · No jobs recorded yet.') }}
      </p>
    </details>
  </div>
</template>

<style scoped lang="less">
.logs-page {
  display: grid;
  gap: 12px;
  padding-bottom: 16px;
}
.log-level-setting,
.log-toolbar,
.log-toolbar-actions,
.log-search,
.job-detail {
  display: flex;
  align-items: center;
  gap: 8px;
}
.log-level-setting,
.level-hint {
  color: var(--ui-muted);
  font-size: 13px;
}
.level-hint {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
  margin: -4px 0 0;
}
.log-console {
  min-width: 0;
  border: 1px solid var(--ui-border);
  border-radius: 12px;
  background: var(--ui-surface);
}
.log-toolbar {
  flex-wrap: wrap;
  padding: 12px 16px;
  border-bottom: 1px solid var(--ui-border);
}
.log-search {
  flex: 1 1 140px;
  min-width: 140px;
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
    min-width: 0;
    min-height: 32px;
    border: 0;
    outline: 0;
    color: var(--ui-text);
    background: transparent;
    font-size: 13px;

    &::placeholder {
      color: var(--ui-muted);
    }
  }
}
.log-toolbar-actions {
  margin-left: auto;
}
.log-console-scroll {
  max-height: clamp(140px, 56vh, 640px);
  overflow: auto;
}
.log-table {
  width: 100%;
  border-collapse: separate;
  border-spacing: 0;
  table-layout: fixed;
  font-size: 12px;

  th,
  td {
    padding: 8px 12px;
    border-bottom: 1px solid var(--ui-border);
    text-align: left;
    vertical-align: top;
  }

  th {
    position: sticky;
    top: 0;
    background: var(--ui-surface-soft);
    color: var(--ui-muted);
    font-weight: 600;
  }

  th:nth-child(1) {
    width: 96px;
  }
  th:nth-child(2) {
    width: 82px;
  }
  th:nth-child(3) {
    width: 68px;
  }
  td:first-child,
  td:nth-child(2) {
    color: var(--ui-muted);
  }
  tr:last-child td {
    border-bottom: 0;
  }
}
.log-level {
  color: var(--ui-muted);
  font-weight: 600;
  text-transform: uppercase;
}
.log-level--warn {
  color: var(--ui-warning);
}
.log-level--error,
.log-level--fatal,
.log-level--panic {
  color: var(--ui-danger);
}
.log-message {
  font-family: ui-monospace, Consolas, monospace;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
time,
.job-progress {
  font-variant-numeric: tabular-nums;
}
.log-empty {
  display: grid;
  place-items: center;
  min-height: 120px;
  margin: 0;
  padding: 16px;
  color: var(--ui-muted);
  text-align: center;
  font-size: 13px;
}
.log-error {
  margin: 12px 16px;
  color: var(--ui-danger);
  overflow-wrap: anywhere;
  font-size: 13px;
}
.log-detail-body {
  padding: 0 16px 12px;
}
.runtime-details {
  display: flex;
  flex-wrap: wrap;
  gap: 12px 24px;
  margin: 0;

  dt {
    color: var(--ui-muted);
  }
  dd {
    margin: 4px 0 0;
  }
}
.runtime-message {
  margin: 8px 0 0;
  overflow-wrap: anywhere;
  color: var(--ui-muted);
}
.history-list {
  margin: 0;
  list-style: none;

  li + li {
    margin-top: 8px;
    padding-top: 8px;
    border-top: 1px solid var(--ui-border);
  }
}
.job-detail {
  flex-wrap: wrap;
}
.job-time {
  margin-left: auto;
  color: var(--ui-muted);
}
.empty-history {
  margin: 0;
  color: var(--ui-muted);
}
@media (max-width: 640px) {
  .log-table th,
  .log-table td {
    padding-inline: 8px;
  }
  .log-table th:nth-child(1) {
    width: 84px;
  }
  .log-table th:nth-child(2) {
    width: 66px;
  }
  .log-table th:nth-child(3) {
    width: 52px;
  }
}
</style>
