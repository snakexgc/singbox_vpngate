import type { RouteRecordRaw } from 'vue-router'

import HomeView from '@/views/HomeView/index.vue'
import RoutingView from '@/views/RoutingView/index.vue'
import SettingsView from '@/views/SettingsView/index.vue'
import VPNGateView from '@/views/VPNGateView/index.vue'

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'Overview',
    component: HomeView,
    meta: { name: 'router.overview', icon: 'overview', hidden: false },
  },
  {
    path: '/vpngate',
    name: 'VPNGate',
    component: VPNGateView,
    meta: { name: 'VPNGate', icon: 'subscriptions', hidden: false },
  },
  {
    path: '/routing',
    name: 'Routing',
    component: RoutingView,
    meta: { name: 'router.routing', icon: 'rulesets', hidden: false },
  },
  {
    path: '/settings',
    name: 'Settings',
    component: SettingsView,
    meta: { name: 'router.settings', icon: 'settings2', hidden: false },
  },
  { path: '/:pathMatch(.*)*', redirect: '/', meta: { name: '', hidden: true } },
]
export default routes
