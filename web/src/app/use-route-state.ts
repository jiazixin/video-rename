import { nextTick, onScopeDispose, readonly, ref, watch, type Ref } from 'vue'
import { useRoute, useRouter, type LocationQuery, type LocationQueryRaw } from 'vue-router'

/** One validated, shareable query value. Local UI state stays synchronous. */
export type RouteBinding = {
  read: () => string | undefined
  restore: (value: string | undefined) => void
}

type RouteStateOptions = {
  /** Called after all fields restore together; never called for our own URL writes. */
  onRestore?: () => void
  /** Tabs that reuse a route name can own only their current path. */
  exactPath?: boolean
  canWrite?: () => boolean
}

/** Synchronizes query values without letting a parked page modify another route. */
export function useRouteState(
  bindings: Record<string, RouteBinding>,
  options: RouteStateOptions = {}
) {
  const route = useRoute()
  const router = useRouter()
  const owner = route.name
  const path = route.path
  const isRestoring = ref(false)
  let isDisposed = false
  let revision = 0
  let previousPath = route.path
  const ownsRoute = () => route.name === owner && (!options.exactPath || route.path === path)
  const snapshot = () =>
    Object.fromEntries(Object.entries(bindings).map(([key, field]) => [key, field.read()]))
  const equal = (left: LocationQueryRaw, right: LocationQueryRaw) =>
    JSON.stringify(left) === JSON.stringify(right)
  function readQuery(query: LocationQuery) {
    for (const [key, field] of Object.entries(bindings)) {
      const value = query[key]
      field.restore(typeof value === 'string' ? value : undefined)
    }
  }
  // Initial restoration precedes the page's first request and its filter watchers.
  if (ownsRoute()) readQuery(route.query)
  watch(
    () => route.fullPath,
    async () => {
      if (!ownsRoute()) return
      const before = snapshot()
      const ownRevision = ++revision
      isRestoring.value = true
      readQuery(route.query)
      const hasChanged = !equal(before, snapshot()) || previousPath !== route.path
      previousPath = route.path
      await nextTick()
      if (isDisposed || ownRevision !== revision) return
      isRestoring.value = false
      if (hasChanged) options.onRestore?.()
    },
    { flush: 'post' }
  )
  function query(): LocationQueryRaw {
    return {
      ...Object.fromEntries(Object.entries(route.query).filter(([key]) => !(key in bindings))),
      ...snapshot()
    }
  }
  watch(
    snapshot,
    () => {
      if (
        isDisposed ||
        isRestoring.value ||
        !ownsRoute() ||
        (options.canWrite && !options.canWrite())
      )
        return
      const next = query()
      if (equal(next, route.query)) return
      // Query updates preserve the current history entry, so returning from another
      // page restores this page's latest view without a history entry per keystroke.
      void router.replace({ query: next }).catch(() => {
        console.warn('route_state_sync_failed')
      })
    },
    { flush: 'post' }
  )
  onScopeDispose(() => {
    isDisposed = true
    revision++
  })
  return { isRestoring: readonly(isRestoring), query }
}

/** Encodes a bounded search or date input, omitting its initial default. */
export function queryText(
  state: Ref<string>,
  validate: (value: string) => boolean = () => true
): RouteBinding {
  const fallback = state.value
  return {
    read: () => (state.value === fallback ? undefined : state.value),
    restore: (value) => {
      state.value =
        value !== undefined && value.length <= 2000 && validate(value) ? value : fallback
    }
  }
}

/** Encodes one known enum value; unknown values restore the initial default. */
export function queryChoice<T extends string>(state: Ref<T>, choices: readonly T[]): RouteBinding {
  const fallback = state.value
  return {
    read: () => (state.value === fallback ? undefined : state.value),
    restore: (value) => {
      state.value = choices.find((choice) => choice === value) ?? fallback
    }
  }
}

/** Encodes a safe integer and validates its permitted range. */
export function queryInteger(
  state: Ref<number>,
  bounds: { min?: number; max?: number; choices?: readonly number[] } = {}
): RouteBinding {
  const fallback = state.value
  return {
    read: () => (state.value === fallback ? undefined : String(state.value)),
    restore: (value) => {
      const number = value && /^-?\d+$/.test(value) ? Number(value) : NaN
      state.value =
        Number.isSafeInteger(number) &&
        number >= (bounds.min ?? 0) &&
        number <= (bounds.max ?? Number.MAX_SAFE_INTEGER) &&
        (!bounds.choices || bounds.choices.includes(number))
          ? number
          : fallback
    }
  }
}

/** Encodes an explicit selection; no operation is performed when it is restored. */
export function queryIDs(state: Ref<Set<number>>): RouteBinding {
  return {
    read: () => (state.value.size ? [...state.value].sort((a, b) => a - b).join(',') : undefined),
    restore: (value) => {
      const ids = value?.split(',') ?? []
      state.value = new Set(
        ids.length <= 5000
          ? ids
              .filter((id) => /^\d+$/.test(id))
              .map(Number)
              .filter((id) => Number.isSafeInteger(id) && id > 0)
          : []
      )
    }
  }
}

/** Encodes a nonsecret UI toggle. */
export function queryBoolean(state: Ref<boolean>): RouteBinding {
  const fallback = state.value
  return {
    read: () => (state.value === fallback ? undefined : String(state.value)),
    restore: (value) => {
      state.value = value === 'true' || (value !== 'false' && fallback)
    }
  }
}
