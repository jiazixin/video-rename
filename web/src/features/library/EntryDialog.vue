<script setup lang="ts">
  import './library.css';
  import { computed } from 'vue';
  import { ArrowRight, FileVideo, Folder, Undo2 } from '@lucide/vue';
  import ModalDialog from '../../components/ModalDialog.vue';
  import DateTime from '../../components/DateTime.vue';
  import { ModelPicker } from '../models';
  import type { Entry, Operation } from '../../app/contracts';
  import type { EntryDialogMode } from './entry-dialog';
  // One entry's rename, restore, run submission or naming history.
  const { mode, entry, history, extension, error, saving } = defineProps<{
    mode: EntryDialogMode;
    entry: Entry;
    history: Operation[];
    extension: string;
    error: string;
    saving: boolean;
  }>();
  const newName = defineModel<string>('newName', { required: true });
  const modelConfigID = defineModel<number>('modelConfigId', { required: true });
  const recursive = defineModel<boolean>('recursive', { required: true });
  const emit = defineEmits<{ close: []; submit: [] }>();
  const titles: Record<string, string> = {
    extract: '目录抽帧',
    rename: '重命名',
    restore: '还原名称',
    history: '命名历史'
  };
  const title = computed(() => titles[mode] ?? '自动命名');
  const historySources: Record<string, string> = { restore: '还原', manual: '手动命名' };
  const historySource = (source: string) => historySources[source] ?? '自动命名';
  const submitLabels: Record<string, string> = {
    rename: '保存名称',
    restore: '确认还原',
    extract: '抽帧'
  };
  const submitLabel = computed(() => {
    if (saving) return '正在提交…';
    return submitLabels[mode] ?? '重命名';
  });
  const restoreName = computed(
    () => history.find((x) => x.id === entry.restore_id)?.old_name || '正在读取…'
  );
  const blocked = computed(
    () =>
      saving || (mode === 'restore' && !history.length) || (mode === 'auto' && !modelConfigID.value)
  );
</script>

<template>
  <ModalDialog :title="title" @close="!saving && emit('close')"
    ><form @submit.prevent="emit('submit')">
      <div class="selection-summary"
        ><Folder v-if="entry.kind === 'directory'" :size="23" /><FileVideo
          v-else
          :size="23"
        /><strong>{{ entry.name || '整个媒体库' }}</strong></div
      >
      <template v-if="mode === 'rename'"
        ><label
          >新名称<div class="extension-input"
            ><input v-model="newName" aria-label="新名称" required maxlength="240" /><span>{{
              extension
            }}</span></div
          ></label
        ><p class="help">只修改名称，文件保留在原目录。成功后可以还原。</p></template
      >
      <template v-else-if="mode === 'restore'"
        ><p>将恢复到最近一次改名前的名称：</p
        ><div class="restore-target"><Undo2 :size="18" />{{ restoreName }}</div
        ><p class="help"
          >还原后可再次自动命名，也会被后续计划重新处理。不会覆盖同名文件。</p
        ></template
      >
      <template v-else-if="mode === 'auto' || mode === 'extract'"
        ><ModelPicker v-if="mode === 'auto'" v-model="modelConfigID" /><p>{{
          mode === 'extract'
            ? '为当前目录内的视频预先生成截图，之后重命名可直接复用缓存。'
            : '后台将抽取视频画面，由指定模型生成名称并自动修改。'
        }}</p
        ><label v-if="entry.kind === 'directory'" class="check-label"
          ><input v-model="recursive" type="checkbox" role="switch" />包含子目录</label
        ><p class="help">{{
          mode === 'extract'
            ? '覆盖目录内全部分页的视频，包括已处理视频；已有有效截图直接复用。进度见“批次记录”，结果见“抽帧记录”。'
            : '已命名的视频自动跳过。进度见“批次记录”，命名结果见“运行记录”。'
        }}</p></template
      >
      <template v-else
        ><div v-for="h in history" :key="h.id" class="history-item"
          ><span class="badge">{{ historySource(h.source) }} · {{ h.status }}</span
          ><p>{{ h.old_name }} <ArrowRight :size="14" /> {{ h.new_name }}</p
          ><DateTime :value="h.created" /></div
        ><p v-if="!history.length" class="empty compact">暂无命名历史</p></template
      >
      <div v-if="error" class="notice error" role="alert">{{ error }}</div
      ><div class="modal-actions"
        ><button type="button" :disabled="saving" @click="emit('close')">{{
          mode === 'history' ? '关闭' : '取消'
        }}</button
        ><button v-if="mode !== 'history'" class="primary" :disabled="blocked">{{
          submitLabel
        }}</button></div
      ></form
    ></ModalDialog
  >
</template>

<style scoped>
  .selection-summary {
    padding: 15px;
    background: var(--control-hover);
    border-radius: 8px;
    display: flex;
    gap: 12px;
    align-items: center;
    margin-bottom: 22px;
    overflow-wrap: anywhere;
  }
  .selection-summary svg {
    color: var(--palette-7a9284);
  }
  .selection-summary strong {
    font-size: 13px;
  }
</style>
