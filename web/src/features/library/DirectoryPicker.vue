<script setup lang="ts">
  import PageNavigation from '../../components/PageNavigation.vue';
  import { ref, onMounted, onUnmounted, computed } from 'vue';
  import { Folder, ChevronRight, Home, ChevronLeft, RefreshCw } from 'lucide-vue-next';
  import DirectoryStatus from './DirectoryStatus.vue';
  import './directory-picker.css';
  import { api, type Page } from '../../app/api';
  const props = defineProps<{ modelValue: number[] }>();
  const emit = defineEmits<{ 'update:modelValue': [ids: number[]] }>();
  const selectedOnPage = computed(
    () => page.value?.items.filter((e) => props.modelValue.includes(e.id)).length || 0
  );
  function selectPage(action: 'all' | 'invert' | 'clear') {
    if (busy.value || error.value) return;
    const ids = new Set(props.modelValue);
    for (const e of page.value?.items || []) {
      if (action === 'clear' || (action === 'invert' && ids.has(e.id))) ids.delete(e.id);
      else ids.add(e.id);
    }
    emit('update:modelValue', [...ids]);
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
      pageNumber.value = result.page ?? pageNumber.value;
      if (result.refreshing) timer = setTimeout(load, 1200);
    } catch (e) {
      if (seq === serial) error.value = (e as Error).message;
    } finally {
      if (seq === serial) busy.value = false;
    }
  }
  function enter(id: number) {
    dir.value = id;
    pageNumber.value = 1;
    load();
  }
  function toggle(id: number) {
    if (busy.value || error.value) return;
    emit(
      'update:modelValue',
      props.modelValue.includes(id)
        ? props.modelValue.filter((x) => x !== id)
        : [...props.modelValue, id]
    );
  }
  onMounted(load);
  onUnmounted(() => {
    serial++;
    clearTimeout(timer);
  });
</script>
<template>
  <div class="directory-picker"
    ><div class="picker-breadcrumb"
      ><button type="button" @click="enter(1)"><Home :size="15" />媒体库</button
      ><template v-for="crumb in page?.breadcrumbs.filter((x) => x.id !== 1)" :key="crumb.id"
        ><ChevronRight :size="13" /><button type="button" @click="enter(crumb.id)">{{
          crumb.name
        }}</button></template
      ></div
    ><label class="picker-current"
      ><input
        type="checkbox"
        :disabled="busy || !!error"
        :checked="modelValue.includes(dir)"
        @change="toggle(dir)" />选择当前目录
      <strong>{{ page?.directory.name || '整个媒体库' }}</strong
      ><DirectoryStatus v-if="page" :entry="page.directory" /></label
    ><div v-if="error" class="notice error"
      >{{ error
      }}<button
        type="button"
        @click="
          pageNumber = 1;
          load();
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
          :checked="modelValue.includes(entry.id)"
          @change="toggle(entry.id)" /><button
          type="button"
          :aria-label="entry.name"
          @click="enter(entry.id)"
          ><Folder :size="18" /><span class="picker-entry-name">{{ entry.name }}</span
          ><DirectoryStatus :entry="entry" /><ChevronRight :size="15" /></button></div></div
    ><div class="picker-bottom"
      ><span
        >本页已选 {{ selectedOnPage }} / {{ page?.items.length || 0 }} · 共选
        {{ modelValue.length }} 个目录</span
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
          load();
        " /></div
  ></div>
</template>
