<script setup lang="ts">
  import { computed } from 'vue';
  import { CircleCheck, CircleMinus, Images, Sparkles } from '@lucide/vue';
  import DirectoryNumbering from './DirectoryNumbering.vue';
  import './library-actions.css';
  // Run, number and mark actions for the directory being shown.
  const { directory, loaded, running, busy, ready } = defineProps<{
    directory: number;
    /** Actions stay disabled until the directory loads. */
    loaded: boolean;
    /** The directory has an active batch. */
    running: boolean;
    busy: boolean;
    /** Whether a naming model is available. */
    ready: boolean;
  }>();
  const emit = defineEmits<{
    extract: [];
    rename: [];
    mark: [value: boolean];
    notice: [message: string];
    numbered: [];
  }>();
  const unavailable = computed(() => !loaded || running);
</script>

<template>
  <div class="heading-actions directory-mark-actions" role="group" aria-label="当前目录操作">
    <div class="directory-action-row"
      ><button
        class="library-action action-extract"
        aria-label="抽帧当前目录"
        :disabled="busy || unavailable"
        @click="emit('extract')"
        ><Images :size="16" />抽帧</button
      ><button
        class="library-action action-name"
        aria-label="重命名当前目录"
        :disabled="!ready || unavailable"
        @click="emit('rename')"
        ><Sparkles :size="16" />重命名</button
      ><DirectoryNumbering
        :directory="directory"
        :disabled="busy || unavailable"
        @notice="emit('notice', $event)"
        @changed="emit('numbered')"
      /><button
        class="library-action action-mark"
        title="标记当前目录及子目录中的现有媒体：视频跳过自动命名，图片和音频跳过编号"
        :disabled="!loaded"
        @click="emit('mark', true)"
        ><CircleCheck :size="16" />标记</button
      ><button
        class="library-action action-unmark"
        title="清除当前目录及子目录中媒体的标记，允许视频再次自动命名、图片和音频再次编号；文件名不变"
        :disabled="!loaded"
        @click="emit('mark', false)"
        ><CircleMinus :size="16" />取消标记</button
      ></div
    >
  </div>
</template>
