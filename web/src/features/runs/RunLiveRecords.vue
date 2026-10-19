<script setup lang="ts">
  import { computed, onUnmounted, ref, watch } from 'vue';
  import { Check, Clock3, Copy, Folder, History } from '@lucide/vue';
  import { type RecentItem, type RunItem } from './use-run-records';

  const props = defineProps<{
    items: RunItem[];
    recent: RecentItem[];
    task?: 'rename' | 'extract';
    active: boolean;
    loaded: boolean;
  }>();
  const emit = defineEmits<{ copy: [path: string] }>();

  // Elapsed times tick locally between the 3-second record reads.
  const now = ref(Date.now());
  let ticker: ReturnType<typeof setInterval> | undefined;
  watch(
    () => props.active,
    (active) => {
      clearInterval(ticker);
      if (active) ticker = setInterval(() => (now.value = Date.now()), 1000);
    },
    { immediate: true }
  );
  onUnmounted(() => clearInterval(ticker));

  const duration = (ms: number) => {
    const s = Math.max(0, Math.floor(ms / 1000));
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    const pad = (n: number) => String(n).padStart(2, '0');
    return h ? `${h}:${pad(m)}:${pad(s % 60)}` : `${m}:${pad(s % 60)}`;
  };
  const elapsed = (from?: number) => (from ? duration(now.value - from) : '—');
  const rename = computed(() => props.task !== 'extract');

  type Step = { label: string; state: 'done' | 'current' | 'pending' };
  function steps(item: RunItem): Step[] {
    const s = item.stage;
    const position = !s ? 0 : s.stage === 'extracting' ? 1 : s.stage === 'committing' ? 3 : 2;
    const at = (i: number): Step['state'] =>
      i < position ? 'done' : i === position ? 'current' : 'pending';
    const frames = s?.frames ? `抽帧 ${s.frames} 张` : '抽帧';
    const extract = { label: position === 1 ? '抽帧中' : frames, state: at(1) };
    if (!rename.value)
      return [{ label: '开始', state: at(0) }, extract, { label: '完成', state: 'pending' }];
    let model = '模型请求';
    if (s && position === 2) {
      if (s.stage === 'model_wait') model = '等待模型名额';
      else if (s.stage === 'retry_wait') model = '等待重试';
      else if (s.fallback) model = '兜底模型请求';
      else model = s.attempt > 1 ? `重试第 ${s.attempt - 1} 次` : '首次请求';
    } else if (position > 2) model = '模型已返回';
    return [
      { label: '开始', state: at(0) },
      extract,
      { label: model, state: at(2) },
      { label: position === 3 ? '提交改名中' : '提交改名', state: at(3) },
      { label: '完成', state: 'pending' }
    ];
  }
  const proposed = (item: RunItem) => item.stage?.title || '';
  const lastFailure = (item: RunItem) => {
    const last = item.attempts?.at(-1);
    if (!last || last.status !== 'failed') return '';
    const label = last.number === 1 ? '首次请求' : `重试第 ${last.number - 1} 次`;
    return `${label}失败：${last.message || '未记录原因'}`;
  };
  const path = (item: RunItem) => item.current_path || item.original_path;

  const recentTone = (status: string) =>
    status === 'done'
      ? 'success'
      : status === 'failed'
        ? 'error'
        : status === 'skipped'
          ? 'warning'
          : 'muted';
  const recentLabel = (item: RecentItem) =>
    item.status === 'failed'
      ? '最终失败'
      : item.status === 'skipped'
        ? '已跳过'
        : item.status === 'cancelled'
          ? '已取消'
          : '已完成';
  const recentMeta = (item: RecentItem) => {
    const parts = [recentLabel(item)];
    if (item.started_ms && item.finished_ms >= item.started_ms)
      parts.push(`用时 ${duration(item.finished_ms - item.started_ms)}`);
    if (item.attempts > 1) parts.push(`${item.attempts} 次尝试`);
    if (item.status === 'skipped' && item.message) parts.push(item.message);
    return parts.join(' · ');
  };
  const recentResult = (item: RecentItem) => {
    if (item.new_name && item.operation_status === 'committed') return '';
    if (item.status === 'failed') return item.message || '未生成有效名称';
    if (item.status === 'cancelled') return '已取消';
    if (item.status === 'skipped') return '保留原名';
    return rename.value ? '未记录改名结果' : '抽帧完成';
  };
  const clock = (ms: number) =>
    ms ? new Date(ms).toLocaleTimeString('zh-CN', { hour12: false }) : '—';
