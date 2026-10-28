<script setup lang="ts">
  import { useRouteState, queryText, queryChoice, queryInteger } from '../../app/use-route-state';

  import { RouterLink } from 'vue-router';
  import { formatCount, formatCompact } from '../../lib/format/number';
  import { errorText } from '../../app/errors';
  import PageNavigation from '../../components/PageNavigation.vue';
  import { useToast } from '../../app/use-toast';
  import { ref, onMounted, onUnmounted, computed, watch } from 'vue';
  import {
    Pause,
    Play,
    Plus,
    Clock3,
    Folder,
    PenLine,
    CirclePlay,
    CircleCheck,
    CircleAlert,
    CircleMinus,
    Trash2,
    Info,
    X,
    RefreshCw,
    CalendarDays,
    Undo2,
    Images
  } from '@lucide/vue';
  import { useApi } from '../../app/use-api';
  import { displayPath } from '../../lib/format/path';
  import { formatClock, formatLongDate, formatTime } from '../../lib/format/date';
  import { statusNames } from '../../app/labels';
  import { isActiveRunStatus, type Plan, type Run, type ServerRecord } from '../../app/contracts';
  import StatusBadge from '../../components/StatusBadge.vue';
  import './runs.css';
  import ModalDialog from '../../components/ModalDialog.vue';
  import ModalTransition from '../../components/ModalTransition.vue';
  import RunDetail from './RunDetail.vue';
  import SelectField from '../../components/SelectField.vue';
  import { statusOptions, useRunFilters } from './run-filters';
  import { processed, progress, useRunTiming } from './run-timing';
  import PlanDialog from './PlanDialog.vue';
  import type { PlanMode } from './plan-form';
  const { api, apiItems } = useApi();
  const toast = useToast();
  const {
    category,
    task,
    status,
    profile,
    duration,
    period,
    from,
    to,
    hasFilters,
    filterQuery,
    resetFilters
  } = useRunFilters();
  type RunPage = {
    items: Run[];
    total: number;
    offset: number;
    limit: number;
    profiles?: { id: number; title: string }[];
  };
  const profiles = ref<{ id: number; title: string }[]>([]);
  const pagination = ref({
    all: { page: 1, size: 50, total: 0 },
    manual: { page: 1, size: 50, total: 0 },
    automatic: { page: 1, size: 50, total: 0 }
  });
  const currentPage = computed(() => pagination.value[category.value]);
  const pageCount = computed(() =>
    Math.max(1, Math.ceil(currentPage.value.total / currentPage.value.size))
  );
  const routePage = computed({
    get: () => currentPage.value.page,
    set: (value) => {
      currentPage.value.page = value;
    }
  });
  const routeLimit = computed({
    get: () => currentPage.value.size,
    set: (value) => {
      currentPage.value.size = value;
    }
  });
  const detailID = ref(0);
  const routeState = useRouteState(
    {
      detail: queryInteger(detailID),
      category: queryChoice(category, ['all', 'manual', 'automatic']),
      task: queryChoice(task, ['', 'rename', 'extract']),
      status: queryChoice(status, [
        '',
        'queued',
        'scanning',
        'running',
        'paused',
        'cancelling',
        'completed',
        'partial_failure',
        'failed',
        'cancelled',
        'interrupted'
      ]),
      profile: queryText(profile, (value) => /^\d*$/.test(value)),
      duration: queryChoice(duration, ['', 'under1', '1to5', '5to30', '30to60', 'over60']),
      period: queryChoice(period, ['all', 'today', '1', '7', '30', 'custom']),
      from: queryText(from, (value) => !Number.isNaN(Date.parse(value))),
      to: queryText(to, (value) => !Number.isNaN(Date.parse(value))),
      page: queryInteger(routePage, { min: 1 }),
      limit: queryInteger(routeLimit, { choices: [50, 100, 200] })
    },
    {
      onRestore: () => {
        void load();
      }
    }
  );
  const loading = ref(false);
  let loadSequence = 0;
  function changePage(page: number) {
    void load(page);
  }
  function changeSize() {
    currentPage.value.page = 1;
    void load();
  }
  watch(category, () => {
    if (routeState.isRestoring.value) return;
    runs.value = [];
    void load();
  });
  watch([task, status, profile, duration, period, from, to], () => {
    if (routeState.isRestoring.value) return;
    for (const value of Object.values(pagination.value)) value.page = 1;
    runs.value = [];
    void load();
  });
  const { ready, view } = defineProps<{ ready: boolean; view: string }>();
  const runs = ref<Run[]>([]),
    plans = ref<Plan[]>([]),
    error = ref(''),
    planDialog = ref<{ mode: PlanMode; plan: Plan | null }>();
  type RestoreResult = {
    restored: number;
    skipped: number;
    failed: number;
    errors: { operation: number; name: string; error: ServerRecord }[];
  };
  const restoreTarget = ref<Run>(),
    restoring = ref(false),
    restoreError = ref(''),
    restoreResult = ref<RestoreResult>();
  function askRestore(r: Run) {
    restoreTarget.value = r;
    restoreError.value = '';
    restoreResult.value = undefined;
  }
  async function restoreBatch() {
    if (!restoreTarget.value || restoring.value) return;
    restoring.value = true;
    restoreError.value = '';
    try {
      restoreResult.value = await api<RestoreResult>(`/runs/${restoreTarget.value.id}/restore`, {
        method: 'POST',
        body: {}
      });
      if (!restoreResult.value.failed) {
        const { restored, skipped } = restoreResult.value;
        toast.show(`已还原 ${restored} 项 · 已还原跳过 ${skipped} 项 · 失败 0 项`);
      }
      await load();
    } catch (e) {
      restoreError.value = errorText(e);
    } finally {
      restoring.value = false;
    }
  }
  const pauseTarget = ref<Run>(),
    pauseBusy = ref(false),
    pauseError = ref('');
  const cancelTarget = ref<Run>(),
    cancelling = ref(false),
    cancelError = ref('');
  const detail = ref<Run>(),
    deletePlan = ref<Plan>();
  watch(detail, (value) => {
    detailID.value = value?.id ?? 0;
  });
  let timer: ReturnType<typeof setTimeout> | undefined,
    alive = true;
  const sourceLabel = (r: Run) => {
    if (r.source === 'schedule') return '定时触发';
    return r.plan ? '计划立即运行' : '手动触发';
  };
  function groupDays(list: Run[]) {
    const days: { key: string; label: string; runs: Run[] }[] = [];
    for (const r of list) {
      const stamp = r.started || r.created;
      const date = stamp ? new Date(stamp) : null;
      const key = date ? `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}` : 'pending';
      const label = date ? formatLongDate(date) : '等待开始';
      const day = days.find((d) => d.key === key);
      if (day) day.runs.push(r);
      else days.push({ key, label, runs: [r] });
    }
    return days;
  }
  const runDays = computed(() => groupDays(runs.value));
  const { etas, elapsed, trackEta } = useRunTiming(() => runs.value);
  const active = (r: Run) => isActiveRunStatus(r.status);
  const pausable = (r: Run) => ['queued', 'scanning', 'running'].includes(r.status);
  const runStatusIcons = {
    queued: Clock3,
    scanning: RefreshCw,
    running: CirclePlay,
    paused: Pause,
    cancelling: CircleMinus,
    completed: CircleCheck,
    done: CircleCheck,
    failed: CircleAlert,
    partial_failure: CircleAlert,
    cancelled: CircleMinus,
    interrupted: CircleAlert
  };
  const runStatusIcon = (status: string) =>
    status in runStatusIcons ? runStatusIcons[status as keyof typeof runStatusIcons] : Info;
  const running = (r: Run) => active(r) && r.status !== 'paused' && !!r.started && !r.finished;
  const modelName = (r: Run) => r.model_config_title || r.request_config?.model || '—';
  const modelHint = (r: Run) =>
    [r.model_config_title, r.request_config?.model].filter(Boolean).join(' · ') || '未记录模型';
  function tokenHint(r: Run) {
    return r.token_estimated_records
      ? `累计 ${formatCount(r.token_count)} token；${r.token_estimated_records} 条请求使用文字粗估，未计图片 token 和无法还原的重试。`
      : `累计 ${formatCount(r.token_count)} token；来自模型返回的 usage。`;
  }
  const runClock = (r: Run) => {
    const stamp = r.started || r.created;
    return stamp ? formatClock(stamp).slice(0, 5) : '—';
  };
  let loadController: AbortController | undefined;
  async function load(requestedPage?: number) {
    loadController?.abort();
    const controller = new AbortController();
    loadController = controller;
    const sequence = ++loadSequence,
      selected = category.value,
      { size } = currentPage.value,
      page = requestedPage ?? currentPage.value.page;
    loading.value = true;
    try {
      if (view === 'plans') {
        const p = await apiItems<Plan>('/schedules', { signal: controller.signal });
        if (alive && sequence === loadSequence) {
          plans.value = p;
          error.value = '';
        }
        return;
      }
      const r = await api<RunPage>(
        `/runs?category=${selected}&task=${task.value}&offset=${(page - 1) * size}&limit=${size}&${filterQuery()}`,
        { signal: controller.signal }
      );
      if (!alive || sequence !== loadSequence) return;
      runs.value = r.items;
      profiles.value = r.profiles || [];
      pagination.value[selected] = {
        page: Math.floor(r.offset / r.limit) + 1,
        size: r.limit,
        total: r.total
      };
      trackEta(r.items);
      error.value = '';
      if (!detailID.value) detail.value = undefined;
      if (detailID.value) {
        const id = detailID.value;
        const run = await api<Run>(`/runs/${id}`, { signal: controller.signal });
        // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- the compared state can change while awaiting
        if (alive && sequence === loadSequence && detailID.value === id) detail.value = run;
      }
    } catch (e) {
      if (alive && sequence === loadSequence) error.value = errorText(e);
    } finally {
      if (alive && sequence === loadSequence) loading.value = false;
    }
  }
  async function poll() {
    if (document.visibilityState === 'visible' && !loading.value) await load();
    if (alive) timer = setTimeout(() => void poll(), 3000);
  }
  function manual() {
    planDialog.value = { mode: 'manual', plan: null };
  }
  function plan(p?: Plan) {
    planDialog.value = { mode: 'plan', plan: p ?? null };
  }
  function planSaved() {
    planDialog.value = undefined;
    void load();
  }
  async function toggle(p: Plan) {
    try {
      await api<unknown>(`/schedules/${p.id}`, {
        method: 'PATCH',
        headers: { 'If-Match': p.etag },
        body: { enabled: !p.enabled }
      });
      toast.show(p.enabled ? '计划已停用' : '计划已启用');
      await load();
    } catch (e) {
      error.value = errorText(e);
    }
  }
  async function runPlan(p: Plan) {
    try {
      const r = await api<{ id: number }>(`/schedules/${p.id}/run`, {
        method: 'POST',
        body: {}
      });
      toast.show(`运行 #${r.id} 已加入队列`);
      await load();
    } catch (e) {
      error.value = errorText(e);
    }
  }
  async function remove() {
    if (!deletePlan.value) return;
    try {
      await api<unknown>(`/schedules/${deletePlan.value.id}`, {
        method: 'DELETE',
        headers: { 'If-Match': deletePlan.value.etag },
        body: {}
      });
      deletePlan.value = undefined;
      toast.show('计划已删除');
      await load();
    } catch (e) {
      error.value = errorText(e);
    }
  }
  function askPause(r: Run) {
    pauseTarget.value = r;
    pauseError.value = '';
    detail.value = undefined;
  }
  async function confirmPause() {
    if (!pauseTarget.value || pauseBusy.value) return;
    const resume = pauseTarget.value.status === 'paused';
    pauseBusy.value = true;
    pauseError.value = '';
    try {
      await api<unknown>(`/runs/${pauseTarget.value.id}/${resume ? 'resume' : 'pause'}`, {
        method: 'POST',
        body: {
          confirm: true
        }
      });
      pauseTarget.value = undefined;
      toast.show(resume ? '批次已继续运行' : '批次已暂停调度，正在执行的步骤将完成后释放名额');
      await load();
    } catch (e) {
      pauseError.value = errorText(e);
    } finally {
      pauseBusy.value = false;
    }
  }
  function askCancel(r: Run) {
    cancelTarget.value = r;
    cancelError.value = '';
    detail.value = undefined;
  }
  async function confirmCancel() {
    if (!cancelTarget.value || cancelling.value) return;
    cancelling.value = true;
    try {
      await api<unknown>(`/runs/${cancelTarget.value.id}/cancel`, { method: 'POST', body: {} });
      cancelTarget.value = undefined;
      toast.show('已提交取消请求');
      await load();
    } catch (e) {
      cancelError.value = errorText(e);
    } finally {
      cancelling.value = false;
    }
  }
  async function inspect(r: Run) {
    detail.value = r;
    try {
      const run = await api<Run>(`/runs/${r.id}`);
      // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- the compared state can change while awaiting
      if (detail.value?.id === r.id) {
        detail.value = run;
      }
    } catch (e) {
      error.value = errorText(e);
    }
  }
  onMounted(() => {
    void poll();
  });
  onUnmounted(() => {
    alive = false;
    clearTimeout(timer);
  });
  const planKinds: Record<string, string> = { hourly: '按小时', daily: '按日', weekly: '按周' };
  const planKind = (kind: string) => planKinds[kind] ?? 'Cron';
  function tokenValue(r: Run) {
    if (!r.token_records) return '—';
    return (r.token_estimated_records ? '≈' : '') + formatCompact(r.token_count);
  }
  const emptyText = computed(() => {
    if (loading.value) return '正在加载批次记录…';
    return hasFilters.value
      ? '没有符合筛选条件的批次'
      : '暂无批次记录，请到计划管理创建手动计划或自动计划。';
  });
  const restoreLabel = computed(() => {
    if (restoring.value) return '正在还原…';
    return restoreResult.value ? '重试失败项' : '确认还原批次';
  });
  const pauseLabel = computed(() => {
    if (pauseBusy.value) return '正在提交…';
    return pauseTarget.value?.status === 'paused' ? '确认继续' : '确认暂停';
  });
