<script setup lang="ts">
  import ModalDialog from '../../components/ModalDialog.vue';
  import type { Entry } from '../../app/contracts';
  // Confirms setting or clearing the processed mark of the chosen entries.
  const { entries, value, saving, progress, error, isRetryUnavailable } = defineProps<{
    entries: Entry[];
    value: boolean;
    saving: boolean;
    progress: string;
    error: string;
    isRetryUnavailable: boolean;
  }>();
  const emit = defineEmits<{ close: []; submit: [] }>();
  const submitLabel = () => {
    if (saving) return '正在处理…';
    return value ? '确认标记' : '确认取消标记';
  };
  function close() {
    if (!saving) emit('close');
  }
</script>

<template>
  <ModalDialog :title="value ? '标记已处理' : '取消已处理标记'" @close="close"
    ><form @submit.prevent="emit('submit')"
      ><p
        >已选择 {{ entries.length }} 个目标<span v-if="entries.length === 1"
          >：{{ entries[0]?.name || '整个媒体库' }}</span
        >。</p
      ><p v-if="entries.some((e) => e.kind === 'directory')"
        >包含所选目录及所有子目录中的现有视频、图片和音频；以后新增的文件不受影响。</p
      ><p>{{
        value
          ? '标记后，视频会跳过自动命名和定时任务；图片和音频会跳过全局编号及一键编号，保留你自己的名称。已经处理的视频保留原状态。'
          : '取消后，视频可以再次自动命名，图片和音频可以再次参与编号，包括之前实际改名的文件。'
      }}</p
      ><p class="help"
        >只修改处理状态，不修改文件名、不删除命名历史。活动任务中的文件会报告失败，请等待任务结束后重试。标记保存在数据库中，数据库丢失后需重新标记。</p
      ><p v-if="progress" role="status">{{ progress }}</p
      ><div v-if="error" class="notice error batch-errors" role="alert">{{ error }}</div
      ><div class="modal-actions"
        ><button type="button" :disabled="saving" @click="emit('close')">关闭</button
        ><button class="primary" :disabled="saving || isRetryUnavailable">{{
          submitLabel()
        }}</button></div
      ></form
    ></ModalDialog
  >
</template>
