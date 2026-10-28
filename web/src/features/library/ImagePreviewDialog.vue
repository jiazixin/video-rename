<script setup lang="ts">
  import { RotateCw, Scan, ZoomIn, ZoomOut } from '@lucide/vue';
  import ModalDialog from '../../components/ModalDialog.vue';
  import ImageViewer from './ImageViewer.vue';
  import type { Entry } from '../../app/contracts';
  import './theater.css';
  // Theater view of one image with zoom and rotation tools and the folder strip.
  const { entry, strip, busy, notice, position } = defineProps<{
    entry: Entry;
    strip: Entry[];
    busy: boolean;
    notice: string;
    position: string;
  }>();
  const zoom = defineModel<number>('zoom', { required: true });
  const turn = defineModel<number>('turn', { required: true });
  const emit = defineEmits<{ close: []; previous: []; next: []; select: [entry: Entry] }>();
  function fit() {
    zoom.value = 1;
    turn.value = 0;
  }
</script>

<template>
  <ModalDialog :title="entry.name" theater @close="emit('close')"
    ><template #header
      ><span v-if="position" class="theater-count">第 {{ position }} 张</span
      ><span class="theater-tools" role="group" aria-label="图片查看"
        ><button
          aria-label="缩小"
          title="缩小"
          :disabled="zoom <= 0.5"
          @click="zoom = Math.max(0.5, zoom - 0.5)"
          ><ZoomOut :size="18" /></button
        ><span class="theater-zoom">{{ Math.round(zoom * 100) }}%</span
        ><button
          aria-label="放大"
          title="放大"
          :disabled="zoom >= 4"
          @click="zoom = Math.min(4, zoom + 0.5)"
          ><ZoomIn :size="18" /></button
        ><button aria-label="适应窗口" title="适应窗口" :disabled="zoom === 1 && !turn" @click="fit"
          ><Scan :size="18" /></button
        ><button aria-label="旋转" title="旋转" @click="turn = (turn + 90) % 360"
          ><RotateCw :size="18" /></button></span></template
    ><ImageViewer
      :entry="entry"
      :strip="strip"
      :busy="busy"
      :zoom="zoom"
      :turn="turn"
      :notice="notice"
      @previous="emit('previous')"
      @next="emit('next')"
      @select="emit('select', $event)"
      @zoom="zoom = $event"
  /></ModalDialog>
</template>

<style scoped>
  .theater-tools {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    margin-left: auto;
  }
  .theater-tools button {
    padding: var(--space-7);
    border: 0;
    border-radius: var(--radius-8);
    background: transparent;
    color: var(--stage-ink);
  }
  .theater-tools button:hover:not(:disabled) {
    background: var(--stage-hover);
    border: 0;
  }
  .theater-zoom {
    min-width: 44px;
    text-align: center;
    font-size: var(--font-size-12);
    color: var(--stage-dim);
    font-variant-numeric: tabular-nums;
  }
  @media (max-width: 600px) {
    .theater-zoom {
      display: none;
    }
  }
</style>
