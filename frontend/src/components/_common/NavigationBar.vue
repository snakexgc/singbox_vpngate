<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import rawRoutes from '@/router/routes'
import { useAppSettingsStore } from '@/stores'
import { useVPNGateText } from '@/utils/vpngateText'

const { t } = useI18n()
const appSettings = useAppSettingsStore()
const text = useVPNGateText()

const routes = computed(() =>
  rawRoutes.filter(
    (r) =>
      r.meta?.hidden === false ||
      (!r.meta?.hidden && appSettings.app.pages.includes(r.name! as string)),
  ),
)
</script>

<template>
  <nav class="main-nav" :aria-label="text('主导航 · Main navigation')">
    <RouterLink
      v-for="r in routes"
      :key="r.path"
      :to="r.path"
      class="main-nav__link"
      active-class="is-active"
    >
      <Icon
        v-if="r.meta?.icon"
        :icon="r.meta.icon"
        :size="18"
        color="currentColor"
        aria-hidden="true"
      />
      <span>{{
        r.name === 'VPNGate' ? text('节点 · Nodes') : (r.meta && t(r.meta.name)) || r.name
      }}</span>
    </RouterLink>
  </nav>
</template>

<style scoped lang="less">
.main-nav {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 4px;
}
.main-nav__link {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  min-height: 34px;
  padding: 3px 12px;
  border-radius: 6px;
  font-size: 14px;
  color: var(--btn-text-color);
  transition:
    color 150ms,
    background 150ms;
  &:hover {
    background: var(--ui-surface-soft);
    color: var(--ui-text);
  }
  &.is-active {
    color: var(--ui-accent);
  }
}
@media (max-width: 600px) {
  .main-nav {
    gap: 4px;
  }
  .main-nav__link {
    gap: 6px;
    padding: 4px 8px;
  }
}
</style>
