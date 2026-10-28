import { createRouter, createWebHistory, type RouteLocationNormalized } from 'vue-router'

const settingsTabs = [
  'processing',
  'scan',
  'numbering',
  'prompts',
  'models',
  'sampling',
  'notifications'
]

function param(route: RouteLocationNormalized, name: string): string {
  const value = route.params[name]
  return typeof value === 'string' ? value : ''
}

// The Go asset handler falls back to index.html for application routes.
export const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    { path: '/', redirect: '/library' },
    {
      path: '/library/:directory(\\d+)?',
      name: 'library',
      component: () => import('../features/library/LibraryPage.vue')
    },
    {
      path: '/history',
      name: 'history',
      component: () => import('../features/history/HistoryPage.vue')
    },
    {
      path: '/extractions',
      name: 'extractions',
      component: () => import('../features/history/ExtractionHistory.vue')
    },
    {
      path: '/plans',
      name: 'plans',
      component: () => import('../features/runs/RunsPage.vue'),
      props: { view: 'plans' }
    },
    {
      path: '/runs',
      name: 'runs',
      component: () => import('../features/runs/RunsPage.vue'),
      props: { view: 'runs' }
    },
    {
      path: '/usage/:tab(tokens|analysis)?',
      name: 'usage',
      component: () => import('../features/usage/UsagePage.vue'),
      props: (route) => ({ tab: param(route, 'tab') || 'tokens' })
    },
    {
      path: '/settings/:tab?',
      name: 'settings',
      component: () => import('../features/settings/SystemSettingsPage.vue'),
      props: (route) => {
        const tab = param(route, 'tab')
        return { tab: settingsTabs.includes(tab) ? tab : 'processing' }
      }
    },
    { path: '/:unknown(.*)*', redirect: '/library' }
  ]
})