</script>

<template>
  <div class="run-live">
    <article
      v-for="item in items"
      :key="item.id"
      class="run-live-file"
      :data-tone="item.stage?.stage === 'retry_wait' ? 'warning' : 'primary'"
    >
      <div class="run-live-heading"
        ><Folder :size="15" /><span
          class="run-live-directory"
          :title="item.directory || '媒体根目录'"
          >{{ item.directory || '媒体根目录' }}</span
        ><span class="run-live-elapsed"
          ><Clock3 :size="14" />已用时 {{ elapsed(item.updated_ms)
          }}<template v-if="item.stage?.since_ms">
            · 本阶段 {{ elapsed(item.stage.since_ms) }}</template
          ></span
        ><button
          class="icon-btn"
          :aria-label="`复制当前路径 ${item.original_name}`"
          :title="path(item)"
          @click="emit('copy', path(item))"
          ><Copy :size="14" /></button
      ></div>
      <dl class="run-live-names"
        ><div
          ><dt>原名</dt><dd>{{ item.original_name || '未记录原名' }}</dd></div
        ><div v-if="rename"
          ><dt>{{ proposed(item) ? '拟用名称' : '新名' }}</dt
          ><dd :class="{ 'is-pending': !proposed(item) }">{{
            proposed(item) || '等待模型生成…'
          }}</dd></div
        ></dl
      >
      <ol class="run-live-steps" aria-label="处理阶段">
        <li
          v-for="step in steps(item)"
          :key="step.label"
          :data-state="step.state"
          :aria-current="step.state === 'current' ? 'step' : undefined"
          ><span class="run-live-node"><Check v-if="step.state === 'done'" :size="10" /></span
          >{{ step.label }}</li
        >
      </ol>
      <p v-if="lastFailure(item)" class="run-live-failure">{{ lastFailure(item) }}</p>
    </article>
    <p v-if="loaded && !items.length" class="detail-empty" role="status">{{
      active
        ? '当前没有处理中的文件，可能正在扫描或等待处理名额。'
        : '批次已结束，没有处理中的文件。'
    }}</p>

    <section class="run-live-recent" aria-labelledby="run-live-recent-title">
      <h4 id="run-live-recent-title"
        ><History :size="16" />最近结束<span class="detail-caption">最近 20 个，新的在上</span></h4
      >
      <ol v-if="recent.length">
        <li v-for="item in recent" :key="item.id" :data-tone="recentTone(item.status)">
          <span class="run-live-recent-meta"
            ><time>{{ clock(item.finished_ms) }}</time> · {{ recentMeta(item) }}</span
          >
          <span class="run-live-recent-names"
            ><span :title="item.original_name">{{ item.original_name }}</span
            ><template v-if="!recentResult(item)"
              ><span class="run-live-arrow" aria-label="改名为">→</span
              ><strong :title="item.new_name">{{ item.new_name }}</strong
              ><span v-if="item.undone" class="detail-caption">已还原</span></template
            ><span v-else class="run-live-result" :title="recentResult(item)"
              >— {{ recentResult(item) }}</span
            ></span
          >
        </li>
      </ol>
      <p v-else-if="loaded" class="detail-caption">还没有处理结束的文件。</p>
    </section>
  </div>
</template>
