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
const text = useVPNGateText()
const vpn = useVPNGateStore()
const draft = ref(defaultVPNGateSettings())
const dirty = ref(false)
const saved = ref(false)
const usesSOCKS4 = computed(() => ['socks4', 'socks4a'].includes(draft.value.upstream.protocol))
const protocolHint = computed(() => upstreamProtocolHints[draft.value.upstream.protocol])
let editRevision = 0
watch(
  () => vpn.state?.settings,
  (settings) => {
    if (settings && !dirty.value) draft.value = JSON.parse(JSON.stringify(settings))
  },
  { immediate: true },
)
const processPaths = computed({
  get: () => draft.value.upstream.processPaths.join('\n'),
  set: (value) => {
    draft.value.upstream.processPaths = value
      .split('\n')
      .map((path) => path.trim())
      .filter(Boolean)
  },
})
const edit = () => {
  editRevision++
  dirty.value = true
  saved.value = false
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
  <div class="flex flex-col gap-12" @input="edit" @change="edit">
    <Card :title="text('外部上游 · External mixed upstream')">
      <p>
        {{
          text(
            '所有管理下载与节点探测使用此外部 HTTP / SOCKS4 / SOCKS4a / SOCKS5 代理。端口必填，不自动猜测。 · Management downloads and probes use this HTTP / SOCKS4 / SOCKS4a / SOCKS5 proxy. An explicit port is required.',
          )
        }}
      </p>
      <div class="settings-grid">
        <label
          >{{ text('主机 · Host') }}<Input v-model="draft.upstream.host" @change="edit"
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
          }}<Select
            v-model="draft.upstream.protocol"
            :options="upstreamProtocolOptions"
            @change="edit"
        /></label>
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
            :placeholder="usesSOCKS4 ? text('此协议不支持密码 · Unsupported by this protocol') : ''"
            @change="edit"
        /></label>
      </div>
      <p class="text-12">{{ text(protocolHint) }}</p>
      <div
        v-if="usesSOCKS4 && draft.upstream.password"
        class="flex items-center flex-wrap gap-8 my-8"
      >
        <span class="text-12">{{
          text(
            '原密码已保留；保存前请清除，或切回 HTTP / SOCKS5。 · The previous password is retained. Clear it before saving, or switch back to HTTP / SOCKS5.',
          )
        }}</span>
        <Button size="small" @click="clearUnsupportedPassword">{{
          text('清除密码 · Clear password')
        }}</Button>
      </div>
      <label class="flex flex-col gap-8 my-12"
        >{{ text('上游进程完整路径，每行一个 · Upstream process paths, one per line')
        }}<textarea
          v-model="processPaths"
          rows="3"
          class="paths rounded-4 p-8"
          spellcheck="false"
          placeholder="C:\Program Files\Proxy\proxy.exe"
        />
      </label>
      <p class="text-12">
        {{
          text(
            '可选的上游可执行文件路径，用于标识本地上游。填写时使用实际完整路径。 · Optional executable paths identifying the local upstream. Enter the full executable path.',
          )
        }}
      </p>
    </Card>
    <Card :title="text('运行配置 · Managed runtime')">
      <div class="settings-grid">
        <label
          >{{ text('选定的内核 · Selected core')
          }}<span class="select-text break-all">{{
            vpn.state?.settings.corePath || text('请在内核设置中下载 · Download in Core settings')
          }}</span></label
        >
        <label
          >{{ text('本地 mixed 端口 · Local mixed port')
          }}<Input v-model="draft.mixedPort" type="number" :min="1" :max="65535" @change="edit"
        /></label>
        <label
          >TUN <Switch v-model="draft.tunEnabled" @change="edit" /><span>{{
            text('Windows 上需要以管理员身份运行 · Requires administrator rights on Windows')
          }}</span></label
        >
        <label
          >{{ text('系统代理 · System proxy')
          }}<Switch v-model="draft.systemProxyEnabled" @change="edit"
        /></label>
        <label
          >{{ text('节点池容量 · Pool capacity')
          }}<Input v-model="draft.poolCapacity" type="number" :min="1" :max="10000" @change="edit"
        /></label>
        <label
          >{{ text('刷新间隔（分钟） · Refresh minutes')
          }}<Input v-model="draft.refreshMinutes" type="number" :min="1" @change="edit"
        /></label>
      </div>
      <p class="text-12">
        {{
          text(
            '退出软件会停止连接并恢复本应用的系统代理设置。 · Exiting stops the connection and restores this application’s system proxy settings.',
          )
        }}
      </p>
    </Card>
    <Card :title="text('连通性测试 · Connectivity tests')">
      <div class="settings-grid">
        <label>测试 URL <Input v-model="draft.testURL" @change="edit" /></label>
        <label
          >{{ text('并发 · Concurrency')
          }}<Input v-model="draft.testConcurrency" type="number" :min="1" :max="32" @change="edit"
        /></label>
        <label
          >{{ text('超时（秒） · Timeout seconds')
          }}<Input
            v-model="draft.testTimeoutSeconds"
            type="number"
            :min="1"
            :max="600"
            @change="edit"
        /></label>
      </div>
    </Card>
    <Card :title="text('规则更新 · Rule updates')">
      <div class="settings-grid">
        <label
          >{{ text('自动更新 · Auto update')
          }}<Switch v-model="draft.rulesAutoUpdate" @change="edit"
        /></label>
        <label
          >{{ text('更新间隔（小时） · Update hours')
          }}<Input v-model="draft.rulesUpdateHours" type="number" :min="1" @change="edit"
        /></label>
        <label>CN rules URL <Input v-model="draft.cnRulesURL" @change="edit" /></label>
        <label>GFW rules URL <Input v-model="draft.gfwRulesURL" @change="edit" /></label>
      </div>
    </Card>
  </div>
</template>
<style scoped lang="less">
.settings-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(245px, 1fr));
  gap: 16px;
  margin: 16px 0;
}
label {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 8px;
  font-size: 12px;
}
.paths {
  color: var(--input-color);
  background: var(--input-bg);
  border: 1px solid var(--primary-color);
  width: 100%;
  box-sizing: border-box;
  resize: vertical;
}
</style>