</script>
<template>
  <section>
    <div class="page-heading"
      ><div
        ><h1>{{ view === 'plans' ? '计划管理' : '批次记录' }}<span class="title-dot">.</span></h1
        ><p>{{
          view === 'plans'
            ? '手动发起任务，或设置自动运行时间与处理范围。'
            : '查看任务进度、处理结果与批次详情。'
        }}</p></div
      ><div class="heading-actions"
        ><button :disabled="loading" @click="load()"
          ><RefreshCw :size="16" :class="{ spin: loading }" />刷新</button
        ></div
      ></div
    ><div v-if="error" role="alert" class="notice error"
      >{{ error
      }}<button
        @click="
          error = '';
          void load();
        "
        >重试</button
      ></div
    ><template v-if="view === 'plans'">
      <section class="run-section manual-plan-section" aria-label="手动计划"
        ><div class="plan-section-heading"
          ><div
            ><h2><Play :size="19" />手动计划</h2
            ><p>按需执行一次，选择命名或抽帧以及目标目录。</p></div
          ><button class="primary" @click="manual()"><Plus :size="17" />新建手动计划</button></div
        ><p class="help"
          >提交后立即加入队列，进度与结果在 <RouterLink to="/runs">批次记录</RouterLink> 中查看。</p
        ></section
      >
      <section class="run-section" aria-label="自动计划"
        ><div class="plan-section-heading"
          ><div
            ><h2><CalendarDays :size="19" />自动计划</h2
            ><p>按小时、日、周或 Cron 定时执行命名或抽帧任务。</p></div
          ><button class="primary" @click="plan()"><Plus :size="17" />新建自动计划</button></div
        ><div v-if="!plans.length" class="panel empty plans-empty"
          ><CalendarDays :size="31" /><h3>{{ loading ? '正在加载计划…' : '暂无自动计划' }}</h3
          ><p>新建自动计划，按设定时间自动运行。</p></div
        ><div v-else class="plan-grid"
          ><article v-for="p in plans" :key="p.id" class="panel plan-card" :aria-label="p.name"
            ><header
              ><span class="plan-icon"><Clock3 :size="19" /></span
              ><span class="badge">{{ p.task === 'extract' ? '抽帧' : '命名' }}</span
              ><button
                class="switch"
                :class="{ on: p.enabled }"
                role="switch"
                :aria-checked="p.enabled"
                :aria-label="`${p.enabled ? '暂停' : '启用'} ${p.name}`"
                @click="toggle(p)"
              ></button></header
            ><h3>{{ p.name }}</h3
            ><p class="plan-expression"
              >{{ planKind(p.kind) }} <code>{{ p.expression }}</code></p
            ><div class="plan-meta"
              ><span
                ><Folder :size="14" />{{ p.targets.length }} 个目录 ·
                {{ p.recursive ? '含子目录' : '仅当前层' }}</span
              ><span class="plan-paths" :title="p.target_names?.map(displayPath).join(' · ')">{{
                p.target_names?.map(displayPath).join(' · ')
              }}</span
              ><span>{{ p.timezone }}</span></div
            ><div class="plan-next"
              ><span>下次运行</span
              ><strong>{{ p.enabled ? formatTime(p.next) : '已暂停' }}</strong></div
            ><p v-if="p.last" class="help">上次：{{ errorText(p.last) }}</p
            ><footer
              ><button :disabled="p.task !== 'extract' && !ready" @click="runPlan(p)"
                ><Play :size="14" />立即运行</button
              ><button class="icon-btn" :aria-label="`编辑 ${p.name}`" @click="plan(p)"
                ><PenLine :size="15" /></button
              ><button
                class="icon-btn danger-button"
                :aria-label="`删除 ${p.name}`"
                @click="deletePlan = p"
                ><Trash2 :size="15" /></button></footer></article></div></section
    ></template>
    <section v-else class="run-section" aria-label="批次列表">
      <div class="batch-filters">
        <label
          >时间范围<SelectField
            v-model="period"
            label="时间范围"
            :options="[
              { value: 'all', label: '全部时间' },
              { value: 'today', label: '今天' },
              { value: '1', label: '最近 24 小时' },
              { value: '7', label: '最近 7 天' },
              { value: '30', label: '最近 30 天' },
              { value: 'custom', label: '自定义时间' }
            ]"
        /></label>
        <label
          >模型配置<SelectField
            v-model="profile"
            label="模型配置筛选"
            :options="[
              { value: '', label: '全部配置' },
              { value: '0', label: '无模型配置（抽帧）' },
              ...profiles.map((p) => ({ value: String(p.id), label: p.title || `配置 #${p.id}` }))
            ]"
        /></label>
        <label
          >批次状态<SelectField
            v-model="status"
            label="批次状态"
            :options="[{ value: '', label: '全部状态' }, ...statusOptions]"
        /></label>
        <label
          >批次耗时<SelectField
            v-model="duration"
            label="批次耗时"
            :options="[
              { value: '', label: '全部耗时' },
              { value: 'under1', label: '1 分钟内' },
              { value: '1to5', label: '1–5 分钟' },
              { value: '5to30', label: '5–30 分钟' },
              { value: '30to60', label: '30–60 分钟' },
              { value: 'over60', label: '1 小时及以上' }
            ]"
        /></label>
        <label
          >运行来源<SelectField
            v-model="category"
            label="运行来源"
            :options="[
              { value: 'all', label: '全部来源' },
              { value: 'manual', label: '手动运行' },
              { value: 'automatic', label: '计划触发' }
            ]" /></label
        ><label
          >任务类型<SelectField
            v-model="task"
            label="任务类型"
            :options="[
              { value: '', label: '全部任务' },
              { value: 'rename', label: '命名' },
              { value: 'extract', label: '抽帧' }
            ]"
        /></label>
        <template v-if="period === 'custom'"
          ><label
            >开始时间<input v-model="from" type="datetime-local" aria-label="开始时间" /></label
          ><label>结束时间<input v-model="to" type="datetime-local" aria-label="结束时间" /></label
        ></template>
        <button v-if="hasFilters" class="reset-batch-filters" @click="resetFilters"
          >重置筛选</button
        > </div
      ><p class="help batch-filter-help"
        >时间按实际开始时间（未开始时按提交时间）；耗时不含排队等待，含暂停时间，进行中批次按已运行时长筛选。</p
      >
      <div class="panel run-timeline"
        ><template v-for="day in runDays" :key="day.key"
          ><h4 class="run-day-label"
            ><CalendarDays :size="13" aria-hidden="true" />{{ day.label }}</h4
          >
          <article
            v-for="(r, runIndex) in day.runs"
            :key="r.id"
            class="run-entry"
            :aria-label="`批次 #${r.id}`"
            :data-task="r.task === 'extract' ? 'extract' : 'rename'"
            :class="{
              'is-active': active(r),
              'is-running': running(r),
              'is-full': !active(r) && progress(r) >= 100,
              'is-tail': runIndex === day.runs.length - 1
            }"
            ><time
              class="run-time"
              :datetime="r.started || r.created"
              :title="formatTime(r.started || r.created || '')"
              >{{ runClock(r) }}</time
            ><span class="run-node" aria-hidden="true"><i class="run-node-dot" /></span
            ><div class="run-body">
              <div class="run-head"
                ><strong class="run-id">#{{ r.id }}</strong
                ><span class="run-task"
                  ><component
                    :is="r.task === 'extract' ? Images : PenLine"
                    :size="14"
                    aria-hidden="true"
                  />{{ r.task === 'extract' ? '抽帧' : '重命名' }}</span
                ><StatusBadge :status="r.status"
                  ><template #icon
                    ><component
                      :is="runStatusIcon(r.status)"
                      :size="13"
                      aria-hidden="true" /></template
                  >{{ statusNames[r.status] || r.status }}</StatusBadge
                ><span
                  class="run-duration"
                  title="从实际开始到结束，不含排队等待，含暂停时间；进行中的批次显示已耗时与按近期处理速度估算的剩余时间"
                  ><span class="run-duration-value"
                    ><template v-if="etas[r.id]">已运行 </template>{{ elapsed(r)
                    }}<span v-if="etas[r.id]" class="run-eta-sep" aria-hidden="true">·</span
                    ><span v-if="etas[r.id]" class="run-eta">剩余约 {{ etas[r.id] }}</span></span
                  ></span
                ></div
              >
              <p class="run-meta"
                ><span>{{ sourceLabel(r) }}</span
                ><span class="run-meta-sep">·</span
                ><span class="table-model" :title="modelHint(r)">{{ modelName(r) }}</span
                ><span class="run-meta-sep">·</span
                ><span
                  class="run-tokens"
                  :title="r.token_records ? tokenHint(r) : '暂无模型请求记录'"
                  >{{ tokenValue(r) }}</span
                ></p
              >
              <div
                class="run-progress"
                role="progressbar"
                aria-valuemin="0"
                aria-valuemax="100"
                :aria-valuenow="progress(r)"
                :aria-label="`运行 #${r.id} 进度：已处理 ${processed(r)} / ${r.discovered}`"
                ><span class="run-progress-bar"
                  ><span class="run-progress-fill" :style="{ width: progress(r) + '%' }" /></span
                ><span class="run-progress-pct">{{ progress(r) }}%</span></div
              >
              <div class="run-foot"
                ><div class="run-stat-row"
                  ><span class="run-stat" data-stat="total"
                    >总数 <b>{{ r.discovered }}</b></span
                  ><span class="run-stat" data-stat="done" :data-zero="!r.done || undefined"
                    >成功 <b>{{ r.done }}</b></span
                  ><span class="run-stat" data-stat="skipped" :data-zero="!r.skipped || undefined"
                    >跳过 <b>{{ r.skipped }}</b></span
                  ><span class="run-stat" data-stat="failed" :data-zero="!r.failed || undefined"
                    >失败 <b>{{ r.failed }}</b></span
                  ></div
                ><div class="row-actions run-actions"
                  ><button
                    v-if="pausable(r) || r.status === 'paused'"
                    class="pause-run icon-only-action"
                    :class="{ 'resume-run': r.status === 'paused' }"
                    :aria-label="r.status === 'paused' ? '继续运行' : '暂停调度'"
                    :title="r.status === 'paused' ? '继续运行' : '暂停调度'"
                    @click="askPause(r)"
                    ><Play v-if="r.status === 'paused'" :size="14" /><Pause
                      v-else
                      :size="14" /></button
                  ><button
                    v-if="r.task !== 'extract'"
                    class="restore-run icon-only-action"
                    aria-label="还原批次"
                    :disabled="active(r) || !r.done"
                    :title="active(r) ? '请先取消并等待运行结束' : '还原本批次成功改名的文件'"
                    @click="askRestore(r)"
                    ><Undo2 :size="14" /></button
                  ><button
                    :disabled="!active(r) || r.status === 'cancelling'"
                    class="cancel-run icon-only-action"
                    aria-label="取消"
                    :title="r.status === 'cancelling' ? '取消中' : '取消运行'"
                    @click="askCancel(r)"
                    ><X :size="16" /></button
                  ><button
                    class="detail-button icon-only-action"
                    aria-label="详情"
                    title="详情"
                    @click="inspect(r)"
                    ><Info :size="16" /></button></div
              ></div> </div></article></template
        ><div v-if="!runs.length" class="empty compact">{{ emptyText }}</div></div
      >
      <div class="pagination" aria-label="批次记录分页"
        ><span class="subtle" role="status"
          >共 {{ currentPage.total }} 个批次 · 第 {{ currentPage.page }} / {{ pageCount }} 页 ·
          当前页 {{ runs.length }} 条</span
        ><SelectField
          v-model="currentPage.size"
          label="每页批次数"
          :options="[
            { value: 50, label: '50 条 / 页' },
            { value: 100, label: '100 条 / 页' },
            { value: 200, label: '200 条 / 页' }
          ]"
          @change="changeSize" /><PageNavigation
          :page="currentPage.page"
          :total-pages="pageCount"
          :disabled="loading"
          @change="changePage" /></div
    ></section>
    <ModalTransition
      ><PlanDialog
        v-if="planDialog"
        :mode="planDialog.mode"
        :plan="planDialog.plan"
        :ready="ready"
        @close="planDialog = undefined"
        @saved="planSaved"
    /></ModalTransition>
    <ModalTransition
      ><ModalDialog
        v-if="restoreTarget"
        :title="`还原批次 #${restoreTarget.id}`"
        @close="!restoring && (restoreTarget = undefined)"
        ><p>将本批次成功改名的文件恢复为本批次处理前的名称。</p
        ><p class="help"
          >已还原的文件会跳过；后续又改名、内容变化或原名被占用的文件不会强制覆盖。还原成功后可重新自动命名，已启用的定时计划也可能再次处理。</p
        ><div v-if="restoreError" class="notice error" role="alert">{{ restoreError }}</div
        ><div v-if="restoreResult?.failed" role="alert" class="notice error"
          >已还原 {{ restoreResult.restored }} 项 · 已还原跳过 {{ restoreResult.skipped }} 项 · 失败
          {{ restoreResult.failed }} 项<ul v-if="restoreResult.errors.length"
            ><li v-for="failure in restoreResult.errors" :key="failure.operation"
              >{{ failure.name }}（操作 #{{ failure.operation }}）：{{
                errorText(failure.error)
              }}</li
            ></ul
          ><p v-if="restoreResult.failed > 100">仅显示前 100 条失败详情。</p></div
        ><div class="modal-actions"
          ><button :disabled="restoring" @click="restoreTarget = undefined">{{
            restoreResult ? '关闭' : '暂不还原'
          }}</button
          ><button
            v-if="!restoreResult || restoreResult.failed"
            class="primary"
            :disabled="restoring"
            @click="restoreBatch"
            >{{ restoreLabel }}</button
          ></div
        ></ModalDialog
      ></ModalTransition
    >
    <ModalTransition
      ><ModalDialog
        v-if="pauseTarget"
        :title="pauseTarget.status === 'paused' ? '继续运行' : '暂停调度'"
        @close="!pauseBusy && (pauseTarget = undefined)"
        ><p
          >确定{{ pauseTarget.status === 'paused' ? '继续运行' : '暂停' }}批次 #{{
            pauseTarget.id
          }}？</p
        ><p class="help">{{
          pauseTarget.status === 'paused'
            ? '将从未完成的任务继续处理，与其他运行中的批次共享并发名额。'
            : '确认后停止调度新的抽帧与模型请求，正在执行的步骤会正常完成并释放名额，其他批次可继续运行。已完成的结果保留，之后可点击“继续运行”。'
        }}</p
        ><div v-if="pauseError" class="notice error" role="alert">{{ pauseError }}</div
        ><div class="modal-actions"
          ><button :disabled="pauseBusy" @click="pauseTarget = undefined">返回</button
          ><button class="primary" :disabled="pauseBusy" @click="confirmPause">{{
            pauseLabel
          }}</button></div
        ></ModalDialog
      ></ModalTransition
    >
    <ModalTransition
      ><ModalDialog
        v-if="cancelTarget"
        title="取消运行"
        @close="!cancelling && (cancelTarget = undefined)"
        ><p>确定取消运行 #{{ cancelTarget.id }}？</p
        ><p class="help">未完成的任务将停止，已完成的改名不会撤销。定时计划仍保持原设置。</p
        ><div v-if="cancelError" class="notice error" role="alert">{{ cancelError }}</div
        ><div class="modal-actions"
          ><button :disabled="cancelling" @click="cancelTarget = undefined">继续运行</button
          ><button class="cancel-run" :disabled="cancelling" @click="confirmCancel"
            ><X :size="16" />{{ cancelling ? '正在取消…' : '确认取消' }}</button
          ></div
        ></ModalDialog
      ></ModalTransition
    >
    <ModalTransition
      ><ModalDialog v-if="deletePlan" title="删除运行计划" @close="deletePlan = undefined"
        ><p>删除“{{ deletePlan.name }}”后，将不再自动触发。已有运行和历史记录会保留。</p
        ><div class="modal-actions"
          ><button @click="deletePlan = undefined">取消</button
          ><button class="danger-button" @click="remove"><Trash2 :size="16" />删除计划</button></div
        ></ModalDialog
      ></ModalTransition
    >
    <ModalTransition
      ><RunDetail
        v-if="detail"
        :key="detail.id"
        :run="detail"
        :elapsed="elapsed(detail)"
        @notice="toast.show($event)"
        @close="detail = undefined"
        @pause="askPause(detail)"
        @cancel="askCancel(detail)"
    /></ModalTransition>
  </section>
</template>
