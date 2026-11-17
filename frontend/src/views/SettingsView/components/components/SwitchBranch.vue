<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import { Branch } from '@/enums/app'
import { chooseCoreBranch } from '@/hooks/useCoreBranch'
import { useAppSettingsStore } from '@/stores'

const { t } = useI18n()
const appSettings = useAppSettingsStore()

const handleUseBranch = async (branch: Branch) => {
  await chooseCoreBranch(branch === Branch.Alpha)
}
</script>

<template>
  <div class="font-bold text-16 mx-4 my-12">{{ t('settings.kernel.version') }}</div>
  <div class="flex gap-8">
    <Card
      :selected="appSettings.app.kernel.branch === Branch.Main"
      title="Stable"
      class="w-[36%]"
      @click="handleUseBranch(Branch.Main)"
    >
      <div class="py-4 text-12">
        {{ t('settings.kernel.stable') }}
      </div>
    </Card>
    <Card
      :selected="appSettings.app.kernel.branch === Branch.Alpha"
      title="Alpha"
      class="w-[36%]"
      @click="handleUseBranch(Branch.Alpha)"
    >
      <div class="py-4 text-12">
        {{ t('settings.kernel.alpha') }}
      </div>
    </Card>
  </div>
</template>
