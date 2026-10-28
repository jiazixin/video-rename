<script setup lang="ts">
  import { localTimeZone } from '../lib/format/date';
  import { computed, onMounted, onScopeDispose, ref, watch } from 'vue';
  import { useRoute, useRouter, type RouteLocationNormalizedLoaded } from 'vue-router';
  import { api, bootstrap, session } from './api';
  import { errorText } from './errors';
  import type { ServerRecord } from './contracts';
  import LoginPage from '../features/auth/LoginPage.vue';
  import WorkspaceLayout from '../layouts/WorkspaceLayout.vue';
  import ToastNotice from '../components/ToastNotice.vue';
  import { closeToast, toasts, toastsAnimated } from './use-toast';
  const route = useRoute();
  const router = useRouter();
  const lifetime = new AbortController();
  let systemController: AbortController | undefined;
  onScopeDispose(() => {
    lifetime.abort();
    systemController?.abort();
  });
  const loading = ref(true),
    system = ref<{ model: string; ready: boolean; problem?: ServerRecord }>({
      model: '',
      ready: false
    });
  // The library entry returns to the directory the user last browsed.
  let libraryPath = '/library';
  const stopRouteTracking = router.afterEach((to) => {
    if (to.name === 'library') libraryPath = to.fullPath;
  });
  onScopeDispose(stopRouteTracking);
  function navigate(target: string) {
    void router.push(target === 'library' ? libraryPath : `/${target}`);
  }
  const activePage = computed(() => {
    if (route.name === 'usage')
      return `usage/${route.params.tab === 'analysis' ? 'analysis' : 'tokens'}`;
    return typeof route.name === 'string' ? route.name : 'library';
  });
  // Page-specific props and listeners; RouterView passes the route's own props.
  function pageAttrs(page: RouteLocationNormalizedLoaded) {
    if (page.name === 'library')
      return {
        ready: system.value.ready,
        onRun: () => {
          navigate('runs');
        }
      };
    if (page.name === 'runs' || page.name === 'plans') return { ready: system.value.ready };
    if (page.name === 'settings') return { onChanged: loadSystem };
    return {};
  }
  async function loadSystem() {
    systemController?.abort();
    if (session.value) {
      const controller = new AbortController();
      systemController = controller;
      try {
        const result = await api<typeof system.value>('/system', { signal: controller.signal });
        if (!controller.signal.aborted) system.value = result;
      } catch {
        // Without the status the workspace still opens; the pages report their own failures.
      } finally {
        if (systemController === controller) systemController = undefined;
      }
    } else {
      system.value = { model: '', ready: false };
    }
  }
  onMounted(async () => {
    await bootstrap(lifetime.signal);
    if (lifetime.signal.aborted) return;
    await loadSystem();
    loading.value = false;
  });
  watch(session, async (value, _previous, onCleanup) => {
    if (value) {
      const controller = new AbortController();
      onCleanup(() => {
        controller.abort();
      });
      try {
        await api<unknown>('/notifications/timezone', {
          method: 'PUT',
          signal: controller.signal,
          body: {
            timezone: localTimeZone()
          }
        });
      } catch {
        // Notification times fall back to the stored timezone; nothing to show the user.
      }
    }
  });
  // The leaving page fades out for a moment; keep it out of focus and assistive tech meanwhile.
  // The kept-alive library reuses its element, so entering clears the marks again.
  function retirePage(el: Element) {
    el.setAttribute('aria-hidden', 'true');
    (el as HTMLElement).inert = true;
  }
  function restorePage(el: Element) {
    el.removeAttribute('aria-hidden');
    (el as HTMLElement).inert = false;
  }
  watch(session, (_value, _previous, onCleanup) => {
    onCleanup(() => {
      systemController?.abort();
    });
    void loadSystem();
  });
</script>
<template>
  <div v-if="loading" class="boot">正在打开工作台…</div
  ><LoginPage v-else-if="!session" /><WorkspaceLayout
    v-else
    :page="activePage"
    :model="system.model"
    :ready="system.ready"
    @navigate="navigate"
    ><div v-if="system.problem" class="notice error"
      >{{ errorText(system.problem) }}。文件浏览和手动改名仍可使用。</div
    ><RouterView v-slot="{ Component, route: page }"
      ><Transition name="page" mode="out-in" @before-leave="retirePage" @before-enter="restorePage"
        ><KeepAlive include="LibraryPage"
          ><component
            :is="Component"
            :key="page.name"
            v-bind="pageAttrs(page)" /></KeepAlive></Transition
    ></RouterView>
  </WorkspaceLayout>
  <ToastNotice :notices="toasts" :animated="toastsAnimated" @close="closeToast" />
</template>

<style scoped>
  :global(.page-enter-active) {
    transition:
      opacity var(--motion-enter) linear,
      transform var(--motion-enter) linear;
  }
  :global(.page-leave-active) {
    transition: opacity var(--motion-exit) linear;
    pointer-events: none;
  }
  :global(.page-enter-from) {
    opacity: 0;
    transform: translateY(6px);
  }
  :global(.page-leave-to) {
    opacity: 0;
  }

  .boot {
    display: grid;
    place-items: center;
    height: 100vh;
    color: var(--ash);
  }
</style>
