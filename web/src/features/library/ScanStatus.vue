<script setup lang="ts">
  import { computed, onUnmounted, ref, watch } from 'vue';
  import { RefreshCw, CircleStop, Folder } from 'lucide-vue-next';
  import { time, type LibraryScanStatus } from '../../app/api';
  const props = defineProps<{
    status: LibraryScanStatus;
    submitting?: boolean;
    compact?: boolean;
  }>();
  defineEmits<{ cancel: [] }>();
  const active = computed(() => ['scanning', 'cancelling'].includes(props.status.status));
  const warning = computed(() => ['partial_failure', 'failed'].includes(props.status.status));
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
  const now = ref(Date.now());
  let clock: ReturnType<typeof setInterval> | undefined;
  watch(
    active,
    (running) => {
      clearInterval(clock);
      now.value = Date.now();
      if (running) clock = setInterval(() => (now.value = Date.now()), 1000);
    },
    { immediate: true }
  );
  onUnmounted(() => clearInterval(clock));
  const elapsed = computed(() => {
    if (!props.status.started) return '';
    const seconds = Math.max(
      0,
      Math.floor(
        ((props.status.finished ? new Date(props.status.finished).getTime() : now.value) -
          new Date(props.status.started).getTime()) /
          1000
      )
    );
    return seconds < 60 ? `${seconds} 秒` : `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`;
  });
  const number = (value: number) => value.toLocaleString('zh-CN');
