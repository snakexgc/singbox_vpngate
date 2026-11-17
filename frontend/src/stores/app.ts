import { defineStore } from 'pinia'
import { ref } from 'vue'

import { ReadDir } from '@/bridge'
import { LanguageOptions, LocalesFilePath } from '@/constant/app'
import { loadLocale } from '@/lang'
import { sleep } from '@/utils'

export const useAppStore = defineStore('app', () => {
  const isAppExiting = ref(false)
  const isAppReloading = ref(false)

  /* Global Menu */
  const menuShow = ref(false)
  const menuList = ref<App.Menu[]>([])
  const menuPosition = ref({
    x: 0,
    y: 0,
  })

  /* Global Tips */
  const tipsShow = ref(false)
  const tipsMessage = ref('')
  const tipsPosition = ref({
    x: 0,
    y: 0,
  })

  /* Modal Stack */
  const modalStack: (() => void)[] = []
  const modalZIndexCounter = 999
  const modalMinimized = ref<
    {
      id: string
      title: () => string
      openFn: () => void
      closeFn: () => void
      minimizeFn: () => void
    }[]
  >([])

  /* Side-by-side modal tabs */
  const modalTabs = ref<
    {
      id: string
      title: () => string
      activate: () => void
      close: () => void
    }[]
  >([])
  const activeModalTab = ref('')
  const modalSideBySide = ref(false)
  const modalSplitActive = ref(false)

  /* i18n */
  const localesLoading = ref(false)
  const locales = ref<{ label: string; value: string }[]>([])
  const loadLocales = async (delay = true, reload = true) => {
    localesLoading.value = true
    const dirs = await ReadDir(LocalesFilePath).catch(() => [])
    const localLanguage = dirs.flatMap((file) => {
      if (file.isDir) return []
      const [name, ext] = file.name.split('.')
      return name && ext === 'json' ? { label: name, value: name } : []
    })
    locales.value = [...LanguageOptions, ...localLanguage]
    reload && (await loadLocale())
    delay && (await sleep(200))
    localesLoading.value = false
  }

  return {
    isAppExiting,
    isAppReloading,
    menuShow,
    menuPosition,
    menuList,
    tipsShow,
    tipsMessage,
    tipsPosition,
    modalTabs,
    activeModalTab,
    modalSideBySide,
    modalSplitActive,
    modalStack,
    modalMinimized,
    modalZIndexCounter,
    localesLoading,
    locales,
    loadLocales,
  }
})
