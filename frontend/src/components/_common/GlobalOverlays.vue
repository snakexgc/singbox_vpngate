<script setup lang="ts">
import { useAppStore } from '@/stores'

interface Props {
  loading: boolean
}

defineProps<Props>()

const appStore = useAppStore()

const openAll = () => {
  appStore.modalMinimized.forEach((m) => m.openFn())
}
const minimizeAll = () => {
  appStore.modalMinimized.forEach((m) => m.minimizeFn())
}
</script>

<template>
  <Menu
    v-model="appStore.menuShow"
    :position="appStore.menuPosition"
    :menu-list="appStore.menuList"
  />

  <Tips
    v-model="appStore.tipsShow"
    :position="appStore.tipsPosition"
    :message="appStore.tipsMessage"
  />

  <div
    v-if="appStore.modalMinimized.length"
    :style="appStore.modalSplitActive ? { right: 'calc(50% + 32px)' } : undefined"
    class="fixed right-32 bottom-32 flex flex-col gap-8 z-9999"
  >
    <Dropdown v-if="appStore.modalMinimized.length" placement="top">
      <Button icon="adjust" class="shadow" />
      <template #overlay>
        <Card title="common.modalList">
          <template v-if="false" #extra>
            <Button size="small" type="link" @click="openAll">
              {{ $t('common.openAll') }}
            </Button>
            <Button size="small" type="link" @click="minimizeAll">
              {{ $t('common.minimizeAll') }}
            </Button>
          </template>
          <div class="flex flex-col gap-4 p-4 min-w-128">
            <div
              v-for="modal in appStore.modalMinimized"
              :key="modal.id"
              class="flex items-center justify-between"
            >
              <Button class="flex-1" type="text" @click="modal.openFn">
                {{ $t(modal.title()) }}
              </Button>
              <Button icon="close" type="text" size="small" @click="modal.closeFn" />
            </div>
          </div>
        </Card>
      </template>
    </Dropdown>
  </div>
</template>
