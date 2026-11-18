<script setup lang="ts">
import { OS } from '@/enums/app'
import { useEnvStore } from '@/stores'
import { useVPNGateStore } from '@/stores/vpngate'
import { useVPNGateText } from '@/utils/vpngateText'

const envStore = useEnvStore()
const vpn = useVPNGateStore()
const text = useVPNGateText()
</script>

<template>
  <div
    v-if="
      envStore.env.os === OS.Windows &&
      !envStore.env.isPrivileged &&
      (vpn.state?.settings.tunEnabled ||
        vpn.state?.runtime.requiresAdmin ||
        vpn.state?.repair.requiresAdmin)
    "
    class="admin-notice"
    role="alert"
  >
    <Icon icon="messageWarn" :size="20" class="admin-notice-icon" aria-hidden="true" />
    <div class="admin-notice-copy">
      <strong>{{ text('需要管理员权限 · Administrator rights required') }}</strong>
      <p>
        {{
          text(
            'TUN 或网络修复需要管理员权限。 · TUN or network repair requires administrator rights.',
          )
        }}
      </p>
    </div>
    <Button
      type="primary"
      size="small"
      :disabled="!!vpn.busy"
      :loading="vpn.busy === 'runtime.elevate'"
      @click="vpn.run('runtime.elevate', undefined)"
      >{{ text('管理员重启 · Restart as admin') }}</Button
    >
  </div>
</template>

<style scoped lang="less">
.admin-notice {
  display: flex;
  flex-wrap: wrap;
  flex-shrink: 0;
  align-items: center;
  gap: 8px 12px;
  margin: 8px 8px 0;
  padding: 10px 12px;
  border: 1px solid rgba(250, 173, 20, 0.4);
  border-radius: 8px;
  color: var(--color);
  background: rgba(250, 173, 20, 0.1);
}
.admin-notice-icon,
.admin-notice > .gui-button {
  flex-shrink: 0;
}
.admin-notice-copy {
  flex: 1;
  min-width: 200px;
  font-size: 12px;
  line-height: 1.5;
  p {
    margin: 2px 0 0;
    opacity: 0.8;
  }
}
</style>
