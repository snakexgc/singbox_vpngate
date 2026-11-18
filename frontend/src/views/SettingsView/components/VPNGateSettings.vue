<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import { useVPNGateStore } from '@/stores/vpngate'
import {
  defaultVPNGateSettings,
  submitVPNGateSettingsDraft,
  upstreamProtocolHints,
  upstreamProtocolOptions,
} from '@/utils/vpngate'
import { useVPNGateText } from '@/utils/vpngateText'

import StateBadge from '@/components/_common/StateBadge.vue'
const text = useVPNGateText()
const vpn = useVPNGateStore()
const draft = ref(defaultVPNGateSettings())
const dirty = ref(false)
const saved = ref(false)
const usesSOCKS4 = computed(() => ['socks4', 'socks4a'].includes(draft.value.upstream.protocol))
const protocolHint = computed(() => upstreamProtocolHints[draft.value.upstream.protocol])
const updatedDate = (value: string) => {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleDateString()
}
let editRevision = 0
watch(
  () => vpn.state?.settings,
  (settings, previous) => {
    if (!settings) return
    if (!dirty.value) {
      draft.value = JSON.parse(JSON.stringify(settings))
      return
    }
    // Overview saves these switches immediately. Keep locally edited switches in the draft.
    for (const key of ['tunEnabled', 'systemProxyEnabled'] as const) {
      if (draft.value[key] === previous?.[key]) draft.value[key] = settings[key]
    }
  },
  { immediate: true },
)
const edit = () => {
  editRevision++
  dirty.value = true
  saved.value = false
}
const toggleTUN = () => {
  draft.value.tunEnabled = !draft.value.tunEnabled
  edit()
}
const toggleSystemProxy = () => {
  draft.value.systemProxyEnabled = !draft.value.systemProxyEnabled
  edit()
}
const toggleRulesAutoUpdate = () => {
  draft.value.rulesAutoUpdate = !draft.value.rulesAutoUpdate
  edit()
}
const clearUnsupportedPassword = () => {
  draft.value.upstream.password = ''
  edit()
}
const save = async () => {
  if (!vpn.state) return
  saved.value = false
  if (
    await submitVPNGateSettingsDraft(
      draft.value,
      vpn.state.settings.routingPreset,
      () => editRevision,
      vpn.saveSettings,
    )
  ) {
    dirty.value = false
    saved.value = true
  }
}
const reset = () => {
  if (vpn.state) {
    editRevision++
    draft.value = JSON.parse(JSON.stringify(vpn.state.settings))
    dirty.value = false
    saved.value = false
  }
}
defineExpose({ dirty, saved, save, reset })
</script>
<template>
  <div class="managed-settings" @input="edit" @change="edit">
    <Card :title="text('上游代理 · Upstream proxy')">
      <p class="section-hint">
        {{
          text(
            '支持本机或远程代理，为 VPNGate 隧道、节点测试和下载提供上游连接。 · Use a local or remote proxy for VPNGate tunnels, node tests, and downloads.',
          )
        }}
      </p>
      <div class="settings-grid upstream-grid">
        <label
          >{{ text('主机 · Host')
          }}<Input
            v-model="draft.upstream.host"
            :placeholder="text('域名或 IP 地址 · Hostname or IP address')"
            @change="edit"
        /></label>
        <label
          >{{ text('端口 · Port')
          }}<Input
            v-model="draft.upstream.port"
            type="number"
            :min="0"
            :max="65535"
            :placeholder="text('必填 · Required')"
            @change="edit"
        /></label>
        <label
          >{{ text('协议 · Protocol')
          }}<select v-model="draft.upstream.protocol" class="ui-select" @change="edit">
            <option
              v-for="option in upstreamProtocolOptions"
              :key="option.value"
              :value="option.value"
            >
              {{ text(option.label) }}
            </option>
          </select></label
        >
      </div>
      <div v-if="usesSOCKS4 && draft.upstream.password" class="password-warning" role="alert">
        <span>{{
          text(
            '此协议不支持密码。请清除已保留的密码，或改用 HTTP / SOCKS5。 · This protocol does not support passwords. Clear the retained password or use HTTP / SOCKS5.',
          )
        }}</span>
        <Button size="small" @click="clearUnsupportedPassword">{{
          text('清除密码 · Clear password')
        }}</Button>
      </div>
      <details class="ui-details optional-details" :open="usesSOCKS4 && !!draft.upstream.password">
        <summary>
          {{ text('认证信息 · Authentication')
          }}<span class="optional-label">{{ text('可选 · Optional') }}</span>
        </summary>
        <div class="details-content">
          <div class="settings-grid">
            <label
              >{{ text(usesSOCKS4 ? '用户 ID · User ID' : '用户名 · Username')
              }}<Input v-model="draft.upstream.username" @change="edit"
            /></label>
            <label
              >{{ text('密码 · Password')
              }}<Input
                v-model="draft.upstream.password"
                type="password"
                :disabled="usesSOCKS4"
                :placeholder="
                  usesSOCKS4 ? text('此协议不支持密码 · Unsupported by this protocol') : ''
                "
                @change="edit"
            /></label>
          </div>
          <p class="field-hint protocol-hint">{{ text(protocolHint) }}</p>
        </div>
      </details>
    </Card>
    <Card :title="text('连接方式 · Connection mode')">
      <div class="settings-grid runtime-grid">
        <label
          >{{ text('本地端口 · Local mixed port')
          }}<Input v-model="draft.mixedPort" type="number" :min="1" :max="65535" @change="edit"
        /></label>
        <label class="toggle-field"
          ><span>TUN</span
          ><Switch
            v-model="draft.tunEnabled"
            :aria-label="text('启用 TUN · Enable TUN')"
            tabindex="0"
            @keydown.space.prevent="toggleTUN"
            @keydown.enter.prevent="toggleTUN"
            @change="edit"
          /><span class="field-hint">{{
            text('Windows 需要管理员权限 · Administrator rights on Windows')
          }}</span></label
        >
        <label class="toggle-field"
          >{{ text('系统代理 · System proxy')
          }}<Switch
            v-model="draft.systemProxyEnabled"
            :aria-label="text('启用系统代理 · Enable system proxy')"
            tabindex="0"
            @keydown.space.prevent="toggleSystemProxy"
            @keydown.enter.prevent="toggleSystemProxy"
            @change="edit"
        /></label>
      </div>
      <p class="field-hint runtime-note">
        {{
          text(
            '退出时停止连接并恢复系统代理。 · Exiting stops the connection and restores the system proxy.',
          )
        }}
      </p>
      <details class="ui-details core-details">
        <summary>{{ text('当前内核路径 · Current core path') }}</summary>
        <p class="core-path select-text">
          {{
            vpn.state?.settings.corePath || text('请在「内核」中下载 · Download from the Core tab')
          }}
        </p>
      </details>
    </Card>
    <Card :title="text('分流规则 · Routing rules')">
      <template #extra>
        <Button
          size="small"
          :disabled="!vpn.state || !!vpn.busy || vpn.activeJobs.some((job) => job.kind === 'rules')"
          :loading="vpn.busy === 'rules.update'"
          :title="
            dirty ? text('使用已保存的配置更新规则 · Update rules using saved settings') : undefined
          "
          @click="vpn.run('rules.update', undefined)"
          >{{ text('更新规则 · Update rules') }}</Button
        >
      </template>
      <div v-if="vpn.state" class="rule-list">
        <div v-for="(rule, key) in vpn.state.rules" :key="key" class="rule-item">
          <div class="rule-row">
            <strong class="rule-name">{{
              key === 'cn' ? text('大陆规则 · China rules') : text('黑名单规则 · Blacklist rules')
            }}</strong>
            <time
              v-if="rule.updatedAt"
              class="rule-date"
              :datetime="rule.updatedAt"
              :title="rule.updatedAt"
              >{{ updatedDate(rule.updatedAt) }}</time
            >
            <span v-else class="rule-date">{{ text('从未更新 · Never updated') }}</span>
            <StateBadge :tone="rule.error ? 'warning' : rule.ready ? 'success' : 'warning'">
              {{
                rule.error
                  ? text('更新失败 · Update failed')
                  : rule.ready
                    ? text('就绪 · Ready')
                    : text('未就绪 · Missing')
              }}
            </StateBadge>
          </div>
          <details v-if="rule.error" class="ui-details rule-error">
            <summary>{{ text('查看原因 · View details') }}</summary>
            <p class="select-text">{{ text(rule.error) }}</p>
            <p>
              {{
                rule.ready
                  ? text('已有规则仍可使用 · Existing rules are available')
                  : text('请更新规则后重试 · Update the rules and retry')
              }}
            </p>
          </details>
        </div>
      </div>
      <p v-else class="section-hint rules-loading">
        {{ text('正在读取规则状态… · Loading rule status…') }}
      </p>
      <div v-if="vpn.state" class="rules-schedule">
        <span>{{ text('已保存的自动更新 · Saved auto update') }}</span>
        <StateBadge :tone="vpn.state.settings.rulesAutoUpdate ? 'info' : 'neutral'">
          {{
            vpn.state.settings.rulesAutoUpdate
              ? text(
                  `每 ${vpn.state.settings.rulesUpdateHours} 小时 · Every ${vpn.state.settings.rulesUpdateHours} h`,
                )
              : text('关闭 · Off')
          }}
        </StateBadge>
      </div>
      <p v-if="dirty" class="field-hint rules-draft-note">
        {{
          text(
            '立即更新使用已保存的配置。修改后请点击页面上方「保存」。 · Update rules uses saved settings. Save your changes using the button above.',
          )
        }}
      </p>
      <details class="ui-details optional-details rules-config">
        <summary>{{ text('自动更新与来源 · Auto update & sources') }}</summary>
        <div class="details-content">
          <div class="settings-grid">
            <label class="toggle-field"
              >{{ text('自动更新 · Auto update')
              }}<Switch
                v-model="draft.rulesAutoUpdate"
                :aria-label="text('自动更新规则 · Auto-update rules')"
                tabindex="0"
                @keydown.space.prevent="toggleRulesAutoUpdate"
                @keydown.enter.prevent="toggleRulesAutoUpdate"
                @change="edit"
            /></label>
            <label
              >{{ text('更新间隔（小时） · Update interval (hours)')
              }}<Input v-model="draft.rulesUpdateHours" type="number" :min="1" @change="edit"
            /></label>
            <label class="wide-field"
              >{{ text('CN 规则 URL · CN rules URL')
              }}<Input v-model="draft.cnRulesURL" @change="edit"
            /></label>
            <label class="wide-field"
              >{{ text('GFW 规则 URL · GFW rules URL')
              }}<Input v-model="draft.gfwRulesURL" @change="edit"
            /></label>
          </div>
        </div>
      </details>
    </Card>
    <details class="ui-details advanced-details">
      <summary>{{ text('节点池与测试 · Node pool & tests') }}</summary>
      <div class="details-content">
        <div class="settings-grid">
          <label
            >{{ text('节点池容量 · Pool capacity')
            }}<Input
              v-model="draft.poolCapacity"
              type="number"
              :min="1"
              :max="10000"
              @change="edit"
          /></label>
          <label
            >{{ text('刷新间隔（分钟） · Refresh interval (min)')
            }}<Input v-model="draft.refreshMinutes" type="number" :min="1" @change="edit"
          /></label>
          <label class="wide-field"
            >{{ text('测试 URL · Test URL') }}<Input v-model="draft.testURL" @change="edit"
          /></label>
          <label
            >{{ text('并发 · Concurrency')
            }}<Input
              v-model="draft.testConcurrency"
              type="number"
              :min="1"
              :max="32"
              @change="edit"
          /></label>
          <label
            >{{ text('超时（秒） · Timeout (sec)')
            }}<Input
              v-model="draft.testTimeoutSeconds"
              type="number"
              :min="1"
              :max="600"
              @change="edit"
          /></label>
        </div>
      </div>
    </details>
  </div>
