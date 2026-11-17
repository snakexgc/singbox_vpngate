<script setup lang="ts">
import { computed } from 'vue'

import { useVPNGateStore } from '@/stores/vpngate'
import { routingOptions } from '@/utils/vpngate'
import { useVPNGateText } from '@/utils/vpngateText'

import VPNGateStatus from '@/components/_common/VPNGateStatus.vue'

import type { RoutingPreset } from '@/types/vpngate'
const text = useVPNGateText()
const vpn = useVPNGateStore()
const choose = (value: string) => {
  if (vpn.state)
    void vpn.saveSettings({ ...vpn.state.settings, routingPreset: value as RoutingPreset })
}
const descriptions = computed<Record<string, string>>(() => ({
  direct: text('业务流量直连。 · Business traffic connects directly.'),
  'bypass-cn': text(
    '中国规则匹配流量直连，其余业务流量使用所选 VPNGate 节点。 · Traffic matching the China rules is direct; other business traffic uses the selected node.',
  ),
  blacklist: text(
    '黑名单流量使用所选 VPNGate 节点，其余直连。 · Blacklisted traffic uses the selected node; other traffic is direct.',
  ),
  global: text(
    '全部业务流量使用所选 VPNGate 节点。 · All business traffic uses the selected node.',
  ),
}))
</script>
<template>
  <div class="flex flex-col gap-12 pb-16">
    <VPNGateStatus />
    <Card :title="text('业务路由 · Business routing')">
      <p>
        {{
          text(
            '四种模式仅控制业务流量。节点列表、规则下载与连通性测试始终走外部上游。 · Management always uses the external upstream.',
          )
        }}
      </p>
      <div v-if="vpn.state" class="flex flex-col gap-8">
        <Card
          v-for="option in routingOptions"
          :key="option.value"
          :title="text(option.label)"
          :selected="vpn.state.settings.routingPreset === option.value"
        >
          <p>{{ descriptions[option.value] }}</p>
          <Button
            :disabled="!!vpn.busy || vpn.state.settings.routingPreset === option.value"
            @click="choose(option.value)"
            >{{ text('应用模式 · Apply mode') }}</Button
          >
        </Card>
      </div>
    </Card>
    <Card v-if="vpn.state" :title="text('规则数据 · Routing rules')">
      <p v-for="(rule, key) in vpn.state.rules" :key="key">
        {{ key }} · {{ rule.ready ? text('就绪 · Ready') : text('未就绪 · Missing') }} ·
        {{ rule.updatedAt || text('从未更新 · Never updated') }}
        <span v-if="rule.error"> · {{ rule.error }}</span>
      </p>
      <p>
        {{ text('自动更新 · Auto update:') }}
        {{
          vpn.state.settings.rulesAutoUpdate
            ? `${vpn.state.settings.rulesUpdateHours} h`
            : text('关闭 · Off')
        }}
      </p>
      <Button
        :disabled="!!vpn.busy || vpn.activeJobs.some((job) => job.kind === 'rules')"
        :loading="vpn.busy === 'rules.update'"
        @click="vpn.run('rules.update', undefined)"
        >{{ text('更新规则 · Update rules') }}</Button
      >
    </Card>
  </div>
</template>
