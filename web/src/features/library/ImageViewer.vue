<script setup lang="ts">
  import { computed, ref, watch } from 'vue';
  import { ChevronLeft, ChevronRight } from '@lucide/vue';
  import type { Entry } from '../../app/contracts';
  const {
    entry,
    strip,
    busy,
    zoom,
    turn,
    notice = ''
  } = defineProps<{
    entry: Entry;
    strip: Entry[];
    busy?: boolean;
    zoom: number;
    turn: number;
    notice?: string;
  }>();
  const emit = defineEmits<{
    previous: [];
    next: [];
    select: [entry: Entry];
    zoom: [value: number];
  }>();
  const failed = ref(false),
    pan = ref({ x: 0, y: 0 }),
    dragging = ref(false);
  let start = { x: 0, y: 0, panX: 0, panY: 0 };
  watch(
    () => entry.id,
    () => {
      failed.value = false;
      pan.value = { x: 0, y: 0 };
    }
  );
  // Back to the fitted view drops any panning, so a re-zoom starts from the centre.
  watch(
    () => zoom,
    (zoom) => {
      if (zoom <= 1) pan.value = { x: 0, y: 0 };
    }
  );
  const transform = computed(
    () => `translate(${pan.value.x}px, ${pan.value.y}px) scale(${zoom}) rotate(${turn}deg)`
  );
  function grab(e: PointerEvent) {
    if (zoom <= 1 || e.button !== 0) return;
    dragging.value = true;
    start = { x: e.clientX, y: e.clientY, panX: pan.value.x, panY: pan.value.y };
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
  }
  function drag(e: PointerEvent) {
    if (!dragging.value) return;
    pan.value = { x: start.panX + e.clientX - start.x, y: start.panY + e.clientY - start.y };
  }
  function release() {
    dragging.value = false;
  }
  function toggleZoom() {
    emit('zoom', zoom > 1 ? 1 : 2);
  }
</script>
<template>
  <div class="image-theater">
    <div
      class="image-stage"
      :class="{ zoomed: zoom > 1, dragging }"
      @pointerdown="grab"
      @pointermove="drag"
      @pointerup="release"
      @pointercancel="release"
      @dblclick="toggleZoom"
    >
      <img
        v-if="!failed"
        :key="entry.id"
        class="image-preview"
        :src="`/api/entries/${entry.id}/image`"
        :alt="entry.name"
        :style="{ transform }"
        draggable="false"
        @error="failed = true"
      /><p v-else class="stage-error" role="alert"
        >无法显示图片，文件可能不可用或浏览器不支持其格式。</p
      >
      <button
        class="image-nav previous"
        aria-label="上一张图片"
        :disabled="busy"
        @pointerdown.stop
        @click="emit('previous')"
        ><ChevronLeft :size="24" /></button
      ><button
        class="image-nav next"
        aria-label="下一张图片"
        :disabled="busy"
        @pointerdown.stop
        @click="emit('next')"
        ><ChevronRight :size="24"
      /></button>
    </div>
    <p v-if="notice" class="stage-notice" role="alert">{{ notice }}</p>
    <div v-if="strip.length > 1" class="image-strip" aria-label="同目录图片">
      <button
        v-for="item in strip"
        :key="item.id"
        :aria-label="`切换到 ${item.name}`"
        :aria-current="item.id === entry.id ? 'true' : undefined"
        :title="item.name"
        @click="item.id !== entry.id && emit('select', item)"
        ><img :src="`/api/entries/${item.id}/image`" alt="" loading="lazy" decoding="async"
      /></button>
    </div>
    <p class="image-hint">← / ↑ 上一张 · → / ↓ 下一张 · 双击放大 · 放大后可拖动</p>
  </div>
</template>
<style scoped>
  .image-theater {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-10);
  }
  .image-stage {
    position: relative;
    flex: 1;
    min-height: 0;
    display: grid;
    place-items: center;
    overflow: hidden;
    border-radius: var(--radius-10);
    background: radial-gradient(circle at 50% 42%, var(--palette-1a2421), var(--stage));
    touch-action: none;
  }
  .image-stage.zoomed {
    cursor: grab;
  }
  .image-stage.dragging {
    cursor: grabbing;
  }
  .image-preview {
    max-width: calc(100% - 120px);
    max-height: calc(100% - 24px);
    object-fit: contain;
    user-select: none;
    box-shadow: var(--shadow-11);
    transition: transform var(--motion-enter) linear;
  }
  .dragging .image-preview {
    transition: none;
  }
  .stage-error {
    color: var(--palette-ffc9c9);
    font-size: var(--font-size-13);
    padding: 0 var(--space-72);
    text-align: center;
  }
  .image-nav {
    position: absolute;
    top: 50%;
    width: 44px;
    height: 44px;
    padding: 0;
    border: 0;
    border-radius: var(--radius-round);
    transform: translateY(-50%);
    background: var(--palette-rgba-255-255-255-0_1);
    color: var(--stage-ink);
  }
  .image-nav:hover:not(:disabled) {
    background: var(--palette-rgba-255-255-255-0_22);
    border: 0;
  }
  .image-nav.previous {
    left: 14px;
  }
  .image-nav.next {
    right: 14px;
  }
  .stage-notice {
    margin: 0;
    font-size: var(--font-size-12);
    color: var(--palette-ffc9c9);
    text-align: center;
  }
  .image-strip {
    flex-shrink: 0;
    display: flex;
    justify-content: center;
    gap: var(--space-8);
    overflow-x: auto;
    padding: var(--space-2);
  }
  .image-strip button {
    flex: 0 0 auto;
    width: 64px;
    height: 46px;
    padding: 0;
    border: 2px solid transparent;
    border-radius: var(--radius-6);
    overflow: hidden;
    background: var(--palette-000000);
    opacity: 0.5;
    transition:
      opacity var(--motion-hover) linear,
      border-color var(--motion-hover) linear;
  }
  .image-strip button:hover:not(:disabled) {
    opacity: 0.85;
    background: var(--palette-000000);
    border-color: transparent;
  }
  .image-strip button[aria-current='true'],
  .image-strip button[aria-current='true']:hover:not(:disabled) {
    opacity: 1;
    border-color: var(--stage-accent);
  }
  .image-strip img {
    display: block;
    width: 100%;
    height: 100%;
    object-fit: cover;
  }
  .image-hint {
    margin: 0;
    font-size: var(--font-size-11);
    color: var(--stage-dim);
    text-align: center;
  }
  @media (max-width: 600px) {
    .image-preview {
      max-width: calc(100% - 16px);
    }
    .image-nav {
      width: 38px;
      height: 38px;
    }
    .image-nav.previous {
      left: 6px;
    }
    .image-nav.next {
      right: 6px;
    }
    .image-strip button {
      width: 52px;
      height: 38px;
    }
    .image-hint {
      display: none;
    }
  }
</style>
