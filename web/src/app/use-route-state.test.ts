import { cleanup, render, waitFor } from '@testing-library/vue'
import { afterEach, expect, it, vi } from 'vitest'
import { defineComponent, nextTick, ref } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { queryChoice, queryIDs, queryInteger, queryText, useRouteState } from './use-route-state'

afterEach(cleanup)
function stateComponent(restore: () => void) {
  return defineComponent({
    setup() {
      restore()
      return () => null
    }
  })
}

it('restores a shared view, updates its URL and restores all fields together after navigation', async () => {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/list', name: 'list', component: { render: () => null } },
      { path: '/other', name: 'other', component: { render: () => null } }
    ]
  })
  await router.push('/list?q=birds&page=3&kind=video&selected=2,5&unowned=keep')
  const query = ref(''),
    page = ref(1),
    kind = ref('all'),
    selected = ref(new Set<number>())
  const restored = vi.fn()
  const component = stateComponent(() => {
    useRouteState(
      {
        q: queryText(query),
        page: queryInteger(page, { min: 1 }),
        kind: queryChoice(kind, ['all', 'video']),
        selected: queryIDs(selected)
      },
      { onRestore: restored }
    )
  })
  render(component, { global: { plugins: [router] } })
  expect([query.value, page.value, kind.value, [...selected.value]]).toEqual([
    'birds',
    3,
    'video',
    [2, 5]
  ])
  query.value = 'cats'
  await waitFor(() => {
    expect(router.currentRoute.value.query.q).toBe('cats')
  })
  expect(router.currentRoute.value.query.unowned).toBe('keep')
  expect(restored).not.toHaveBeenCalled()
  await router.push('/other')
  query.value = 'parked'
  await nextTick()
  expect(router.currentRoute.value.fullPath).toBe('/other')
  router.back()
  await waitFor(() => {
    expect(query.value).toBe('cats')
  })
  expect(page.value).toBe(3)
  expect(restored).toHaveBeenCalledOnce()
  await router.push('/list?q=flowers&page=7&kind=video')
  await waitFor(() => {
    expect(restored).toHaveBeenCalledTimes(2)
  })
  expect([query.value, page.value, [...selected.value]]).toEqual(['flowers', 7, []])
})

it('rejects invalid and duplicate query values and stops writing when disposed', async () => {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/list', component: { render: () => null } }]
  })
  await router.push('/list?page=9007199254740992&q=one&q=two&kind=unknown&selected=1,-2,abc,3')
  const query = ref(''),
    page = ref(1),
    kind = ref('all'),
    selected = ref(new Set<number>())
  const view = render(
    stateComponent(() => {
      useRouteState({
        q: queryText(query),
        page: queryInteger(page, { min: 1 }),
        kind: queryChoice(kind, ['all', 'video']),
        selected: queryIDs(selected)
      })
    }),
    { global: { plugins: [router] } }
  )
  expect([query.value, page.value, kind.value, [...selected.value]]).toEqual(['', 1, 'all', [1, 3]])
  view.unmount()
  const path = router.currentRoute.value.fullPath
  query.value = 'after disposal'
  await nextTick()
  expect(router.currentRoute.value.fullPath).toBe(path)
})
