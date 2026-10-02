<script setup lang="ts">
  import { computed } from 'vue';
  import { RefreshCw, CircleStop } from 'lucide-vue-next';
  import { time, type LibraryScanStatus } from '../../app/api';
  const props = defineProps<{ status: LibraryScanStatus; submitting?: boolean }>();
  defineEmits<{ cancel: [] }>();
  const active = computed(() => ['scanning', 'cancelling'].includes(props.status.status));
  const label = computed(
    () =>
      ({
        idle: '等待扫描',
        scanning: '正在扫描',
        cancelling: '正在停止扫描',
        completed: '扫描完成',
        partial_failure: '扫描部分失败',
        failed: '扫描失败',
        cancelled: '扫描已停止'
      })[props.status.status]
  );
  const elapsed = computed(() => {
    if (!props.status.started) return '';
    const seconds = Math.max(
      0,
      Math.floor(
        (new Date(props.status.finished || Date.now()).getTime() -
          new Date(props.status.started).getTime()) /
          1000
      )
    );
    return seconds < 60 ? `${seconds} 秒` : `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`;
  });
</script>
<template>
  <section class="scan-status" aria-label="媒体索引扫描状态" aria-live="polite">
    <header>
      <strong
        ><RefreshCw v-if="active" :size="15" :class="{ spin: status.status === 'scanning' }" />{{
          label
        }}<span v-if="status.started" class="subtle"
          >{{ status.source === 'automatic' || status.source === 'schedule' ? '自动' : '手动' }} ·
          耗时 {{ elapsed }}</span
        ></strong
      >
      <button
        v-if="active"
        type="button"
        :disabled="submitting || status.status === 'cancelling'"
        @click="$emit('cancel')"
        ><CircleStop :size="15" />{{
          status.status === 'cancelling' ? '正在停止…' : '停止扫描'
        }}</button
      >
    </header>
    <p v-if="status.started" class="scan-counts"
      >已扫描 {{ status.directories }} 个目录 · {{ status.files }} 个文件<span
        >新增 {{ status.added }} · 更新 {{ status.changed }} · 移除 {{ status.removed }}</span
      ></p
    >
    <p v-if="active && status.current_path" class="scan-path"
      >当前目录：{{ status.current_path }}</p
    >
    <p class="help scan-timing"
      ><span v-if="status.finished">上次完成：{{ time(status.finished) }}</span
      ><span v-if="status.next_scan">下次扫描：{{ time(status.next_scan) }}</span
      ><span v-else-if="!active">可随时手动扫描目录及子目录。</span></p
    >
    <details v-if="status.errors?.length" class="scan-errors"
      ><summary>{{ status.errors.length }} 个目录读取失败，保留上次完整列表</summary
      ><ul
        ><li v-for="(failure, index) in status.errors" :key="index"
          ><strong>{{ failure.path || '媒体库' }}</strong
          >：{{ failure.message }}</li
        ></ul
      ></details
    >
  </section>
</template>
<style scoped>
  .scan-status {
    min-width: 0;
    padding: 14px 18px;
    border: 1px solid var(--line);
    border-radius: 9px;
    background: #f6f9f7;
  }
  .scan-status header,
  .scan-status header > strong {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 10px;
  }
  .scan-status header {
    justify-content: space-between;
  }
  .scan-status header strong {
    font-size: 13px;
  }
  .scan-status header .subtle {
    font-weight: 400;
    font-size: 11px;
  }
  .scan-status header button {
    font-size: 12px;
  }
  .scan-counts,
  .scan-path {
    margin: 10px 0 0;
    font-size: 12px;
    line-height: 1.7;
  }
  .scan-counts span {
    display: inline-block;
    margin-left: 18px;
    color: var(--ash);
  }
  .scan-path {
    overflow-wrap: anywhere;
  }
  .scan-timing {
    display: flex;
    flex-wrap: wrap;
    gap: 6px 18px;
    margin: 10px 0 0;
  }
  .scan-errors {
    margin-top: 12px;
    color: #b14545;
    font-size: 12px;
  }
  .scan-errors summary {
    cursor: pointer;
    line-height: 1.7;
  }
  .scan-errors ul {
    max-height: 200px;
    overflow: auto;
    padding-left: 20px;
    line-height: 1.7;
    overflow-wrap: anywhere;
  }
  @media (max-width: 600px) {
    .scan-counts span {
      display: block;
      margin-left: 0;
    }
    .scan-status {
      padding: 12px;
    }
  }
</style>
