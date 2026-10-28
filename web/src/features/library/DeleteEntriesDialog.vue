<script setup lang="ts">
  import ModalDialog from '../../components/ModalDialog.vue';
  import type { Entry } from '../../app/contracts';
  // Confirms permanently deleting one entry or a selection; the page performs the deletion.
  const {
    entries,
    isBatch,
    path,
    progress = '',
    error = '',
    saving
  } = defineProps<{
    entries: Entry[];
    isBatch?: boolean;
    path: string;
    progress?: string;
    error?: string;
    saving: boolean;
  }>();
  const emit = defineEmits<{ close: []; confirm: [] }>();
  const single = () => entries[0];
  const hasDirectory = () => entries.some((e) => e.kind === 'directory');
  const title = () => {
    if (isBatch) return '批量删除';
    return single()?.kind === 'directory' ? '删除目录' : '删除文件';
  };
  function close() {
    if (!saving) emit('close');
  }
</script>

<template>
  <ModalDialog :title="title()" @close="close"
    ><form @submit.prevent="emit('confirm')">
      <template v-if="isBatch"
        ><p>确定永久删除选中的 {{ entries.length }} 个条目？包含其他分页的已选项。</p
        ><p class="delete-path">{{ path }}</p>
        <ul class="delete-targets"
          ><li v-for="entry in entries" :key="entry.id"
            >{{ entry.name }}<span v-if="entry.kind === 'directory'">（目录）</span></li
          ></ul
        >
        <p v-if="hasDirectory()">所选目录内的所有文件和子目录都会一并删除。</p
        ><p class="help"
          >将从磁盘真实删除，不进入回收站，无法通过还原功能恢复。逐项执行，失败不会撤销已完成的删除。</p
        >
        <p v-if="progress" role="status">{{ progress }}</p
        ><div v-if="error" class="notice error batch-errors" role="alert">{{
          error
        }}</div></template
      ><template v-else
        ><p>确定永久删除以下{{ single()?.kind === 'directory' ? '目录' : '文件' }}？</p
        ><p class="delete-path">媒体库 / {{ path }}</p
        ><p v-if="hasDirectory()">目录内的所有文件和子目录都会一并删除。</p
        ><p class="help">将从磁盘真实删除，不进入回收站，无法通过还原功能恢复。</p
        ><div v-if="error" class="notice error" role="alert">{{ error }}</div></template
      >
      <div class="modal-actions"
        ><button type="button" :disabled="saving" @click="emit('close')">取消</button
        ><button class="delete-confirm" :disabled="saving">{{
          saving ? '正在删除…' : '确认永久删除'
        }}</button></div
      >
    </form></ModalDialog
  >
</template>

<style scoped>
  .delete-targets {
    max-height: 220px;
    overflow: auto;
    padding-left: var(--space-24);
    overflow-wrap: anywhere;
  }
  .delete-targets li {
    margin: var(--space-6) 0;
  }
  .delete-path {
    overflow-wrap: anywhere;
    font-weight: var(--font-weight-600);
  }
</style>
