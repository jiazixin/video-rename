<script setup lang="ts">
  import { computed } from 'vue';
  import ModalDialog from '../../components/ModalDialog.vue';
  import { ModelPicker } from '../models';
  // Confirms a batch over the selection: extract frames, auto-rename or restore names.
  const { action, count, saving, progress, error } = defineProps<{
    action: 'extract' | 'auto' | 'restore';
    count: number;
    saving: boolean;
    progress: string;
    error: string;
  }>();
  const modelConfigID = defineModel<number>('modelConfigId', { required: true });
  const recursive = defineModel<boolean>('recursive', { required: true });
  const emit = defineEmits<{ close: []; submit: [] }>();
  const titles = { extract: '批量抽帧', auto: '批量自动命名', restore: '批量还原' } as const;
  const submitLabels = { extract: '抽帧', auto: '重命名', restore: '确认批量还原' } as const;
  const isBlocked = computed(() => saving || !count || (action === 'auto' && !modelConfigID.value));
  const submitLabel = computed(() => (saving ? '正在处理…' : submitLabels[action]));
  function close() {
    if (!saving) emit('close');
  }
</script>

<template>
  <ModalDialog :title="titles[action]" @close="close"
    ><form @submit.prevent="emit('submit')">
      <template v-if="action === 'auto' || action === 'extract'"
        ><p>处理选中的 {{ count }} 个条目，创建一个批次。</p
        ><ModelPicker v-if="action === 'auto'" v-model="modelConfigID" /><label class="check-label"
          ><input v-model="recursive" type="checkbox" role="switch" />包含子目录</label
        ><p class="help">{{
          action === 'extract'
            ? '预先生成截图供后续重命名使用，已有有效截图直接复用。结果见“抽帧记录”。'
            : '已命名和正在处理的视频会跳过。'
        }}</p></template
      >
      <template v-else
        ><p>还原选中的 {{ count }} 个条目最近一次改名。目录还原只恢复目录名称。</p
        ><p class="help">还原后可以再次自动命名。冲突条目保留原状。</p></template
      ><p v-if="progress" role="status">{{ progress }}</p
      ><div v-if="error" class="notice error batch-errors" role="alert">{{ error }}</div
      ><div class="modal-actions"
        ><button type="button" :disabled="saving" @click="emit('close')">关闭</button
        ><button class="primary" :disabled="isBlocked">{{ submitLabel }}</button></div
      ></form
    ></ModalDialog
  >
</template>
