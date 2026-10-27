<script setup lang="ts">
  import { niceCeil, stepActive } from './chart-scale';
  import { formatCount, formatCompact } from '../../lib/format/number';
  import { computed, onMounted, onUnmounted, ref } from 'vue';
  type ScatterPoint = {
    task: 'rename' | 'extract';
    run: number;
    items: number;
    ms: number;
    status: string;
    source: string;
    profile_title: string;
  };
  const { points, formatMs } = defineProps<{
    points: ScatterPoint[];
    formatMs: (v: number) => string;
    statusName: (s: string) => string;
  }>();
  const box = ref<HTMLElement>(),
    width = ref(860),
    height = 280,
    active = ref(-1);
  const pad = { top: 16, right: 18, bottom: 38, left: 62 };
  const inner = () => ({
    w: Math.max(80, width.value - pad.left - pad.right),
    h: height - pad.top - pad.bottom
  });
  const maxX = computed(() => niceCeil(Math.max(1, ...points.map((p) => p.items))));
  const maxY = computed(() => niceCeil(Math.max(1, ...points.map((p) => p.ms))));
  const yTicks = computed(() => {
    const out: number[] = [];
    for (let i = 0; i <= 4; i++) out.push((maxY.value * i) / 4);
    return out.reverse();
  });
  const xTicks = computed(() => {
    const out: number[] = [];
    for (let i = 0; i <= 4; i++) out.push((maxX.value * i) / 4);
    return out;
  });
  const placed = computed(() => {
    const { w, h } = inner();
    return points.map((p, i) => ({
      ...p,
      i,
      x: pad.left + (p.items / maxX.value) * w,
      y: pad.top + h - (p.ms / maxY.value) * h
    }));
  });
  const hovered = computed(() => (active.value >= 0 ? placed.value[active.value] : undefined));
  function pick(event: PointerEvent) {
    const rect = (event.currentTarget as SVGRectElement).getBoundingClientRect();
    const x = pad.left + ((event.clientX - rect.left) * inner().w) / rect.width,
      y = pad.top + ((event.clientY - rect.top) * inner().h) / rect.height;
    let best = -1,
      bestDist = Infinity;
    for (const p of placed.value) {
      const d = (p.x - x) ** 2 + (p.y - y) ** 2;
      if (d < bestDist) {
        bestDist = d;
        best = p.i;
      }
    }
    active.value = best;
  }
  function keys(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      active.value = -1;
      return;
    }
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
    event.preventDefault();
    if (!points.length) return;
    active.value = stepActive(active.value, points.length, event.key === 'ArrowRight' ? 1 : -1);
  }
  const tooltipStyle = computed(() => {
    const p = placed.value[active.value];
    if (!p) return {};
    return {
      left: `${Math.max(0, Math.min(width.value - 210, p.x - 105))}px`,
      top: `${Math.max(0, p.y - 84)}px`
    };
  });
  function resize() {
    const el = box.value;
    if (el) width.value = Math.max(1, el.clientWidth);
  }
  let observer: ResizeObserver | undefined;
  onMounted(() => {
    resize();
    observer = new ResizeObserver(resize);
    if (box.value) observer.observe(box.value);
  });
  onUnmounted(() => observer?.disconnect());
</script>
<template>
  <div ref="box" class="usage-trend analysis-scatter">
    <svg
      :width="width"
      :height="height"
      role="img"
      aria-label="批次大小与批次耗时散点图，每行数值见下方数据表"
      tabindex="0"
      @keydown="keys"
      @pointerleave="active = -1"
    >
      <line
        v-for="t in yTicks"
        :key="`y${t}`"
        :x1="pad.left"
        :x2="width - pad.right"
        :y1="pad.top + inner().h - (t / maxY) * inner().h"
        :y2="pad.top + inner().h - (t / maxY) * inner().h"
        class="gridline"
      />
      <text
        v-for="t in yTicks"
        :key="`yt${t}`"
        :x="pad.left - 10"
        :y="pad.top + inner().h - (t / maxY) * inner().h + 4"
        class="axis-text"
        text-anchor="end"
      >
        {{ formatCompact(t, 0) }}
      </text>
      <text
        v-for="t in xTicks"
        :key="`xt${t}`"
        :x="pad.left + (t / maxX) * inner().w"
        :y="height - 16"
        class="axis-text"
        text-anchor="middle"
      >
        {{ formatCompact(t, 0) }}
      </text>
      <!-- prettier-ignore -->
      <text
        :x="pad.left + inner().w / 2"
        :y="height - 3"
        class="axis-text axis-caption"
        text-anchor="middle"
      >批次目标数（个）</text>
      <circle v-for="p in placed" :key="p.run" :cx="p.x" :cy="p.y" r="4" class="scatter-dot" />
      <circle v-if="hovered" :cx="hovered.x" :cy="hovered.y" r="7" class="scatter-halo" />
      <rect
        :x="pad.left"
        :y="pad.top"
        :width="inner().w"
        :height="inner().h"
        class="hit-zone"
        @pointermove="pick"
      />
    </svg>
    <div v-if="hovered" class="trend-tooltip" :style="tooltipStyle" role="status">
      <strong>批次 #{{ hovered.run }} · {{ formatMs(hovered.ms) }}</strong>
      <span
        >{{ formatCount(hovered.items) }} 个目标 ·
        {{ hovered.source === 'manual' ? '手动' : '自动计划' }} ·
        {{ hovered.task === 'extract' ? '抽帧' : hovered.profile_title || '未命名配置' }}</span
      >
      <span>状态：{{ statusName(hovered.status) }}</span>
    </div>
    <p v-if="!points.length" class="trend-empty">区间内没有已完成的批次</p>
    <p class="trend-hint">悬停或方向键查看每个批次，y 轴为批次耗时</p>
  </div>
</template>
