<script setup lang="ts">
  import { formatCount, formatPercent } from '../../lib/format/number';
  import { errorText } from '../../app/errors';
  import { computed, nextTick, ref } from 'vue';
  import {
    Check,
    Minus,
    X,
    Folder,
    FileText,
    CircleAlert,
    Settings2,
    ChevronRight,
    Copy,
    Pause,
    Play,
    Clock3
  } from '@lucide/vue';
  import ModalDialog from '../../components/ModalDialog.vue';
  import StatusBadge from '../../components/StatusBadge.vue';
  import DateTime from '../../components/DateTime.vue';
  import SelectField from '../../components/SelectField.vue';
  import PageNavigation from '../../components/PageNavigation.vue';
  import { formatClock, formatTime } from '../../lib/format/date';
  import { statusNames } from '../../app/labels';
  import { isActiveRunStatus, type Run } from '../../app/contracts';
  import { displayPath } from '../../lib/format/path';
  import { copyText } from '../../lib/clipboard/clipboard';
  import { useRunRecords, type RecordFilter, type RunItem } from './use-run-records';
  import RunLiveRecords from './RunLiveRecords.vue';
  import './run-detail.css';

  const { run, elapsed } = defineProps<{
    run: Run;
    elapsed: string;
  }>();
  const emit = defineEmits<{
    close: [];
    pause: [];
    cancel: [];
    notice: [message: string];
  }>();
  const active = computed(() => isActiveRunStatus(run.status));
  const sections = ref({ records: true, config: false });
  const {
    state: records,
    refresh: refreshRecords,
    filter,
    directory
  } = useRunRecords(
    () => run.id,
    () => sections.value.records,
    () => active.value
  );
  const closedDirectories = ref(new Set<string>());
  const expandedAttempts = ref(new Set<number>());
  const directoryOptions = computed(() => [
    { value: 'all', label: `全部目录（${records.directories.length}）` },
    ...records.directories.map((d) => ({
      value: `path:${d.path}`,
      label: `${d.path || '媒体根目录'}（${d.total}）`
    }))
  ]);
  const groups = computed(() => {
    const groups = new Map<string, RunItem[]>();
    for (const item of records.items) {
      const files = groups.get(item.directory) || [];
      files.push(item);
      groups.set(item.directory, files);
    }
    return [...groups].map(([path, items]) => ({
      path,
      items,
      stats: records.directories.find((d) => d.path === path)
    }));
  });
  const selectedStats = computed(() =>
    records.directories.find((d) => `path:${d.path}` === directory.value)
  );
  const filters = computed(() => {
    const d = selectedStats.value;
    return [
      { value: 'all', label: '全部文件', count: d?.total ?? records.all_total },
      { value: 'running', label: '处理中', count: d?.running ?? records.running_total },
      { value: 'done', label: '已完成', count: d?.done ?? records.done_total },
      { value: 'skipped', label: '已跳过', count: d?.skipped ?? records.skipped_total },
      { value: 'failed', label: '最终失败', count: d?.failed ?? records.failed_total }
    ] satisfies { value: RecordFilter; label: string; count: number }[];
  });
  const filterNoun: Record<RecordFilter, string> = {
    all: '',
    running: '处理中',
    done: '完成',
    skipped: '跳过',
    failed: '失败'
  };
  const groupCount = (stats?: (typeof records.directories)[number]) => {
    if (!stats) return 0;
    return filter.value === 'all' ? stats.total : stats[filter.value];
  };
  const captions: Record<RecordFilter, string> = {
    all: '按提交时的目录分组，包含排队文件；失败次数在文件内展开。',
    running: '正在处理的文件按开始时间排列，最久的在前；下方是最近结束的文件。',
    done: '按提交时的目录分组，列出本批次已完成的文件及新文件名。',
    skipped: '按提交时的目录分组，列出未处理的文件及跳过原因。',
    failed: '按提交时的目录分组，只列出重试后仍失败的文件。'
  };
  const emptyText = computed(() => {
    if (records.busy) return '正在读取运行记录…';
    if (records.error) return '暂时无法读取运行记录';
    if (filter.value === 'all')
      return active.value ? '发现文件后会显示在这里，排队文件也会保留。' : '当前范围没有文件任务。';
    return `当前范围没有${filters.value.find((f) => f.value === filter.value)?.label}的文件。`;
  });
  function toggleDirectory(path: string) {
    if (closedDirectories.value.has(path)) closedDirectories.value.delete(path);
    else closedDirectories.value.add(path);
  }
  function toggleAttempts(id: number) {
    if (expandedAttempts.value.has(id)) expandedAttempts.value.delete(id);
    else expandedAttempts.value.add(id);
  }
  const promptOpen = ref(false);
  const copyError = ref('');
  const processed = computed(() => (run.done || 0) + (run.skipped || 0) + (run.failed || 0));
  const percent = (n: number) => formatPercent(Math.min(n, run.discovered), run.discovered);
  const progress = computed(() =>
    run.discovered > 0 ? Math.min(100, (processed.value / run.discovered) * 100) : 0
  );
  const stats = computed(() => [
    { label: '已完成', value: run.done || 0, tone: 'success' },
    { label: '已跳过', value: run.skipped || 0, tone: 'muted' },
    { label: '失败', value: run.failed || 0, tone: 'error' },
    { label: '总任务', value: run.discovered || 0, tone: 'primary' }
  ]);
  const tone = (status: string) => {
    if (['done', 'completed', 'success'].includes(status)) return 'success';
    if (['failed', 'partial_failure', 'interrupted'].includes(status)) return 'error';
    if (['queued', 'paused', 'cancelling'].includes(status)) return 'warning';
    if (['running', 'scanning'].includes(status)) return 'primary';
    return 'muted';
  };
  const clock = (stamp?: string) => (stamp ? formatClock(stamp) : '—');
  const processingLabels: Record<string, string> = {
    paused: '已暂停',
    scanning: '扫描与检查',
    cancelling: '正在取消'
  };
  function submittedTone(r: Run) {
    if (r.status === 'queued') return 'warning';
    return r.created ? 'primary' : 'pending';
  }
  function processingValue(r: Run) {
    if (r.started) return `${active.value ? '已运行' : '运行'} ${elapsed}`;
    return r.status === 'scanning' ? '正在扫描目标目录' : '等待开始';
  }
  function processingTone(r: Run, stateTone: string) {
    if (active.value && r.status !== 'queued') return stateTone;
    return r.started ? 'primary' : 'pending';
  }
  const lifecycle = computed(() => {
    const r = run;
    const ended = !active.value;
    const stateTone = tone(r.status);
    const steps = [
      {
        label: '提交',
        value: clock(r.created),
        tone: submittedTone(r),
        reached: !!r.created,
        current: r.status === 'queued'
      },
      {
        label: '开始',
        value: clock(r.started),
        tone: r.started ? 'primary' : 'pending',
        reached: !!r.started,
        current: false
      },
      {
        label: processingLabels[r.status] ?? '处理中',
        value: processingValue(r),
        tone: processingTone(r, stateTone),
        reached: !!r.started,
        current: active.value && r.status !== 'queued'
      },
      {
        label: ended ? statusNames[r.status] || r.status : '完成',
        value: clock(r.finished),
        tone: ended ? stateTone : 'pending',
        reached: ended,
        current: false
      }
    ];
    return steps.map((step, i) => ({
      ...step,
      linkReached: step.reached && !!(steps[i + 1]?.reached || steps[i + 1]?.current)
    }));
  });
  const targetNames = computed(() =>
    run.targets.map((target, i) =>
      run.target_names?.[i] ? displayPath(run.target_names[i]) : `条目 #${target}`
    )
  );
  const retryLimit = computed(() =>
    run.request_config?.retry_enabled ? (run.request_config.retry_count ?? 0) : 0
  );
  const path = (item: RunItem) => item.current_path || item.original_path || `文件 #${item.entry}`;
  const lastAttempt = (item: RunItem) => item.attempts?.at(-1);
  const attemptSummary = (item: RunItem) => {
    const last = lastAttempt(item);
    if (last?.status === 'success') {
      if (last.fallback) return '兜底成功';
      return (item.attempts?.length || 0) > 1 ? '重试成功' : '主模型成功';
    }
    if (item.status === 'running') return '处理中';
    if (item.status === 'cancelled') return '已取消';
    return item.status === 'failed' ? '请求失败' : '请求记录';
  };
  const itemStamp = (item: RunItem) =>
    item.updated_ms ? new Date(item.updated_ms).toISOString() : lastAttempt(item)?.started;
  const nameLabel = (item: RunItem) => {
    if (!item.new_name || item.operation_status === 'committed') return '新文件名';
    return item.operation_status === 'failed' ? '拟用名称 · 改名失败' : '拟用名称 · 待核对';
  };
  const nameValue = (item: RunItem) => {
    if (run.task === 'extract') return '— 独立抽帧，不改名';
    if (item.new_name) return item.new_name;
    if (item.status === 'queued') return '— 等待处理';
    if (item.status === 'running') return '— 处理中';
    if (item.status === 'skipped') return '— 已跳过';
    if (item.status === 'cancelled') return '— 已取消';
    if (item.status === 'done') return '— 未记录改名结果';
    return lastAttempt(item)?.status === 'success' ? '— 未记录拟用名称' : '— 未生成有效名称';
  };
  const promptBody = ref<HTMLElement>();
  async function inspectPrompt() {
    promptOpen.value = !promptOpen.value;
    if (promptOpen.value) {
      await nextTick();
      promptBody.value?.scrollIntoView({ block: 'nearest' });
    }
  }
  async function copyPath(value: string) {
    copyError.value = '';
    try {
      await copyText(value);
      emit('notice', '路径已复制');
    } catch (e) {
      copyError.value = errorText(e);
    }
  }
  function stepState(step: { current: boolean; reached: boolean }) {
    if (step.current) return '当前阶段';
    return step.reached ? '已到达' : '未到达';
  }
  const recordsSummary = computed(() => {
    if (records.loaded) return `${records.all_total} 个文件 · ${records.directories.length} 个目录`;
    return records.error ? '读取失败' : '正在读取…';
  });
  const recordsPageSummary = computed(() => {
    if (records.loaded) return `共 ${records.total} 个文件 · 每页 100 项`;
    return records.error ? '读取失败' : '正在读取…';
  });
  const attemptStatuses: Record<string, string> = { success: '成功', cancelled: '已取消' };
  const attemptStatus = (status: string) => attemptStatuses[status] ?? '失败';
  const triggerLabel = computed(() => {
    if (run.source === 'schedule') return '定时触发';
    return run.plan ? '计划立即运行' : '手动触发';
  });
