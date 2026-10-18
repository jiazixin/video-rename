<script setup lang="ts">
  import { computed, ref } from 'vue';
  import { Folder, Plus, X } from '@lucide/vue';
  import Modal from '../../components/Modal.vue';
  import ModalTransition from '../../components/ModalTransition.vue';
  import DirectoryPicker from './DirectoryPicker.vue';
  const props = defineProps<{
    modelValue: number[];
    paths: Record<number, string>;
    title: string;
    description: string;
    empty: string;
  }>();
  const emit = defineEmits<{ 'update:modelValue': [ids: number[]] }>();
  const open = ref(false),
    draft = ref<number[]>([]),
    knownPaths = ref<Record<number, string>>({});
  const labels = computed(() => ({ ...props.paths, ...knownPaths.value }));
  function choose() {
    draft.value = [...props.modelValue];
    open.value = true;
  }
  function confirm() {
    emit('update:modelValue', [...draft.value]);
    open.value = false;
  }
  function label(id: number) {
    return labels.value[id] || (id === 1 ? '整个媒体库' : `目录 #${id}`);
  }
</script>
<template>
  <section class="scan-directory-selection" :aria-label="title">
    <header
      ><h3
        >{{ title }} <span>{{ modelValue.length }} / 100</span></h3
      ><button type="button" :aria-label="`添加${title}`" @click="choose"
        ><Plus :size="16" />添加目录</button
      ></header
    >
    <p class="help">{{ description }}</p>
    <ul v-if="modelValue.length">
      <li v-for="id in modelValue" :key="id"
        ><Folder :size="16" /><span :title="label(id)">{{ label(id) }}</span
        ><button
          type="button"
          class="icon-btn"
          :aria-label="`从${title}移除 ${label(id)}`"
          @click="
            emit(
              'update:modelValue',
              modelValue.filter((value) => value !== id)
            )
          "
          ><X :size="16" /></button
      ></li>
    </ul>
    <p v-else class="empty compact">{{ empty }}</p>
    <ModalTransition
      ><Modal v-if="open" :title="`选择${title}`" wide @close="open = false">
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
      </Modal></ModalTransition
    >
  </section>
</template>
<style scoped>
  .scan-directory-selection {
    min-width: 0;
    margin-bottom: 22px;
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }
  h3 {
    margin: 0;
    font-size: 13px;
  }
  h3 span {
    color: var(--ash);
    font-weight: 400;
    margin-left: 8px;
  }
  .help {
    margin: 6px 0 12px;
  }
  ul {
    margin: 0;
    padding: 0;
    list-style: none;
    max-height: 320px;
    overflow-y: auto;
    border: 1px solid var(--line);
    border-radius: 8px;
  }
  li {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 12px;
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
    border-radius: 8px;
    background: var(--surface);
  }
</style>
