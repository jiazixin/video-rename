<script setup lang="ts">
  import { localTimeZone } from '../lib/format/date';
  import { computed, onMounted, ref, watch } from 'vue';
  import { useRoute, useRouter, type RouteLocationNormalizedLoaded } from 'vue-router';
  import { api, bootstrap, session } from './api';
  import { errorText } from './errors';
  import type { ServerRecord } from './contracts';
  import LoginPage from '../features/auth/LoginPage.vue';
  import WorkspaceLayout from '../layouts/WorkspaceLayout.vue';
  import ToastNotice from './ToastNotice.vue';
  const route = useRoute();
  const router = useRouter();
  const loading = ref(true),
    system = ref<{ model: string; ready: boolean; problem?: ServerRecord }>({
      model: '',
      ready: false
    });
  // The library entry returns to the directory the user last browsed.
  let libraryPath = '/library';
  router.afterEach((to) => {
    if (to.name === 'library') libraryPath = to.fullPath;
  });
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
    if (session.value) {
      try {
        system.value = await api('/system');
      } catch {
        // Without the status the workspace still opens; the pages report their own failures.
      }
    }
  }
  onMounted(async () => {
    await bootstrap();
    await loadSystem();
    loading.value = false;
  });
  watch(session, async (value) => {
    if (value) {
      try {
        await api<unknown>('/notifications/timezone', {
          method: 'PUT',
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
  watch(session, loadSystem);
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
  <ToastNotice />
</template>
