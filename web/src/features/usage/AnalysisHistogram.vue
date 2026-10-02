<script setup lang="ts">
  // 耗时分布：有序档位使用单色顺序色带（浅→深 = 短→长），值直接标在条尾。
  const props = defineProps<{ bands: { label: string; count: number; color: string }[] }>();
  const max = () => Math.max(1, ...props.bands.map((b) => b.count));
  const total = () => props.bands.reduce((sum, b) => sum + b.count, 0);
</script>
<template>
  <div class="analysis-histogram">
    <div v-for="band in bands" :key="band.label" class="analysis-hist-row">
      <span class="analysis-hist-label">{{ band.label }}</span>
      <div class="analysis-hist-track"
        ><div
          v-if="band.count"
          class="analysis-hist-bar"
          :style="{
            width: `${Math.max(1.5, (band.count / max()) * 100)}%`,
            background: band.color
          }"
      /></div>
      <span
        class="analysis-hist-value"
        :title="`${band.label}：${band.count.toLocaleString()} 项`"
        >{{ band.count.toLocaleString() }}</span
      >
    </div>
    <p v-if="!total()" class="trend-empty analysis-hist-empty">还没有可统计的逐项耗时</p>
    <p class="trend-hint">共 {{ total().toLocaleString() }} 个样本，数值标注在条尾</p>
  </div>
</template>
