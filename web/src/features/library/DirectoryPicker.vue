<script setup lang="ts">
  import './library.css';
  import { errorText } from '../../app/errors';
  import PageNavigation from '../../components/PageNavigation.vue';
  import { ref, onMounted, onUnmounted, computed } from 'vue';
  import { Folder, ChevronRight, Home } from '@lucide/vue';
  import DirectoryStatus from './DirectoryStatus.vue';
  import './directory-picker.css';
  import { api } from '../../app/api';
  import { type Page } from '../../app/contracts';
  const model = defineModel<number[]>({ required: true });
  const emit = defineEmits<{
    paths: [paths: Record<number, string>];
  }>();
  const selectedOnPage = computed(
    () => page.value?.items.filter((e) => model.value.includes(e.id)).length || 0
  );
  function selectPage(action: 'all' | 'invert' | 'clear') {
    if (busy.value || error.value) return;
    const ids = new Set(model.value);
    for (const e of page.value?.items || []) {
      if (action === 'clear' || (action === 'invert' && ids.has(e.id))) ids.delete(e.id);
      else ids.add(e.id);
    }
    model.value = [...ids];
  }
  const pageNumber = ref(1);
  const dir = ref(1),
    page = ref<Page>(),
    error = ref(''),
    busy = ref(false);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let serial = 0;
  async function load() {
    const seq = ++serial;
    busy.value = true;
    error.value = '';
    clearTimeout(timer);
    try {
      const result = await api<Page>(
        `/directories/${dir.value}/entries?kind=directory&page=${pageNumber.value}&limit=50`
      );
      if (seq !== serial) return;
      page.value = result;
      const prefix = result.breadcrumbs
        .filter((crumb) => crumb.id !== 1)
        .map((crumb) => crumb.name)
        .join(' / ');
      const paths: Record<number, string> = { [result.directory.id]: prefix || '整个媒体库' };
      for (const entry of result.items)
        paths[entry.id] = prefix ? `${prefix} / ${entry.name}` : entry.name;
      emit('paths', paths);
      pageNumber.value = result.page ?? pageNumber.value;
      if (result.refreshing) timer = setTimeout(() => void load(), 1200);
    } catch (e) {
      if (seq === serial) error.value = errorText(e);
    } finally {
      if (seq === serial) busy.value = false;
    }
  }
  function enter(id: number) {
    dir.value = id;
    pageNumber.value = 1;
    void load();
  }
  function toggle(id: number) {
    if (busy.value || error.value) return;
    model.value = model.value.includes(id)
      ? model.value.filter((x) => x !== id)
      : [...model.value, id];
  }
  onMounted(load);
  onUnmounted(() => {
    serial++;
    clearTimeout(timer);
  });
</script>
<template>
  <div class="directory-picker" :aria-busy="busy || page?.refreshing || undefined"
    ><div class="picker-breadcrumb" role="navigation" aria-label="目录路径"
      ><button type="button" @click="enter(1)"><Home :size="15" />媒体库</button
      ><template v-for="crumb in page?.breadcrumbs.filter((x) => x.id !== 1)" :key="crumb.id"
        ><ChevronRight :size="13" /><button type="button" @click="enter(crumb.id)">{{
          crumb.name
        }}</button></template
      ></div
    ><label class="picker-current"
      ><input
        type="checkbox"
        aria-label="选择当前目录"
        :disabled="busy || !!error"
        :checked="model.includes(dir)"
        @change="toggle(dir)" /><span class="picker-current-name"
        ><span>选择当前目录</span>
        <strong :title="page?.directory.name || '整个媒体库'">{{
          page?.directory.name || '整个媒体库'
        }}</strong></span
      ><DirectoryStatus v-if="page" :entry="page.directory" /></label
    ><div v-if="error" class="notice error"
      >{{ error
      }}<button
        type="button"
        @click="
          pageNumber = 1;
          void load();
        "
        >重试</button
      ></div
    ><div class="picker-list"
      ><div v-if="!page?.items.length" class="empty compact">{{
        busy || page?.refreshing ? '正在读取目录…' : '没有子目录'
      }}</div
      ><div v-for="entry in page?.items" :key="entry.id" class="picker-row"
        ><input
          type="checkbox"
          :disabled="busy || !!error"
          :aria-label="`选择 ${entry.name}`"
          :checked="model.includes(entry.id)"
          @change="toggle(entry.id)" /><button
          type="button"
          :aria-label="entry.name"
          @click="enter(entry.id)"
          ><Folder :size="18" /><span class="picker-entry-name" :title="entry.name">{{
            entry.name
          }}</span
          ><DirectoryStatus :entry="entry" /><ChevronRight :size="15" /></button></div></div
    ><div class="picker-bottom"
      ><span
        >本页已选 {{ selectedOnPage }} / {{ page?.items.length || 0 }} · 共选
        {{ model.length }} 个目录</span
      ><div class="picker-selection"
        ><button
          type="button"
          :disabled="busy || !!error || !page?.items.length"
          @click="selectPage('all')"
          >全选本页</button
        ><button
          type="button"
          :disabled="busy || !!error || !page?.items.length"
          @click="selectPage('invert')"
          >反选本页</button
        ><button
          type="button"
          :disabled="busy || !!error || !selectedOnPage"
          @click="selectPage('clear')"
          >取消本页选择</button
        ></div
      ><PageNavigation
        :page="pageNumber"
        :total-pages="Math.ceil((page?.total || 0) / 50)"
        :disabled="busy"
        previous-label="上页目录"
        next-label="下页目录"
        @change="
          pageNumber = $event;
          void load();
        " /></div
  ></div>
</template>
