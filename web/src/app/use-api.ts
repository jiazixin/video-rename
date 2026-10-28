import { getCurrentInstance, onActivated, onDeactivated, onScopeDispose, shallowRef } from 'vue'
import { api, ApiError, type ApiOptions } from './api'
import { errorText } from './errors'
import type { FieldError } from './server-codes'
import { useToast } from './use-toast'

/** Binds requests to the owning scope and the active lifetime of a kept-alive page. */
export function useApi() {
  const formError = shallowRef<unknown>()
  const toast = getCurrentInstance() ? useToast() : undefined
  let lifetime = new AbortController()
  onScopeDispose(() => {
    lifetime.abort()
  })
  if (getCurrentInstance()) {
    onDeactivated(() => {
      lifetime.abort()
    })
    onActivated(() => {
      if (lifetime.signal.aborted) lifetime = new AbortController()
    })
  }

  function invalidFields(fields: FieldError[]): ApiError {
    const error = new ApiError('invalid form fields', {
      status: 400,
      code: 'invalid_argument',
      requestId: '',
      details: { fields }
    })
    formError.value = error
    toast?.show(errorText(error), 'error')
    return error
  }

  async function request<T>(path: string, options: ApiOptions = {}): Promise<T> {
    const signal = options.signal
      ? AbortSignal.any([lifetime.signal, options.signal])
      : lifetime.signal
    signal.throwIfAborted()
    const isWrite = options.method !== undefined && !['GET', 'HEAD'].includes(options.method)
    if (isWrite) {
      formError.value = undefined
      toast?.clear()
    }
    try {
      const result = await api<T>(path, { ...options, signal })
      // A completed body can race with disposal even after fetch has resolved.
      signal.throwIfAborted()
      return result
    } catch (error) {
      if (isWrite && !signal.aborted) {
        if (error instanceof ApiError && error.status === 400) formError.value = error
        toast?.show(errorText(error), 'error')
      }
      throw error
    }
  }

  async function items<T>(path: string, options?: ApiOptions): Promise<T[]> {
    return (await request<{ items: T[] }>(path, options)).items
  }

  return { api: request, apiItems: items, formError, invalidFields }
}