</script>
<template>
  <ModalDialog :title="`运行 #${run.id}`" wide class="run-detail-dialog" @close="emit('close')">
    <template #header>
      <StatusBadge class="detail-header-status" :status="run.status">{{
        statusNames[run.status] || run.status
      }}</StatusBadge>
      <p class="detail-header-meta"
        ><span>提交于 {{ formatTime(run.created) }}</span
        ><span><Clock3 :size="13" />{{ active ? '已耗时' : '总耗时' }} {{ elapsed }}</span></p
      >
    </template>
    <div class="run-detail">
      <ol class="detail-lifecycle" aria-label="批次生命周期">
        <li
          v-for="step in lifecycle"
          :key="step.label"
          :data-tone="step.tone"
          :data-reached="step.reached || undefined"
          :data-link-reached="step.linkReached || undefined"
          :aria-label="`${step.label} · ${stepState(step)}`"
          :aria-current="step.current ? 'step' : undefined"
          :class="{ 'is-live': step.current && run.status === 'running' }"
        >
          <span class="lifecycle-node" aria-hidden="true"
            ><template v-if="step.current"
              ><Pause v-if="run.status === 'paused'" :size="12" />
              <Clock3 v-else-if="run.status === 'queued'" :size="12" />
              <span v-else class="lifecycle-current-dot" /></template
            ><X v-else-if="step.tone === 'error'" :size="12" />
            <Minus v-else-if="step.tone === 'muted' && step.reached" :size="12" />
            <Check v-else-if="step.reached" :size="12"
          /></span>
          <strong>{{ step.label }}</strong
          ><small>{{ step.value }}</small>
        </li>
      </ol>
      <div class="detail-statistics" aria-label="批次统计">
        <div
          v-for="(stat, i) in stats"
          :key="stat.label"
          class="detail-statistic"
          :data-tone="stat.tone"
        >
          <strong>{{ formatCount(stat.value) }}</strong
          ><span :title="i === 3 ? '已发现任务' : undefined"
            ><span>{{ stat.label }}</span
            ><small v-if="i < 3">{{ percent(stat.value) }}</small></span
          >
        </div>
      </div>
      <div v-if="run.task !== 'extract'" class="detail-metadata">
        <span>{{
          run.request_config?.retry_enabled ? `失败最多重试 ${retryLimit} 次` : '自动重试已关闭'
        }}</span>
        <span class="metadata-separator" aria-hidden="true">·</span>
        <span class="metadata-model" :title="run.request_config?.fallback_model_title"
          >兜底模型：{{ run.request_config?.fallback_model_title || '不使用' }}</span
        >
        <template v-if="run.prompt_body">
          <span class="metadata-separator" aria-hidden="true">·</span>
          <button
            class="detail-prompt-tag"
            :aria-expanded="promptOpen"
            aria-controls="run-prompt-snapshot"
            :title="`提示词快照 · 配置：${run.model_config_title || '未记录'}`"
            @click="promptOpen = !promptOpen"
            ><ChevronRight :size="13" :class="{ 'is-open': promptOpen }" /><span
              >提示词快照 · {{ run.model_config_title || '查看内容' }}</span
            ></button
          >
        </template>
      </div>
      <div v-if="promptOpen" id="run-prompt-snapshot" ref="promptBody" class="detail-prompt-body"
        ><p class="detail-caption">创建批次时保存的完整提示词</p><pre>{{ run.prompt_body }}</pre>
      </div>
      <div v-if="run.task === 'extract'" class="detail-metadata"
        ><span>独立抽帧 · 不使用模型</span></div
      >
      <div v-if="run.error" role="alert" class="detail-run-error"
        ><CircleAlert :size="16" /><span>{{ errorText(run.error) }}</span></div
      >
      <div class="detail-batch-progress">
        <div
          ><span>批次处理进度 <small>含完成、跳过与失败</small></span
          ><strong
            >{{ formatCount(processed) }} / {{ formatCount(run.discovered || 0)
            }}<span>{{ percent(processed) }}</span></strong
          ></div
        >
        <div
          class="detail-progress-track"
          role="progressbar"
          aria-label="批次处理进度"
          :aria-valuenow="progress"
          :aria-valuemin="0"
          :aria-valuemax="100"
          ><span :style="{ width: `${progress}%` }" :data-tone="tone(run.status)"
        /></div>
      </div>
      <div class="detail-sections">
        <section class="detail-section">
          <button
            class="detail-section-toggle"
            :aria-expanded="sections.records"
            aria-controls="run-detail-records"
            @click="sections.records = !sections.records"
          >
            <FileText :size="17" /><strong>运行记录</strong
            ><span class="section-summary">{{ recordsSummary }}</span
            ><ChevronRight :size="16" :class="{ 'is-open': sections.records }" />
          </button>
          <div
            v-show="sections.records"
            id="run-detail-records"
            class="detail-section-content"
            :aria-busy="records.busy"
          >
            <div class="detail-record-toolbar">
              <div class="detail-record-filters" role="group" aria-label="文件状态筛选">
                <button
                  v-for="option in filters"
                  :key="option.value"
                  :aria-pressed="filter === option.value"
                  @click="filter = option.value"
                  >{{ option.label }} <span v-if="records.loaded">{{ option.count }}</span></button
                >
              </div>
              <SelectField v-model="directory" :options="directoryOptions" label="记录目录筛选" />
            </div>
            <p class="detail-caption"
              >{{ captions[filter] }}{{ active ? '运行期间自动刷新。' : '批次已结束。' }}</p
            >
            <ul
              v-if="filter === 'skipped' && records.skip_reasons.length"
              class="detail-skip-reasons"
              aria-label="跳过原因"
            >
              <li v-for="reason in records.skip_reasons" :key="JSON.stringify(reason.reason)"
                >{{ errorText(reason.reason) || '未记录原因' }}
                <strong>{{ reason.count }}</strong></li
              >
            </ul>
            <div v-if="records.error" class="detail-record-error" role="alert"
              ><span
                >运行记录读取失败：{{ records.error
                }}{{ records.items.length ? '；保留上次读取的记录。' : '' }}</span
              ><button :disabled="records.busy" @click="refreshRecords()">重试</button></div
            >
            <RunLiveRecords
              v-if="filter === 'running'"
              :items="records.items"
              :recent="records.recent"
              :task="run.task"
              :active="active"
              :loaded="records.loaded"
              @copy="copyPath"
            />
            <section
              v-for="(group, groupIndex) in filter === 'running' ? [] : groups"
              :key="group.path"
              class="detail-directory"
            >
              <button
                class="detail-directory-toggle"
                :aria-expanded="!closedDirectories.has(group.path)"
                :aria-controls="`run-directory-${groupIndex}`"
                @click="toggleDirectory(group.path)"
              >
                <ChevronRight
                  :size="15"
                  :class="{ 'is-open': !closedDirectories.has(group.path) }"
                /><Folder :size="16" /><strong :title="group.path || '媒体根目录'">{{
                  group.path || '媒体根目录'
                }}</strong>
                <span v-if="group.stats" class="detail-directory-counts"
                  >{{ group.stats.total }} 个文件<span v-if="group.stats.done">
                    · {{ group.stats.done }} 完成</span
                  ><span v-if="group.stats.failed" class="directory-failures">
                    · {{ group.stats.failed }} 失败</span
                  ><span v-if="group.stats.running"> · {{ group.stats.running }} 处理中</span
                  ><span v-if="group.stats.queued"> · {{ group.stats.queued }} 排队</span
                  ><span v-if="group.stats.skipped"> · {{ group.stats.skipped }} 跳过</span
                  ><span v-if="group.stats.cancelled">
                    · {{ group.stats.cancelled }} 取消</span
                  ></span
                >
              </button>
              <div
                v-show="!closedDirectories.has(group.path)"
                :id="`run-directory-${groupIndex}`"
                class="detail-directory-files"
              >
                <p class="detail-caption"
                  >本页 {{ group.items.length }} / {{ groupCount(group.stats) }} 个{{
                    filterNoun[filter]
                  }}文件</p
                >
                <article
                  v-for="item in group.items"
                  :key="item.id"
                  class="detail-record-file"
                  :data-tone="tone(item.status)"
                >
                  <div class="detail-record-heading"
                    ><StatusBadge :status="item.status">{{
                      item.status === 'failed'
                        ? '最终失败'
                        : statusNames[item.status] || item.status
                    }}</StatusBadge
                    ><span v-if="item.undone" class="detail-caption">本次改名已还原</span
                    ><DateTime :value="itemStamp(item)" /><button
                      class="icon-btn"
                      :aria-label="`复制当前路径 ${item.original_name}`"
                      :title="path(item)"
                      @click="copyPath(path(item))"
                      ><Copy :size="14" /></button
                  ></div>
                  <dl class="detail-record-names"
                    ><div
                      ><dt>原文件名</dt><dd>{{ item.original_name || '未记录原名' }}</dd></div
                    ><div
                      ><dt>{{ nameLabel(item) }}</dt
                      ><dd>{{ nameValue(item) }}</dd></div
                    ></dl
                  >
                  <p v-if="item.message" class="detail-record-message">{{
                    errorText(item.message)
                  }}</p>
                  <div v-if="item.attempts?.length" class="attempt-history">
                    <button
                      class="detail-attempt-toggle"
                      :aria-expanded="expandedAttempts.has(item.id)"
                      :aria-controls="`run-attempts-${item.id}`"
                      @click="toggleAttempts(item.id)"
                      ><ChevronRight
                        :size="13"
                        :class="{ 'is-open': expandedAttempts.has(item.id) }"
                      />全部 {{ item.attempts.length }} 次尝试 · {{ attemptSummary(item) }}</button
                    >
                    <div v-show="expandedAttempts.has(item.id)" :id="`run-attempts-${item.id}`">
                      <div
                        v-for="attempt in item.attempts"
                        :key="attempt.number"
                        class="detail-attempt"
                        :data-tone="tone(attempt.status)"
                        ><strong
                          >{{
                            attempt.number === 1 ? '首次请求' : `重试第 ${attempt.number - 1} 次`
                          }}
                          · {{ attempt.fallback ? '兜底模型' : '主模型' }}</strong
                        ><dl
                          ><dt>模型</dt
                          ><dd>{{ attempt.model_config_title }} · {{ attempt.model }}</dd
                          ><dt>开始时间</dt><dd><DateTime :value="attempt.started" /></dd
                          ><dt>耗时</dt><dd>{{ attempt.duration_ms }} ms</dd><dt>请求状态</dt
                          ><dd>{{ attemptStatus(attempt.status) }}</dd
                          ><dt>错误信息</dt><dd>{{ errorText(attempt.error) || '—' }}</dd></dl
                        ></div
                      >
                    </div>
                  </div>
                </article>
              </div>
            </section>
            <p
              v-if="!records.items.length && (filter !== 'running' || !records.loaded)"
              class="detail-empty"
              role="status"
              >{{ emptyText }}</p
            >
            <div class="pagination" aria-label="运行记录分页"
              ><span class="subtle">{{ recordsPageSummary }}</span
              ><PageNavigation
                :page="records.page"
                :total-pages="Math.ceil(records.total / 100)"
                :disabled="records.busy"
                previous-label="上页文件"
                next-label="下页文件"
                label="运行记录翻页"
                @change="refreshRecords"
            /></div>
          </div>
        </section>
        <section class="detail-section">
          <button
            class="detail-section-toggle"
            :aria-expanded="sections.config"
            aria-controls="run-detail-config"
            @click="sections.config = !sections.config"
            ><Settings2 :size="17" /><strong>批次配置与提交范围</strong
            ><span class="section-summary">{{
              run.task === 'extract' ? '独立抽帧' : '自动命名'
            }}</span
            ><ChevronRight :size="16" :class="{ 'is-open': sections.config }"
          /></button>
          <div v-show="sections.config" id="run-detail-config" class="detail-section-content"
            ><div class="detail-targets"
              ><span v-for="target in targetNames" :key="target"
                ><Folder :size="15" /><span :title="target">{{ target }}</span></span
              ><small class="detail-recursive">{{
                run.recursive ? '包含子目录' : '仅所选目标的当前层'
              }}</small></div
            ><dl class="detail-config">
              <div
                ><dt>任务类型</dt
                ><dd>{{ run.task === 'extract' ? '独立抽帧' : '自动命名' }}</dd></div
              ><div
                ><dt>触发方式</dt><dd>{{ triggerLabel }}</dd></div
              >
              <template v-if="run.task !== 'extract'"
                ><div
                  ><dt>最大重试次数</dt><dd>{{ retryLimit }}</dd></div
                ><div
                  ><dt>模型配置</dt><dd>{{ run.model_config_title || '—' }}</dd></div
                ><div
                  ><dt>主模型</dt><dd>{{ run.request_config?.model || '—' }}</dd></div
                ><div
                  ><dt>兜底模型</dt
                  ><dd>{{ run.request_config?.fallback_model_title || '不使用' }}</dd></div
                ><div
                  ><dt>提示词</dt
                  ><dd
                    ><button
                      v-if="run.prompt_body"
                      class="detail-config-prompt"
                      :aria-expanded="promptOpen"
                      aria-controls="run-prompt-snapshot"
                      @click="inspectPrompt"
                      >{{ promptOpen ? '收起' : '查看' }}提示词快照</button
                    ><span v-else>—</span></dd
                  ></div
                ></template
              >
              <div
                ><dt>包含子目录</dt><dd>{{ run.recursive ? '是' : '否' }}</dd></div
              ><div
                ><dt>提交时间</dt><dd>{{ formatTime(run.created) }}</dd></div
              ><div
                ><dt>开始时间</dt><dd>{{ run.started ? formatTime(run.started) : '—' }}</dd></div
              ><div
                ><dt>结束时间</dt><dd>{{ run.finished ? formatTime(run.finished) : '—' }}</dd></div
              ><div
                ><dt>总耗时</dt><dd>{{ elapsed }}</dd></div
              > </dl
            ><p class="detail-caption">总耗时不含排队等待，含暂停时间。</p></div
          >
        </section>
      </div>
    </div>
    <template v-if="active || copyError" #footer
      ><div class="detail-footer"
        ><span v-if="copyError" role="alert" class="detail-copy-error">{{ copyError }}</span
        ><div class="modal-actions"
          ><button
            v-if="['queued', 'scanning', 'running', 'paused'].includes(run.status)"
            @click="emit('pause')"
            ><Play v-if="run.status === 'paused'" :size="15" /><Pause v-else :size="15" />{{
              run.status === 'paused' ? '继续' : '暂停'
            }}</button
          ><button v-if="active" class="cancel-run" @click="emit('cancel')"
            ><X :size="15" />取消</button
          ></div
        ></div
      ></template
    >
  </ModalDialog>
</template>
