<script setup lang="ts">
  import { formatCount } from '../../lib/format/number';
  // Ordered duration bands use one ramp (light to dark = short to long); values sit at the bar ends.
  const { bands } = defineProps<{ bands: { label: string; count: number; tone: string }[] }>();
  const max = () => Math.max(1, ...bands.map((b) => b.count));
  const total = () => bands.reduce((sum, b) => sum + b.count, 0);
</script>
<template>
  <div class="analysis-histogram">
    <div v-for="band in bands" :key="band.label" class="analysis-hist-row">
      <span class="analysis-hist-label">{{ band.label }}</span>
      <div class="analysis-hist-track"
        ><div
          v-if="band.count"
          class="analysis-hist-bar"
          :data-series="band.tone"
          :style="{ width: `${Math.max(1.5, (band.count / max()) * 100)}%` }"
      /></div>
      <span class="analysis-hist-value" :title="`${band.label}：${formatCount(band.count)} 项`">{{
        formatCount(band.count)
      }}</span>
    </div>
    <p v-if="!total()" class="trend-empty analysis-hist-empty">还没有可统计的逐项耗时</p>
    <p class="trend-hint">共 {{ formatCount(total()) }} 个样本，数值标注在条尾</p>
  </div>
</template>