</template>
<style scoped lang="less">
.managed-settings {
  display: flex;
  flex-direction: column;
  gap: 12px;
  color: var(--ui-text);
}
.settings-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
  margin: 12px 0;
}
.upstream-grid,
.runtime-grid {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}
label {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 8px;
  min-width: 0;
  color: var(--ui-text);
  font-size: 13px;
  .ui-select,
  :deep(.gui-input) {
    width: 100%;
    min-width: 0;
  }
}
.section-hint,
.field-hint {
  color: var(--ui-muted);
  font-size: 12px;
  line-height: 1.65;
}
.section-hint {
  margin: 0;
}
.protocol-hint {
  margin: 0;
  max-width: 80ch;
}
.runtime-note {
  margin: 0 0 12px;
}
.rule-item {
  padding: 12px 0;
  + .rule-item {
    border-top: 1px solid var(--ui-border);
  }
}
.rule-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 12px;
}
.rule-name {
  flex: 1;
  min-width: 100px;
  font-size: 13px;
  font-weight: 500;
}
.rule-date {
  color: var(--ui-muted);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
.rule-error {
  margin-top: 8px;
  border: 0;
  border-radius: 0;
  background: transparent;
  font-size: 12px;
  > summary {
    padding: 0;
    color: var(--ui-muted);
    font-weight: 400;
  }
  &[open] > summary {
    margin: 0;
    padding: 0;
    border: 0;
  }
  p {
    margin: 8px 0 0;
    color: var(--ui-muted);
    line-height: 1.65;
    overflow-wrap: anywhere;
  }
}
.rules-loading {
  padding: 12px 0;
}
.rules-schedule {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-top: 2px;
  padding: 12px 0;
  border-top: 1px solid var(--ui-border);
  color: var(--ui-muted);
  font-size: 12px;
}
.rules-draft-note {
  margin: 0 0 12px;
  max-width: 75ch;
}
.optional-details,
.core-details {
  border: 0;
  border-top: 1px solid var(--ui-border);
  border-radius: 0;
  background: transparent;
  > summary {
    padding: 10px 0;
  }
  &[open] > summary {
    margin-bottom: 8px;
    border: 0;
  }
}
.optional-label {
  margin-left: 8px;
  color: var(--ui-muted);
  font-size: 12px;
  font-weight: 400;
}
.details-content {
  padding: 0 0 4px;
}
.advanced-details {
  background: var(--ui-surface);
  > .details-content {
    padding-right: 16px;
    padding-left: 16px;
  }
}
.core-path {
  margin: 4px 0 8px;
  color: var(--ui-muted);
  font-size: 12px;
  overflow-wrap: anywhere;
}
.wide-field {
  grid-column: 1 / -1;
}
.password-warning {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 12px;
  margin-bottom: 16px;
  padding: 10px 12px;
  border: 1px solid var(--ui-warning);
  border-radius: 8px;
  color: var(--ui-warning);
  background: var(--ui-warning-bg);
  font-size: 12px;
  line-height: 1.65;
}
.toggle-field :deep(.gui-switch):focus-visible {
  outline: 2px solid var(--ui-accent);
  outline-offset: 3px;
}
@media (max-width: 760px) {
  .upstream-grid,
  .runtime-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 520px) {
  .settings-grid,
  .upstream-grid,
  .runtime-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
