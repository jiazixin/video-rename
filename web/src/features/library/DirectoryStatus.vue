<script setup lang="ts">
  import { statusNames, type Entry } from '../../app/api';
  import StatusBadge from '../../components/StatusBadge.vue';
  defineProps<{ entry: Entry }>();
  function directoryStatus(e: Entry) {
    if (e.task_status) return statusNames[e.task_status] || e.task_status;
    const p = e.progress;
    if (!p || p.unknown) return '待统计';
    if (!p.total) return '暂无视频';
    if (p.processed === p.total) return '全部已处理';
    return p.processed ? '部分已处理' : '待处理';
  }
  function directoryTone(e: Entry) {
    if (e.task_status) return e.task_status;
    const p = e.progress;
    return !p || p.unknown || !p.total
      ? 'unprocessed'
      : p.processed === p.total
        ? 'completed'
        : p.processed
          ? 'partial'
          : 'unprocessed';
  }
  const number = (value: number) => value.toLocaleString('zh-CN');
</script>
<template>
  <span
    class="directory-progress"
    title="包含子目录，按最近扫描的视频统计"
    :data-directory-status="directoryTone(entry)"
    ><StatusBadge :status="directoryTone(entry)">{{ directoryStatus(entry) }}</StatusBadge
    ><small
      v-if="entry.progress?.total"
      class="directory-progress-count"
      :aria-label="`已处理 ${entry.progress.processed} / ${entry.progress.total}${entry.progress.unknown ? '，统计尚未完成' : ''}`"
      >{{ number(entry.progress.processed) }} / {{ number(entry.progress.total)
      }}{{ entry.progress.unknown ? ' · 已发现' : '' }}</small
    ><small v-if="entry.progress?.active" class="directory-progress-active"
      >{{ entry.progress.active }} 个处理中</small
    ></span
  >
</template>
<style scoped>
  .directory-progress {
    display: inline-flex;
    flex-direction: row;
    align-items: center;
    justify-content: center;
    flex-wrap: wrap;
    min-width: 0;
    gap: 4px 8px;
    vertical-align: middle;
  }
  .directory-progress :deep(.status-badge) {
    padding: 2px 0;
    border: 0;
    background: transparent;
    border-radius: 0;
    font-size: 11px;
    gap: 5px;
    line-height: 1.6;
  }
  .directory-progress :deep(.status-badge[data-status='partial']) {
    color: #927039;
  }
  .directory-progress-count,
  .directory-progress-active {
    font-size: 10px;
    line-height: 1.6;
    color: var(--ash);
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }
  .directory-progress-active {
    color: #2468af;
  }
</style>
