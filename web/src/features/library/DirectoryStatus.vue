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
          ? 'scanning'
          : 'unprocessed';
  }
</script>
<template>
  <span class="directory-progress" title="包含子目录，按最近扫描的视频统计"
    ><StatusBadge :status="directoryTone(entry)">{{ directoryStatus(entry) }}</StatusBadge
    ><small v-if="entry.progress?.total" class="subtle"
      >已处理 {{ entry.progress.processed }} / {{ entry.progress.total
      }}{{ entry.progress.unknown ? ' · 已发现' : '' }}</small
    ><small v-if="entry.progress?.active" class="subtle"
      >{{ entry.progress.active }} 个处理中</small
    ></span
  >
</template>
