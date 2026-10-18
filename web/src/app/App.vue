<script setup lang="ts">
  import { onMounted, onUnmounted, ref, watch } from 'vue';
  import { api, bootstrap, session } from './api';
  import Login from '../features/auth/Login.vue';
  import Workspace from '../layouts/Workspace.vue';
  import Library from '../features/library/Library.vue';
  import History from '../features/history/History.vue';
  import ExtractionHistory from '../features/history/ExtractionHistory.vue';
  import Runs from '../features/runs/Runs.vue';
  import Usage from '../features/usage/Usage.vue';
  import SystemSettings from './SystemSettings.vue';
  const settingsTab = ref('processing'),
    usageTab = ref('tokens');
  const page = ref('library'),
    loading = ref(true),
    system = ref({ model: '', ready: false, message: '' });
  let libraryHash = location.hash.startsWith('#library/') ? location.hash.slice(1) : 'library';
  function navigate(v: string) {
    location.hash = v === 'library' ? libraryHash : v;
    hash();
  }
  function hash() {
    if (location.hash.startsWith('#library')) libraryHash = location.hash.slice(1);
    const [name, tab] = location.hash.slice(1).split('/');
    page.value = [
      'library',
      'history',
      'extractions',
      'plans',
      'runs',
      'usage',
      'settings'
    ].includes(name || '')
      ? name!
      : 'library';
    settingsTab.value = [
      'processing',
      'scan',
      'numbering',
      'prompts',
      'models',
      'sampling',
      'notifications'
    ].includes(tab || '')
      ? tab!
      : 'processing';
    usageTab.value = ['tokens', 'analysis'].includes(tab || '') ? tab! : 'tokens';
  }
  async function loadSystem() {
    if (session.value) {
      try {
        system.value = await api('/system');
      } catch {}
    }
  }
  onMounted(async () => {
    hash();
    window.addEventListener('hashchange', hash);
    await bootstrap();
    await loadSystem();
    loading.value = false;
  });
  watch(session, async (value) => {
    if (value) {
      try {
        await api('/notifications/timezone', 'PUT', {
          timezone: Intl.DateTimeFormat().resolvedOptions().timeZone
        });
      } catch {}
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
  onUnmounted(() => window.removeEventListener('hashchange', hash));
  watch(session, loadSystem);
</script>
<template>
  <div v-if="loading" class="boot">正在打开工作台…</div><Login v-else-if="!session" /><Workspace
    v-else
    :page="page === 'usage' ? `usage/${usageTab}` : page"
    :model="system.model"
    :ready="system.ready"
    @navigate="navigate"
    ><div v-if="system.message" class="notice error"
      >{{ system.message }}。文件浏览和手动改名仍可使用。</div
    ><Transition name="page" mode="out-in" @before-leave="retirePage" @before-enter="restorePage"
      ><KeepAlive include="Library"
        ><Library v-if="page === 'library'" :ready="system.ready" @run="navigate('runs')" /><History
          v-else-if="page === 'history'" /><ExtractionHistory
          v-else-if="page === 'extractions'" /><SystemSettings
          v-else-if="page === 'settings'"
          :tab="settingsTab"
          @changed="loadSystem" /><Runs
          v-else-if="page === 'runs' || page === 'plans'"
          :key="page"
          :view="page"
          :ready="system.ready" /><Usage
          v-else-if="page === 'usage'"
          :tab="usageTab" /></KeepAlive></Transition
  ></Workspace>
</template>
