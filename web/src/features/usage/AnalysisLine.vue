<script setup lang="ts">
  import { niceCeil, stepActive } from './chart-scale';
  import { formatClock, formatHourMinute, formatMonthDay, formatTime } from '../../lib/format/date';
  import { formatCompact } from '../../lib/format/number';
  import { computed, onMounted, onUnmounted, ref } from 'vue';
  type LineSeries = {
    name: string;
    tone: string; // data-series value; analysis.css maps it to a theme color
    values: number[];
  };
  const { series, buckets, interval, format } = defineProps<{
    series: LineSeries[];
    buckets: { from: string; to: string }[];
    interval: string;
    format: (v: number) => string;
    label: string;
    emptyText: string;
  }>();
  const box = ref<HTMLElement>(),
    width = ref(860),
    height = 250,
    active = ref(-1);
  const pad = { top: 18, right: 18, bottom: 34, left: 62 };
  const inner = () => ({
    w: Math.max(80, width.value - pad.left - pad.right),
    h: height - pad.top - pad.bottom
  });
  const maxY = computed(() => {
    let top = 0;
    for (const s of series) for (const v of s.values) if (v > top) top = v;
    return niceCeil(top);
  });
  const ticks = computed(() => {
    const out: number[] = [];
    for (let i = 0; i <= 4; i++) out.push((maxY.value * i) / 4);
    return out.reverse();
  });
  const points = computed(() => {
    const { w, h } = inner(),
      n = buckets.length;
    return buckets.map((b, i) => ({
      x: pad.left + (n === 1 ? w / 2 : (w * i) / (n - 1)),
      ys: series.map((s) => pad.top + h - ((s.values[i] || 0) / maxY.value) * h),
      b
    }));
  });
  const hovered = computed(() => (active.value >= 0 ? points.value[active.value] : undefined));
  const linePaths = computed(() =>
    series.map((_, si) => points.value.map((p) => `${p.x},${p.ys[si]}`).join(' '))
  );
  const labelSteps = computed(() => {
    const n = buckets.length;
    return Math.max(1, Math.ceil(n / 6));
  });
  function axisLabel(value: string) {
    const d = new Date(value);
    switch (interval) {
      case '15m':
      case 'hour':
        return formatHourMinute(d);
      case 'day':
        return formatMonthDay(d);
      case 'month':
        return `${d.getFullYear()}-${d.getMonth() + 1}`;
      default:
        return String(d.getFullYear());
    }
  }
  function bucketTitle(b: { from: string; to: string }) {
    return `${formatTime(b.from)} – ${formatClock(b.to)}`;
  }
  function pick(event: PointerEvent) {
    const n = buckets.length;
    if (!n) return;
    const svg = (event.currentTarget as SVGRectElement).ownerSVGElement;
    if (!svg) return;
    const rect = svg.getBoundingClientRect();
    const x = ((event.clientX - rect.left) * width.value) / rect.width;
    const ratio = (x - pad.left) / inner().w;
    active.value = Math.max(0, Math.min(n - 1, Math.round(ratio * (n - 1))));
  }
  function keys(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      active.value = -1;
      return;
    }
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
    event.preventDefault();
    const n = buckets.length;
    if (!n) return;
    active.value = stepActive(active.value, n, event.key === 'ArrowRight' ? 1 : -1);
  }
  const tooltipStyle = computed(() => {
    const p = points.value[active.value];
    if (!p) return {};
    return { left: `${Math.max(0, Math.min(width.value - 200, p.x - 100))}px` };
  });
  const isEmpty = computed(() => series.every((s) => s.values.every((v) => !v)));
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
  <div ref="box" class="usage-trend analysis-line">
    <div v-if="series.length > 1" class="analysis-legend" aria-hidden="false">
      <span v-for="s in series" :key="s.name" class="analysis-legend-item"
        ><i :data-series="s.tone" />{{ s.name }}</span
      >
    </div>
    <svg
      :width="width"
      :height="height"
      role="img"
      :aria-label="`${label}，精确数值见下方数据表`"
      tabindex="0"
      @keydown="keys"
      @pointerleave="active = -1"
    >
      <line
        v-for="t in ticks"
        :key="t"
        :x1="pad.left"
        :x2="width - pad.right"
        :y1="pad.top + inner().h - (t / maxY) * inner().h"
        :y2="pad.top + inner().h - (t / maxY) * inner().h"
        class="gridline"
      />
      <text
        v-for="t in ticks"
        :key="t"
        :x="pad.left - 10"
        :y="pad.top + inner().h - (t / maxY) * inner().h + 4"
        class="axis-text"
        text-anchor="end"
      >
        {{ formatCompact(t) }}
      </text>
      <polyline
        v-for="(path, si) in linePaths"
        v-show="points.length"
        :key="si"
        :points="path"
        class="series-line"
        :data-series="series[si]?.tone"
      />
      <template v-if="hovered">
        <line
          :x1="hovered.x"
          :x2="hovered.x"
          :y1="pad.top"
          :y2="pad.top + inner().h"
          class="crosshair"
        />
        <circle
          v-for="(s, si) in series"
          :key="si"
          :cx="hovered.x"
          :cy="hovered.ys[si]"
          r="4.5"
          class="trend-dot"
          :data-series="s.tone"
        />
      </template>
      <text
        v-for="p in points.filter((_, i) => i % labelSteps === 0 || i === points.length - 1)"
        :key="p.b.from"
        :x="p.x"
        :y="height - 10"
        class="axis-text"
        text-anchor="middle"
      >
        {{ axisLabel(p.b.from) }}
      </text>
      <rect
        v-for="(p, i) in points"
        :key="`h${i}`"
        :x="points.length === 1 ? p.x - inner().w / 2 : p.x - inner().w / (points.length - 1) / 2"
        :y="pad.top"
        :width="points.length === 1 ? inner().w : inner().w / (points.length - 1)"
        :height="inner().h"
        class="hit-zone"
        @pointermove="pick"
      />
    </svg>
    <div v-if="hovered" class="trend-tooltip" :style="tooltipStyle" role="status">
      <strong>{{ bucketTitle(hovered.b) }}</strong>
      <span v-for="s in series" :key="s.name" class="tooltip-row"
        ><i class="tooltip-key" :data-series="s.tone" /><b>{{ format(s.values[active] || 0) }}</b
        >{{ s.name }}</span
      >
    </div>
    <p v-if="isEmpty" class="trend-empty">{{ emptyText }}</p>
    <p class="trend-hint">方向键查看每个时间点，精确数值见数据表</p>
  </div>
</template>
