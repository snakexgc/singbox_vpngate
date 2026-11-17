<script setup lang="ts">
import { type IconName } from '@/components/Icon/icons'

interface Props {
  type?: 'primary' | 'normal' | 'link' | 'text'
  size?: 'default' | 'small' | 'large'
  iconSize?: number
  iconColor?: string
  icon?: IconName
  loading?: boolean
  disabled?: boolean
  role?: string
}

withDefaults(defineProps<Props>(), {
  type: 'normal',
  size: 'default',
  iconSize: undefined,
  iconColor: undefined,
  icon: undefined,
  loading: false,
  disabled: false,
  role: 'button',
})
</script>

<template>
  <div
    :role="role"
    :aria-disabled="disabled || loading ? true : undefined"
    :class="[
      type,
      size,
      { 'pointer-events-none': disabled || loading, 'button-disabled': disabled },
    ]"
    class="gui-button relative inline-flex items-center justify-center text-center align-middle rounded-6 text-14 text-nowrap cursor-pointer px-12 py-6 duration-200"
  >
    <span
      class="inline-flex items-center justify-center"
      :class="{ 'button-content-loading': loading }"
      :style="
        !icon && !$slots.default
          ? {
              width: `${size === 'small' ? 14 : 16}px`,
              height: `${size === 'small' ? 14 : 16}px`,
            }
          : undefined
      "
    >
      <Icon
        v-if="icon"
        :icon="icon"
        :size="iconSize"
        :color="iconColor || 'currentColor'"
        :class="$slots.default ? 'mr-4' : ''"
      />
      <slot></slot>
    </span>
    <span
      v-if="loading"
      class="absolute inset-0 flex items-center justify-center"
      aria-hidden="true"
    >
      <Icon
        color="currentColor"
        :size="size === 'small' ? 14 : 16"
        icon="loading"
        class="rotation"
      />
    </span>
  </div>
</template>

<style lang="less" scoped>
.button-disabled {
  opacity: 0.5;
}

.button-content-loading {
  opacity: 0;
}

.normal {
  color: var(--btn-normal-color);
  background-color: var(--btn-normal-bg);
  &:hover {
    color: var(--btn-normal-hover-color);
    border-color: var(--btn-normal-hover-border-color);
  }
  &:active {
    color: var(--btn-normal-active-color);
    border-color: var(--btn-normal-active-border-color);
  }
}

.primary {
  color: var(--btn-primary-color);
  background-color: var(--btn-primary-bg);
  border: none;
  &:hover {
    background-color: var(--btn-primary-hover-bg);
  }
  &:active {
    background-color: var(--btn-primary-active-bg);
  }
}

.link {
  color: var(--btn-link-color);
  background-color: var(--btn-link-bg);
  border: none;
  &:hover {
    color: var(--btn-link-hover-color);
  }
  &:active {
    color: var(--btn-link-active-color);
  }
}

.text {
  color: var(--btn-text-color);
  background-color: var(--btn-text-bg);
  border: none;
  &:hover {
    color: var(--btn-text-hover-color);
    background-color: var(--btn-text-hover-bg);
  }
  &:active {
    color: var(--btn-text-active-color);
    background-color: var(--btn-text-active-bg);
  }
}

.small {
  padding: 4px 8px;
  font-size: 12px;
}
.large {
  padding: 8px 12px;
  font-size: 16px;
}
</style>
