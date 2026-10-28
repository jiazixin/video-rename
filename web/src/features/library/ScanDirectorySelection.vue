<script setup lang="ts">
  import { computed, ref } from 'vue';
  import { Folder, Plus, X } from '@lucide/vue';
  import ModalDialog from '../../components/ModalDialog.vue';
  import ModalTransition from '../../components/ModalTransition.vue';
  import DirectoryPicker from './DirectoryPicker.vue';
  import { displayPath } from '../../lib/format/path';
  const { paths, title, description, empty } = defineProps<{
    paths: Record<number, string>;
    title: string;
    description: string;
    empty: string;
  }>();
  const model = defineModel<number[]>({ required: true });
  const open = ref(false),
    draft = ref<number[]>([]),
    knownPaths = ref<Record<number, string>>({});
  const labels = computed(() => ({ ...paths, ...knownPaths.value }));
  function choose() {
    draft.value = [...model.value];
    open.value = true;
  }
  function confirm() {
    model.value = [...draft.value];
    open.value = false;
  }
  function label(id: number) {
    const path = labels.value[id];
    if (path) return displayPath(path);
    return id === 1 ? '整个媒体库' : `目录 #${id}`;
  }
</script>
<template>
  <section class="scan-directory-selection" :aria-label="title">
    <header
      ><h3
        >{{ title }} <span>{{ model.length }} / 100</span></h3
      ><button type="button" :aria-label="`添加${title}`" @click="choose"
        ><Plus :size="16" />添加目录</button
      ></header
    >
    <p class="help">{{ description }}</p>
    <ul v-if="model.length">
      <li v-for="id in model" :key="id"
        ><Folder :size="16" /><span :title="label(id)">{{ label(id) }}</span
        ><button
          type="button"
          class="icon-btn"
          :aria-label="`从${title}移除 ${label(id)}`"
          @click="model = model.filter((value) => value !== id)"
          ><X :size="16" /></button
      ></li>
    </ul>
    <p v-else class="empty compact">{{ empty }}</p>
    <ModalTransition
      ><ModalDialog v-if="open" :title="`选择${title}`" wide @close="open = false">
        <p class="help">{{ description }} 确认后加入设置草稿，保存扫描设置后生效。</p>
        <DirectoryPicker v-model="draft" @paths="Object.assign(knownPaths, $event)" />
        <p v-if="draft.length > 100" role="alert" class="notice error">最多选择 100 个目录。</p>
        <template #footer
          ><div class="modal-actions"
            ><button type="button" @click="open = false">取消</button
            ><button type="button" class="primary" :disabled="draft.length > 100" @click="confirm"
              >确认选择</button
            ></div
          ></template
        >
      </ModalDialog></ModalTransition
    >
  </section>
</template>
<style scoped>
  .scan-directory-selection {
    min-width: 0;
    margin-bottom: var(--space-22);
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-12);
  }
  h3 {
    margin: 0;
    font-size: var(--font-size-13);
  }
  h3 span {
    color: var(--ash);
    font-weight: var(--font-weight-400);
    margin-left: var(--space-8);
  }
  .help {
    margin: var(--space-6) 0 var(--space-12);
  }
  ul {
    margin: 0;
    padding: 0;
    list-style: none;
    max-height: 320px;
    overflow-y: auto;
    border: 1px solid var(--line);
    border-radius: var(--radius-8);
  }
  li {
    display: flex;
    align-items: center;
    gap: var(--space-10);
    padding: var(--space-10) var(--space-12);
  }
  li + li {
    border-top: 1px solid var(--line);
  }
  li > svg {
    flex-shrink: 0;
    color: var(--mint);
  }
  li > span {
    flex: 1;
    min-width: 0;
    overflow-wrap: anywhere;
    display: -webkit-box;
    -webkit-line-clamp: 3;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  li > button {
    flex-shrink: 0;
  }
  .empty {
    border: 1px dashed var(--line);
    border-radius: var(--radius-8);
    background: var(--surface);
  }
</style>
