<script lang="ts" setup>
import { ref, computed, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'

import { LogLevelOptions } from '@/constant/kernel'
import { useBool } from '@/hooks'
import { useKernelApiStore } from '@/stores'
import { buildSmartRegExp } from '@/utils'

const logType = ref<'trace' | 'debug' | 'info' | 'warn' | 'error' | 'fatal' | 'panic'>('info')
const keywords = ref('')
const logs = ref<{ id: number; type: string; payload: string }[]>([])
let nextLogId = 0

const LogLevelMap = {
  trace: ['trace', 'debug', 'info', 'warn', 'error', 'fatal', 'panic'],
  debug: ['debug', 'info', 'warn', 'error', 'fatal', 'panic'],
  info: ['info', 'warn', 'error', 'fatal', 'panic'],
  warn: ['warn', 'error', 'fatal', 'panic'],
  error: ['error', 'fatal', 'panic'],
  fatal: ['fatal', 'panic'],
  panic: ['panic'],
}

const filteredLogs = computed(() => {
  return logs.value.filter((v) => {
    const hitType = LogLevelMap[logType.value].includes(v.type)
    const hitName = buildSmartRegExp(keywords.value, 'i').test(v.payload)
    return hitName && hitType
  })
})

const { t } = useI18n()
const [pause, togglePause] = useBool(false)
const kernelApiStore = useKernelApiStore()

const handleClear = () => logs.value.splice(0)

const unregisterLogsHandler = kernelApiStore.onLogs((data) => {
  if (!pause.value) logs.value.unshift({ ...data, id: nextLogId++ })
})

onUnmounted(() => {
  unregisterLogsHandler()
})
</script>

<template>
  <ModalContainer :empty="filteredLogs.length === 0">
    <template #top>
      <div class="flex items-center">
        <Select v-model="logType" :options="LogLevelOptions" size="small" />
        <Input
          v-model="keywords"
          clearable
          size="small"
          :placeholder="t('common.keywords')"
          class="ml-8 flex-1"
        />
        <Button
          :icon="pause ? 'play' : 'pause'"
          type="text"
          size="small"
          class="ml-8"
          @click="togglePause"
        />
        <Button
          v-tips="'common.clear'"
          icon="clear"
          size="small"
          type="text"
          @click="handleClear"
        />
      </div>
    </template>

    <template #body>
      <div v-for="log in filteredLogs" :key="log.id" class="log select-text text-12 py-2 my-4">
        <span class="type inline-block text-center">{{ log.type }}</span> {{ log.payload }}
      </div>
    </template>
  </ModalContainer>
</template>

<style lang="less" scoped>
.log {
  background: var(--card-bg);
  &:hover {
    color: #fff;
    background: var(--primary-color);
    .type {
      color: #fff;
    }
  }
}

.type {
  width: 50px;
  color: var(--primary-color);
}
</style>
