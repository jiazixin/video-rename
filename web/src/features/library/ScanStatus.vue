<script setup lang="ts">
  import { formatCount } from '../../lib/format/number';
  import { computed, onUnmounted, ref, watch } from 'vue';
  import { RefreshCw, CircleStop, Folder } from '@lucide/vue';
  import { formatTime } from '../../lib/format/date';
  import { type LibraryScanStatus } from '../../app/contracts';
  import { errorText } from '../../app/errors';
  import { displayPath } from '../../lib/format/path';
  const { status, submitting, compact } = defineProps<{
    status: LibraryScanStatus;
    submitting?: boolean;
    compact?: boolean;
  }>();
  defineEmits<{ cancel: [] }>();
  const active = computed(() => ['scanning', 'cancelling'].includes(status.status));
  const warning = computed(() => ['partial_failure', 'failed'].includes(status.status));
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
      })[status.status]
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
  onUnmounted(() => {
    clearInterval(clock);
  });
  const elapsed = computed(() => {
    if (!status.started) return '';
    const seconds = Math.max(
      0,
      Math.floor(
        ((status.finished ? new Date(status.finished).getTime() : now.value) -
          new Date(status.started).getTime()) /
          1000
      )
    );
    return seconds < 60 ? `${seconds} 秒` : `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`;
  });
  const number = (value: number) => formatCount(value);
  function failureScope(failure: { scope?: string; path?: string }) {
    if (failure.scope === 'settings') return '扫描设置';
    if (failure.scope === 'targets') return '扫描范围';
    return displayPath(failure.path || '/');
  }
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
      <span class="scan-path-value" :title="displayPath(status.current_path) || undefined">{{
        displayPath(status.current_path) ||
        (status.status === 'cancelling' ? '等待当前读取结束…' : '准备扫描…')
      }}</span>
    </div>
    <div class="scan-timing">
      <span v-if="status.finished">上次完成 {{ formatTime(status.finished) }}</span>
      <span v-if="status.next_scan">下次扫描 {{ formatTime(status.next_scan) }}</span>
      <span v-else-if="!active && !status.finished">可随时手动扫描目录及子目录。</span>
      <span v-if="active" class="scan-phase">{{
        status.status === 'cancelling' ? '停止后保留已同步的目录' : '正在读取并同步目录索引'
      }}</span>
    </div>
    <details v-if="status.errors?.length" class="scan-errors"
      ><summary>{{ status.errors.length }} 个目录读取失败，保留上次完整列表</summary
      ><ul
        ><li v-for="failure in status.errors" :key="JSON.stringify(failure)"
          ><strong>{{ failureScope(failure) }}</strong
          >：{{ errorText(failure.error) }}</li
        ></ul
      ></details
    >
  </section>
</template>
<style scoped>
  .scan-status {
    min-width: 0;
    padding: var(--space-16) var(--space-18);
    border: 1px solid var(--palette-dfe8e3);
    border-radius: var(--radius-9);
    background: var(--palette-f7faf8);
  }
  .scan-status.is-active {
    border-color: var(--palette-bcd7cb);
  }
  .scan-status.is-warning {
    border-color: var(--palette-ebd4c6);
    background: var(--palette-fffbf8);
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
    gap: var(--space-12);
    min-height: 32px;
  }
  .scan-status-identity {
    flex-wrap: wrap;
    min-width: 0;
    gap: var(--space-6) var(--space-12);
  }
  .scan-status-label {
    gap: var(--space-7);
    font-size: var(--font-size-13);
    white-space: nowrap;
  }
  .is-active .scan-status-label {
    color: var(--tone-green-ink);
  }
  .is-warning .scan-status-label {
    color: var(--palette-a65c33);
  }
  .scan-source {
    gap: var(--space-10);
    font-size: var(--font-size-11);
    color: var(--ash);
    white-space: nowrap;
  }
  .scan-elapsed {
    font-variant-numeric: tabular-nums;
  }
  .scan-stop {
    flex: none;
    padding: var(--space-5) var(--space-10);
    font-size: var(--font-size-12);
    color: var(--steel);
  }
  .scan-counts {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) minmax(0, 2fr);
    gap: var(--space-18);
    margin: var(--space-12) 0 0;
    padding-top: var(--space-12);
    border-top: 1px solid var(--palette-e3ebe6);
  }
  .scan-metric {
    min-width: 0;
  }
  .scan-metric dt {
    color: var(--ash);
    font-size: var(--font-size-11);
  }
  .scan-metric dd {
    margin: var(--space-3) 0 0;
    font-variant-numeric: tabular-nums;
    font-size: var(--font-size-19);
    font-weight: var(--font-weight-600);
    line-height: var(--line-height-1-4);
  }
  .scan-changes {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: var(--space-14);
    padding-left: var(--space-18);
    border-left: 1px solid var(--palette-e3ebe6);
  }
  .scan-changes dd {
    color: var(--steel);
    font-size: var(--font-size-16);
    line-height: var(--line-height-1-65);
  }
  .scan-path {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    min-width: 0;
    margin-top: var(--space-12);
    padding: var(--space-7) var(--space-10);
    border-radius: var(--radius-5);
    background: var(--palette-ecf3ef);
    font-size: var(--font-size-11);
    color: var(--palette-597266);
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
    gap: var(--space-4) var(--space-18);
    margin-top: var(--space-10);
    color: var(--ash);
    font-size: var(--font-size-11);
    font-variant-numeric: tabular-nums;
    line-height: var(--line-height-1-7);
  }
  .scan-phase {
    margin-left: auto;
  }
  .scan-errors {
    margin-top: var(--space-12);
    padding-top: var(--space-10);
    border-top: 1px solid var(--palette-ebddd2);
    color: var(--palette-a65c33);
    font-size: var(--font-size-12);
  }
  .scan-errors summary {
    cursor: pointer;
    line-height: var(--line-height-1-7);
  }
  .scan-errors ul {
    max-height: 200px;
    overflow: auto;
    padding-left: var(--space-20);
    line-height: var(--line-height-1-7);
    overflow-wrap: anywhere;
  }
  .scan-status.is-compact {
    padding-block: var(--space-10);
  }
  .is-compact .scan-counts {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-8) var(--space-22);
    margin-top: var(--space-2);
    padding-top: 0;
    border-top: 0;
  }
  .is-compact .scan-metric {
    display: flex;
    align-items: baseline;
    gap: var(--space-6);
  }
  .is-compact .scan-metric dd {
    margin: 0;
    font-size: var(--font-size-12);
    line-height: var(--line-height-1-5);
  }
  .is-compact .scan-changes {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-14);
  }
  .is-compact .scan-timing {
    margin-top: var(--space-5);
  }
  @media (max-width: 600px) {
    .scan-status {
      padding: var(--space-12);
    }
    .scan-status-heading {
      align-items: flex-start;
    }
    .scan-status-identity {
      flex-direction: column;
      align-items: flex-start;
      gap: var(--space-5);
    }
    .scan-counts {
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: var(--space-12);
    }
    .scan-changes {
      grid-column: 1 / -1;
      padding: var(--space-10) 0 0;
      border-left: 0;
      border-top: 1px solid var(--palette-e3ebe6);
    }
    .is-compact .scan-changes {
      width: 100%;
      padding: 0;
      border: 0;
    }
    .is-compact .scan-counts {
      margin-top: var(--space-8);
    }
    .scan-phase {
      margin-left: 0;
    }
  }
</style>