</script>
<template>
  <section
    class="scan-status"
    :class="{ 'is-active': active, 'is-warning': warning, 'is-compact': compact && !active }"
    :data-scan-state="status.status"
    aria-label="媒体索引扫描状态"
  >
    <header class="scan-status-heading">
      <div class="scan-status-identity">
        <strong class="scan-status-label" aria-live="polite"
          ><RefreshCw v-if="active" :size="15" :class="{ spin: status.status === 'scanning' }" />{{
            label
          }}</strong
        >
        <span v-if="status.started" class="scan-source"
          >{{ status.source === 'automatic' || status.source === 'schedule' ? '自动' : '手动' }}
          <span class="scan-elapsed">耗时 {{ elapsed }}</span></span
        >
      </div>
      <button
        v-if="active"
        type="button"
        class="scan-stop"
        :disabled="submitting || status.status === 'cancelling'"
        @click="$emit('cancel')"
        ><CircleStop :size="15" />{{
          status.status === 'cancelling' ? '正在停止…' : '停止扫描'
        }}</button
      >
    </header>
    <dl v-if="status.started" class="scan-counts">
      <div class="scan-metric"
        ><dt>已扫描目录</dt><dd>{{ number(status.directories) }}</dd></div
      >
      <div class="scan-metric"
        ><dt>已扫描文件</dt><dd>{{ number(status.files) }}</dd></div
      >
      <div class="scan-changes">
        <div class="scan-metric"
          ><dt>新增</dt><dd>{{ number(status.added) }}</dd></div
        >
        <div class="scan-metric"
          ><dt>更新</dt><dd>{{ number(status.changed) }}</dd></div
        >
        <div class="scan-metric"
          ><dt>移除</dt><dd>{{ number(status.removed) }}</dd></div
        >
      </div>
    </dl>
    <div v-if="active" class="scan-path">
      <Folder :size="14" /><span class="scan-path-label">当前目录</span>
      <span class="scan-path-value" :title="status.current_path || undefined">{{
        status.current_path || (status.status === 'cancelling' ? '等待当前读取结束…' : '准备扫描…')
      }}</span>
    </div>
    <div class="scan-timing">
      <span v-if="status.finished">上次完成 {{ time(status.finished) }}</span>
      <span v-if="status.next_scan">下次扫描 {{ time(status.next_scan) }}</span>
      <span v-else-if="!active && !status.finished">可随时手动扫描目录及子目录。</span>
      <span v-if="active" class="scan-phase">{{
        status.status === 'cancelling' ? '停止后保留已同步的目录' : '正在读取并同步目录索引'
      }}</span>
    </div>
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
    padding: 16px 18px;
    border: 1px solid #dfe8e3;
    border-radius: 9px;
    background: #f7faf8;
  }
  .scan-status.is-active {
    border-color: #bcd7cb;
  }
  .scan-status.is-warning {
    border-color: #ebd4c6;
    background: #fffbf8;
  }
  .scan-status-heading,
  .scan-status-identity,
  .scan-status-label,
  .scan-source {
    display: flex;
    align-items: center;
  }
  .scan-status-heading {
    justify-content: space-between;
    gap: 12px;
    min-height: 32px;
  }
  .scan-status-identity {
    flex-wrap: wrap;
    min-width: 0;
    gap: 6px 12px;
  }
  .scan-status-label {
    gap: 7px;
    font-size: 13px;
    white-space: nowrap;
  }
  .is-active .scan-status-label {
    color: #287452;
  }
  .is-warning .scan-status-label {
    color: #a65c33;
  }
  .scan-source {
    gap: 10px;
    font-size: 11px;
    color: var(--ash);
    white-space: nowrap;
  }
  .scan-elapsed {
    font-variant-numeric: tabular-nums;
  }
  .scan-stop {
    flex: none;
    padding: 5px 10px;
    font-size: 12px;
    color: var(--steel);
  }
  .scan-counts {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) minmax(0, 2fr);
    gap: 18px;
    margin: 12px 0 0;
    padding-top: 12px;
    border-top: 1px solid #e3ebe6;
  }
  .scan-metric {
    min-width: 0;
  }
  .scan-metric dt {
    color: var(--ash);
    font-size: 11px;
  }
  .scan-metric dd {
    margin: 3px 0 0;
    font-variant-numeric: tabular-nums;
    font-size: 19px;
    font-weight: 600;
    line-height: 1.4;
  }
  .scan-changes {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 14px;
    padding-left: 18px;
    border-left: 1px solid #e3ebe6;
  }
  .scan-changes dd {
    color: var(--steel);
    font-size: 16px;
    line-height: 1.65;
  }
  .scan-path {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
    margin-top: 12px;
    padding: 7px 10px;
    border-radius: 5px;
    background: #ecf3ef;
    font-size: 11px;
    color: #597266;
  }
  .scan-path-label {
    flex: none;
  }
  .scan-path-value {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--steel);
  }
  .scan-timing {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 18px;
    margin-top: 10px;
    color: var(--ash);
    font-size: 11px;
    font-variant-numeric: tabular-nums;
    line-height: 1.7;
  }
  .scan-phase {
    margin-left: auto;
  }
  .scan-errors {
    margin-top: 12px;
    padding-top: 10px;
    border-top: 1px solid #ebddd2;
    color: #a65c33;
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
  .scan-status.is-compact {
    padding-block: 10px;
  }
  .is-compact .scan-counts {
    display: flex;
    flex-wrap: wrap;
    gap: 8px 22px;
    margin-top: 2px;
    padding-top: 0;
    border-top: 0;
  }
  .is-compact .scan-metric {
    display: flex;
    align-items: baseline;
    gap: 6px;
  }
  .is-compact .scan-metric dd {
    margin: 0;
    font-size: 12px;
    line-height: 1.5;
  }
  .is-compact .scan-changes {
    display: flex;
    flex-wrap: wrap;
    gap: 14px;
  }
  .is-compact .scan-timing {
    margin-top: 5px;
  }
  @media (max-width: 600px) {
    .scan-status {
      padding: 12px;
    }
    .scan-status-heading {
      align-items: flex-start;
    }
    .scan-status-identity {
      flex-direction: column;
      align-items: flex-start;
      gap: 5px;
    }
    .scan-counts {
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 12px;
    }
    .scan-changes {
      grid-column: 1 / -1;
      padding: 10px 0 0;
      border-left: 0;
      border-top: 1px solid #e3ebe6;
    }
    .is-compact .scan-changes {
      width: 100%;
      padding: 0;
      border: 0;
    }
    .is-compact .scan-counts {
      margin-top: 8px;
    }
    .scan-phase {
      margin-left: 0;
    }
  }
</style>
