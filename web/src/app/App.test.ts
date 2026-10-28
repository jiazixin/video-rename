import { h } from 'vue'
import { cleanup, render, screen, waitFor } from '@testing-library/vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from './App.vue'
import { setSession } from './api'
import { deferred } from '../test/deferred'

type PendingRequest = {
  path: string
  signal: AbortSignal
  response: ReturnType<typeof deferred<Response>>
}

afterEach(() => {
  cleanup()
  setSession(null)
  vi.unstubAllGlobals()
})

async function setup() {
  const requests: PendingRequest[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const address = input instanceof URL ? input.href : input
      const path = typeof address === 'string' ? address : address.url
      if (path === '/api/auth/session') {
        return Promise.resolve(
          Response.json({ error: { code: 'unauthenticated' } }, { status: 401 })
        )
      }
      if (!init?.signal) throw new Error(`missing cancellation signal for ${path}`)
      const response = deferred<Response>()
      requests.push({ path, signal: init.signal, response })
      return response.promise
    })
  )
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/library',
        name: 'library',
        component: Object.assign(() => h('p', '文件目录夹具'), { displayName: 'LibraryFixture' })
      }
    ]
  })
  await router.push('/library')
  await router.isReady()
  const view = render(App, { global: { plugins: [router] } })
  await screen.findByRole('heading', { name: '登录工作台' })
  return { requests, view }
}

describe('App session lifecycle', () => {
  it('cancels the status and timezone requests when the component unmounts', async () => {
    const { requests, view } = await setup()
    setSession({ username: 'admin' })
    await screen.findByText('文件目录夹具')
    await waitFor(() => {
      expect(requests.map((request) => request.path).sort()).toEqual([
        '/api/notifications/timezone',
        '/api/system'
      ])
    })
    view.unmount()
    for (const request of requests) {
      expect(request.signal.aborted).toBe(true)
      request.response.resolve(Response.json({ model: 'retired model', ready: true }))
    }
  })

  it('keeps a late status response from a signed-out session out of the next workspace', async () => {
    const { requests } = await setup()
    setSession({ username: 'first' })
    await screen.findByText('文件目录夹具')
    await waitFor(() => {
      expect(requests).toHaveLength(2)
    })
    const oldRequests = [...requests]
    setSession(null)
    await screen.findByRole('heading', { name: '登录工作台' })
    for (const request of oldRequests) expect(request.signal.aborted).toBe(true)
    setSession({ username: 'second' })
    await screen.findByText('文件目录夹具')
    await waitFor(() => {
      expect(requests).toHaveLength(4)
    })
    for (const request of requests.slice(2)) {
      request.response.resolve(Response.json({ model: 'current model', ready: true }))
    }
    await screen.findByText('current model')
    for (const request of oldRequests) {
      request.response.resolve(Response.json({ model: 'retired model', ready: true }))
    }
    // Let the settled fetch and response-body promises finish before checking the old result.
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 0)
    })
    await waitFor(() => {
      expect(screen.getByText('current model')).toBeInTheDocument()
      expect(screen.queryByText('retired model')).not.toBeInTheDocument()
    })
  })
})
