<script setup lang="ts">
  import { computed } from 'vue';

  const props = defineProps<{ value?: string | null }>();
  const parts = computed(() => {
    if (!props.value) return undefined;
    const date = new Date(props.value);
    if (Number.isNaN(date.getTime())) return undefined;
    return {
      date: date.toLocaleDateString('zh-CN'),
      clock: date.toLocaleTimeString('zh-CN', { hour12: false }),
      full: date.toLocaleString('zh-CN', { hour12: false }),
      iso: date.toISOString()
    };
  });
</script>

<template>
  <time
    v-if="parts"
    class="stacked-date-time"
    :datetime="parts.iso"
    :title="parts.full"
    :aria-label="parts.full"
    ><span>{{ parts.date }}</span
    ><span>{{ parts.clock }}</span></time
  >
  <span v-else class="date-time-empty" aria-label="暂无时间">—</span>
</template>

<style scoped>
  .stacked-date-time {
    display: inline-grid;
    gap: var(--date-time-gap, 4px);
    width: max-content;
    vertical-align: middle;
    text-align: inherit;
    font-size: 12px;
    font-weight: 400;
    font-variant-numeric: tabular-nums;
    line-height: 1.4;
    white-space: nowrap;
    color: var(--steel);
  }
  .stacked-date-time > span:first-child {
    color: var(--ink);
    font-weight: 500;
  }
  .date-time-empty {
    color: var(--steel);
  }
</style>
