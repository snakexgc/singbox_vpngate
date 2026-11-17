import { useI18n } from 'vue-i18n'

import { getVPNGateText } from './vpngateLabels'

// Read locale during render so labels follow the existing language selection.
export const useVPNGateText = () => {
  const { locale } = useI18n()
  return (value: string) => getVPNGateText(value, locale.value)
}
