<script setup lang="ts">
  import PageNavigation from '../../components/PageNavigation.vue';
  import { computed, onUnmounted, ref, watch } from 'vue';
  import { RefreshCw, RotateCcw, ChevronRight } from '@lucide/vue';
  import { api } from '../../app/api';
  import { time } from '../../lib/date/format';
  import { statusNames } from '../../app/labels';

  function statusName(status: string): string {
    return statusNames[status] || status;
  }
  import { type AnalysisReport } from '../../app/contracts';
  import SelectField from '../../components/SelectField.vue';
  import DateField from '../../components/DateField.vue';
  import StatusBadge from '../../components/StatusBadge.vue';
  import DateTime from '../../components/DateTime.vue';
  import AnalysisLine from './AnalysisLine.vue';
  import AnalysisHistogram from './AnalysisHistogram.vue';
  import AnalysisScatter from './AnalysisScatter.vue';
  import './analysis.css';
  type Preset = '1h' | '3h' | '6h' | '12h' | '24h' | 'today' | '7d' | '30d' | 'all' | 'custom';
  const preset = ref<Preset>('24h'),
    fromDate = ref(''),
    fromTime = ref(''),
    toDate = ref(''),
    toTime = ref('');
  const profile = ref(-1),
    source = ref(''),
    sort = ref<'recent' | 'duration' | 'size'>('recent'),
    page = ref(1);
  const report = ref<AnalysisReport>(),
    error = ref(''),
    loading = ref(false);
  let seq = 0,
    controller: AbortController | undefined;
  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const presets: { value: Preset; label: string }[] = [
    { value: '1h', label: '最近 1 小时' },
    { value: '3h', label: '最近 3 小时' },
    { value: '6h', label: '最近 6 小时' },
    { value: '12h', label: '最近 12 小时' },
    { value: '24h', label: '最近 24 小时' },
    { value: 'today', label: '今天' },
    { value: '7d', label: '最近 7 天' },
    { value: '30d', label: '最近 30 天' },
    { value: 'all', label: '全部' },
    { value: 'custom', label: '自定义' }
  ];
  const intervalNames: Record<string, string> = {
    '15m': '15 分钟',
    'hour': '1 小时',
    'day': '1 天',
    'month': '1 个月',
    'year': '1 年'
  };
  // 处理结果三系列：成功 / 跳过 / 失败（配色已通过 CVD 与对比校验）。
  const OUTCOME_COLORS = { done: '#0e8a5c', skipped: '#2468af', failed: '#b14545' };
  // 耗时分布顺序色带：浅 → 深 = 短 → 长（已通过 ordinal 校验）。
  const DURATION_RAMP = [
    '#77c18c',
    '#65ad79',
    '#519866',
    '#3d8454',
    '#287141',
    '#0e5e30',
    '#004c1f',
    '#003a0d'
  ];
  const LINE_COLOR = '#0e8a5c';
  function duration(ms: number) {
    if (!ms) return '—';
    const s = ms / 1000;
    if (s < 60) return `${s < 10 ? Math.round(s * 10) / 10 : Math.round(s)}s`;
    const total = Math.floor(s),
      d = Math.floor(total / 86400),
      h = Math.floor((total % 86400) / 3600),
      m = Math.floor((total % 3600) / 60);
    if (d) return `${d}d${h}h`;
    if (h) return `${h}h${String(m).padStart(2, '0')}m`;
    return `${m}m${String(total % 60).padStart(2, '0')}s`;
  }
  function pct(n: number, d: number) {
    return d ? `${((n / d) * 100).toFixed(1)}%` : '—';
  }
  function rate(n: number) {
    return n >= 100 ? Math.round(n).toLocaleString() : `${Math.round(n * 10) / 10}`;
  }
  function rangeTimes(): { from?: string; to: string } | string {
    const now = new Date();
    if (preset.value === 'custom') {
      if (!fromDate.value || !toDate.value) return '请选择自定义区间的起止日期';
      const from = new Date(`${fromDate.value}T${fromTime.value || '00:00'}`),
        to = new Date(`${toDate.value}T${toTime.value || '23:59'}`);
      if (Number.isNaN(from.getTime()) || Number.isNaN(to.getTime())) return '自定义时间无效';
      if (from >= to) return '结束时间需晚于起始时间';
      return { from: from.toISOString(), to: to.toISOString() };
    }
    if (preset.value === 'all') return { to: now.toISOString() };
    if (preset.value === 'today') {
      const from = new Date(now);
      from.setHours(0, 0, 0, 0);
      return { from: from.toISOString(), to: now.toISOString() };
    }
    const hours: { [k in Preset]?: number } = { '1h': 1, '3h': 3, '6h': 6, '12h': 12, '24h': 24 };
    if (hours[preset.value])
      return {
        from: new Date(now.getTime() - (hours[preset.value] || 0) * 3600_000).toISOString(),
        to: now.toISOString()
      };
    const from = new Date(now);
    from.setDate(from.getDate() - (preset.value === '7d' ? 7 : 30));
    from.setHours(0, 0, 0, 0);
    return { from: from.toISOString(), to: now.toISOString() };
  }
  let frozenRange: { from?: string; to: string } | undefined;
  async function load() {
    const mine = ++seq;
    controller?.abort();
    loading.value = false;
    const range = frozenRange ?? rangeTimes();
    if (typeof range === 'string') {
      error.value = range;
      return;
    }
    frozenRange = range;
    const params = new URLSearchParams({ to: range.to, timezone });
    if (range.from) params.set('from', range.from);
    if (profile.value >= 0) params.set('profile', String(profile.value));
    if (source.value) params.set('source', source.value);
    params.set('sort', sort.value);
    params.set('page', String(page.value));
    controller = new AbortController();
    loading.value = true;
    try {
      const data = await api<AnalysisReport>(
        `/runs/analytics?${params.toString()}`,
        'GET',
        undefined,
        controller.signal
      );
      if (mine === seq) {
        report.value = data;
        if (page.value !== data.batches.page) page.value = data.batches.page;
        error.value = '';
      }
    } catch (e) {
      if (mine === seq && (e as Error).name !== 'AbortError') error.value = (e as Error).message;
    } finally {
      if (mine === seq) loading.value = false;
    }
  }
  const reload = ref(0);
  function refresh() {
    frozenRange = undefined;
    page.value = 1;
    reload.value++;
  }
  function reset() {
    preset.value = '24h';
    fromDate.value = fromTime.value = toDate.value = toTime.value = '';
    profile.value = -1;
    source.value = '';
    sort.value = 'recent';
    frozenRange = undefined;
    page.value = 1;
    reload.value++;
  }
  watch([preset, profile, source, sort, page, reload], (values, previous) => {
    if (values.some((value, i) => i !== 4 && value !== previous[i])) {
      frozenRange = undefined;
      if (page.value !== 1) {
        page.value = 1;
        return;
      }
    }
    void load();
  });
  onUnmounted(() => {
    seq++;
    controller?.abort();
  });
  load();
  const totals = computed(() => report.value?.totals);
  const terminal = computed(() => {
    const t = totals.value;
    return t ? t.completed + t.partial_failure + t.failed_batches + t.cancelled + t.interrupted : 0;
  });
  const attempted = computed(() => {
    const t = totals.value;
    return t ? t.done + t.failed : 0;
  });
  const outcomes = computed(() => {
    const t = totals.value;
    return t ? t.done + t.skipped + t.failed : 0;
  });
  const finishedBatches = computed(() =>
    report.value ? report.value.trend.reduce((sum, b) => sum + b.finished_batches, 0) : 0
  );
  const rangeText = computed(() =>
    report.value ? `${time(report.value.range.from)} 至 ${time(report.value.range.to)}` : ''
  );
  const profileOptions = computed(() => [
    { value: -1, label: '全部配置' },
    ...(report.value?.options.profiles.map((p) => ({
      value: p.id,
      label: p.id === 0 ? '无模型配置（抽帧）' : p.title || `配置 #${p.id}`
    })) || [])
  ]);
  const totalPages = computed(() =>
    report.value
      ? Math.max(1, Math.ceil(report.value.batches.total / report.value.batches.size))
      : 1
  );
  const durationBands = computed(() =>
    (report.value?.distribution || []).map((band, i) => ({
      ...band,
      color: DURATION_RAMP[i % DURATION_RAMP.length]
    }))
  );
  function bucketHours(bucket: { from: string; to: string }) {
    return Math.max((Date.parse(bucket.to) - Date.parse(bucket.from)) / 3600_000, 1 / 3600);
  }
  const batchDurationSeries = computed(() => [
    {
      name: '平均批次耗时',
      color: LINE_COLOR,
      values: (report.value?.trend || []).map((b) => b.avg_batch_ms)
    }
  ]);
  const outcomeSeries = computed(() => [
    {
      name: '成功',
      color: OUTCOME_COLORS.done,
      values: (report.value?.trend || []).map((b) => b.done)
    },
    {
      name: '跳过',
      color: OUTCOME_COLORS.skipped,
      values: (report.value?.trend || []).map((b) => b.skipped)
    },
    {
      name: '失败',
      color: OUTCOME_COLORS.failed,
      values: (report.value?.trend || []).map((b) => b.failed)
    }
  ]);
  const throughputSeries = computed(() => [
    {
      name: '吞吐',
      color: LINE_COLOR,
      values: (report.value?.trend || []).map(
        (b) => (b.done + b.skipped + b.failed) / bucketHours(b)
      )
    }
  ]);
  const totalFailures = computed(() =>
    report.value ? report.value.failures.reduce((sum, f) => sum + f.count, 0) : 0
  );
  const hasBatches = computed(() => !!totals.value && totals.value.batches > 0);
</script>
<template>
  <div>
    <div v-if="error" role="alert" class="notice error"
      >{{ error }}<button @click="load">重试</button></div
    >
    <div class="usage-filters">
      <div class="filterbar log-filters">
        <SelectField v-model="preset" label="时间范围" :options="presets" />
        <SelectField v-model="profile" label="模型配置" :options="profileOptions" />
        <SelectField
          v-model="source"
          label="运行来源"
          :options="[
            { value: '', label: '全部来源' },
            { value: 'manual', label: '手动运行' },
            { value: 'scheduled', label: '自动计划' }
          ]"
        />
        <template v-if="preset === 'custom'">
          <DateField v-model="fromDate" label="起始日期" />
          <input v-model="fromTime" type="time" aria-label="起始时刻" />
          <DateField v-model="toDate" label="结束日期" />
          <input v-model="toTime" type="time" aria-label="结束时刻" />
          <button class="primary" @click="refresh"><ChevronRight :size="14" />查询</button>
        </template>
        <span class="filter-spacer" />
        <button @click="refresh"><RefreshCw :size="14" :class="{ spin: loading }" />刷新</button>
        <button class="text-button" @click="reset"><RotateCcw :size="14" />重置</button>
      </div>
    </div>
    <template v-if="report">
      <div class="analysis-kpis">
        <div class="usage-kpi"
          ><span>批次成功率</span><strong>{{ pct(totals?.completed || 0, terminal) }}</strong
          ><small>{{ totals?.completed || 0 }} / {{ terminal }} 个终态批次</small></div
        >
        <div class="usage-kpi"
          ><span>项目成功率</span><strong>{{ pct(totals?.done || 0, attempted) }}</strong
          ><small>{{ totals?.done || 0 }} / {{ attempted }} 个已处理项</small></div
        >
        <div class="usage-kpi"
          ><span>平均批次耗时</span><strong>{{ duration(totals?.avg_batch_ms || 0) }}</strong
          ><small>{{ finishedBatches }} 个已结束批次</small></div
        >
        <div class="usage-kpi"
          ><span>平均处理耗时</span><strong>{{ duration(totals?.avg_item_ms || 0) }}</strong
          ><small
            >{{ totals?.timed_items || 0 }} 个计时样本<template
              v-if="totals && !totals.timed_items && totals.done + totals.failed > 0"
            >
              · 升级前的批次无逐项耗时</template
            ></small
          ></div
        >
        <div class="usage-kpi"
          ><span>吞吐</span><strong>{{ rate(totals?.throughput_per_hour || 0) }}</strong
          ><small>个 / 小时 · 区间内 {{ outcomes }} 项</small></div
        >
        <div class="usage-kpi"
          ><span>失败率</span><strong>{{ pct(totals?.failed || 0, attempted) }}</strong
          ><small>{{ totals?.failed || 0 }} 项失败</small></div
        >
        <div class="usage-kpi"
          ><span>跳过率</span><strong>{{ pct(totals?.skipped || 0, outcomes) }}</strong
          ><small>{{ totals?.skipped || 0 }} 项跳过</small></div
        >
        <div class="usage-kpi"
          ><span>P50 处理耗时</span><strong>{{ duration(totals?.p50_item_ms || 0) }}</strong
          ><small>逐项耗时中位数</small></div
        >
        <div class="usage-kpi"
          ><span>P95 处理耗时</span><strong>{{ duration(totals?.p95_item_ms || 0) }}</strong
          ><small>逐项耗时 95 分位</small></div
        >
        <div class="usage-kpi"
          ><span>取消批次</span><strong>{{ totals?.cancelled || 0 }}</strong
          ><small
            ><template v-if="totals?.interrupted">另有中断 {{ totals.interrupted }} 个</template
            ><template v-else>区间内已取消批次</template></small
          ></div
        >
      </div>
      <p class="help usage-notes">
        {{ rangeText }} · 按{{
          intervalNames[report.range.interval] || report.range.interval
        }}分桶（{{ report.range.timezone }}）。
        批次耗时按实际开始至结束计算；逐项耗时自本版本起记录。
      </p>
      <div class="usage-stack">
        <div class="panel">
          <div class="panel-toolbar"
            ><h2>批次耗时趋势</h2
            ><span class="subtle">{{ finishedBatches ? '' : '暂无数据' }}</span></div
          >
          <div class="usage-trend-wrap" :class="{ refreshing: loading }"
            ><AnalysisLine
              :series="batchDurationSeries"
              :buckets="report.trend"
              :interval="report.range.interval"
              :format="duration"
              label="批次耗时趋势"
              empty-text="区间内没有已结束的批次"
          /></div>
          <details class="usage-table-toggle">
            <summary>数据表</summary>
            <div class="table-scroll"
              ><table class="usage-trend-table">
                <thead
                  ><tr><th>时间段</th><th>结束批次</th><th>平均耗时</th></tr></thead
                >
                <tbody
                  ><tr v-for="b in report.trend" :key="b.from"
                    ><td class="date-cell"
                      ><span class="date-time-range"
                        ><DateTime :value="b.from" /><span>至</span
                        ><DateTime :value="b.to" /></span></td
                    ><td>{{ b.finished_batches || '—' }}</td
                    ><td>{{ b.avg_batch_ms ? duration(b.avg_batch_ms) : '—' }}</td></tr
                  ></tbody
                >
              </table></div
            >
          </details>
        </div>
        <div class="panel">
          <div class="panel-toolbar"
            ><h2>处理结果趋势</h2><span class="subtle">{{ outcomes ? '' : '暂无数据' }}</span></div
          >
          <div class="usage-trend-wrap" :class="{ refreshing: loading }"
            ><AnalysisLine
              :series="outcomeSeries"
              :buckets="report.trend"
              :interval="report.range.interval"
              :format="(v: number) => `${v.toLocaleString()} 项`"
              label="处理结果趋势"
              empty-text="区间内没有处理记录"
          /></div>
          <details class="usage-table-toggle">
            <summary>数据表</summary>
            <div class="table-scroll"
              ><table class="usage-trend-table">
                <thead
                  ><tr><th>时间段</th><th>成功</th><th>跳过</th><th>失败</th></tr></thead
                >
                <tbody
                  ><tr v-for="b in report.trend" :key="b.from"
                    ><td class="date-cell"
                      ><span class="date-time-range"
                        ><DateTime :value="b.from" /><span>至</span
                        ><DateTime :value="b.to" /></span></td
                    ><td>{{ b.done.toLocaleString() }}</td
                    ><td>{{ b.skipped.toLocaleString() }}</td
                    ><td>{{ b.failed.toLocaleString() }}</td></tr
                  ></tbody
                >
              </table></div
            >
          </details>
        </div>
        <div class="analysis-duo">
          <div class="panel">
            <div class="panel-toolbar"
              ><h2>吞吐趋势</h2><span class="subtle">{{ outcomes ? '' : '暂无数据' }}</span></div
            >
            <div class="usage-trend-wrap" :class="{ refreshing: loading }"
              ><AnalysisLine
                :series="throughputSeries"
                :buckets="report.trend"
                :interval="report.range.interval"
                :format="(v: number) => `${rate(v)} 个/小时`"
                label="吞吐趋势"
                empty-text="区间内没有处理记录"
            /></div>
            <details class="usage-table-toggle">
              <summary>数据表</summary>
              <div class="table-scroll"
                ><table class="usage-trend-table">
                  <thead
                    ><tr><th>时间段</th><th>处理项</th><th>吞吐</th></tr></thead
                  >
                  <tbody
                    ><tr v-for="b in report.trend" :key="b.from"
                      ><td class="date-cell"
                        ><span class="date-time-range"
                          ><DateTime :value="b.from" /><span>至</span
                          ><DateTime :value="b.to" /></span></td
                      ><td>{{ (b.done + b.skipped + b.failed).toLocaleString() }}</td
                      ><td
                        >{{ rate((b.done + b.skipped + b.failed) / bucketHours(b)) }} 个/小时</td
                      ></tr
                    ></tbody
                  >
                </table></div
              >
            </details>
          </div>
          <div class="panel">
            <div class="panel-toolbar"
              ><h2>耗时分布</h2
              ><span class="subtle">{{ totals?.timed_items ? '' : '暂无数据' }}</span></div
            >
            <div class="usage-trend-wrap" :class="{ refreshing: loading }"
              ><AnalysisHistogram :bands="durationBands"
            /></div>
          </div>
        </div>
        <div class="panel">
          <div class="panel-toolbar"
            ><h2>模型配置性能比较</h2><span class="subtle">仅命名批次 · 按批次数量排序</span></div
          >
          <div v-if="report.profiles.length" class="table-scroll"
            ><table>
              <thead
                ><tr
                  ><th>配置</th><th>模型</th><th>批次</th><th>项目成功率</th><th>平均处理耗时</th
                  ><th>P50</th><th>P95</th><th>吞吐</th></tr
                ></thead
              >
              <tbody
                ><tr v-for="p in report.profiles" :key="p.id">
                  <td
                    ><strong :title="[p.provider, p.protocol].filter(Boolean).join(' · ')">{{
                      p.title || `配置 #${p.id}`
                    }}</strong></td
                  >
                  <td
                    ><span
                      class="table-model"
                      :title="[p.provider, p.protocol, p.model].filter(Boolean).join(' · ')"
                      >{{ p.model || '未记录模型' }}</span
                    ></td
                  >
                  <td>{{ p.batches.toLocaleString() }}</td>
                  <td
                    >{{ pct(p.done, p.done + p.failed)
                    }}<small class="subtle"> · {{ p.done + p.skipped + p.failed }} 项</small></td
                  >
                  <td>{{ duration(p.avg_item_ms) }}</td>
                  <td>{{ duration(p.p50_item_ms) }}</td>
                  <td>{{ duration(p.p95_item_ms) }}</td>
                  <td>{{ rate(p.throughput_per_hour) }} 个/小时</td>
                </tr></tbody
              >
            </table></div
          >
          <div v-else class="empty compact">区间内没有可比较的命名批次</div>
        </div>
        <div class="panel">
          <div class="panel-toolbar"
            ><h2>批次大小 vs 耗时</h2
            ><span class="subtle">最多展示最近 {{ report.scatter.length }} 个批次</span></div
          >
          <div class="usage-trend-wrap" :class="{ refreshing: loading }"
            ><AnalysisScatter
              :points="report.scatter"
              :format-ms="duration"
              :status-name="statusName"
              :color="LINE_COLOR"
          /></div>
        </div>
        <div class="panel">
          <div class="panel-toolbar"
            ><h2>失败分析</h2
            ><span class="subtle"
              >{{ totals?.failed || 0 }} 项失败 ·
              {{ report.batch_failures.length }} 类批次错误</span
            ></div
          >
          <div
            v-if="report.failures.length || report.batch_failures.length"
            class="analysis-failures"
          >
            <div v-for="f in report.failures" :key="f.message" class="analysis-failure">
              <div class="usage-model-head"
                ><strong :title="f.message">{{ f.message }}</strong
                ><span
                  >{{ f.count.toLocaleString() }} 次 · {{ pct(f.count, totalFailures) }}</span
                ></div
              >
              <div class="usage-bar-track"
                ><div
                  class="usage-bar"
                  :style="{
                    width: `${totalFailures ? Math.max(1.5, (f.count / totalFailures) * 100) : 0}%`
                  }"
              /></div>
              <small class="subtle">最近发生 {{ time(f.last_at) }}</small>
            </div>
            <template v-if="report.batch_failures.length">
              <h3 class="analysis-subhead">批次级失败原因</h3>
              <div v-for="f in report.batch_failures" :key="f.message" class="analysis-failure">
                <div class="usage-model-head"
                  ><strong :title="f.message">{{ f.message }}</strong
                  ><span
                    >{{ f.count.toLocaleString() }} 个批次 · 最近 {{ time(f.last_at) }}</span
                  ></div
                >
              </div>
            </template>
          </div>
          <div v-else class="empty compact">区间内没有失败记录</div>
        </div>
        <div class="panel">
          <div class="panel-toolbar usage-detail-toolbar">
            <h2>批次明细</h2>
            <div class="usage-detail-tools">
              <button
                class="table-sort"
                :class="{ active: sort !== 'recent' }"
                @click="
                  sort = sort === 'recent' ? 'duration' : sort === 'duration' ? 'size' : 'recent'
                "
                >{{
                  sort === 'recent'
                    ? '按时间排序'
                    : sort === 'duration'
                      ? '按耗时排序'
                      : '按规模排序'
                }}</button
              >
              <span class="subtle"
                >共 {{ report.batches.total }} 个批次 · 第 {{ report.batches.page }} /
                {{ totalPages }} 页</span
              >
              <PageNavigation
                :page="page"
                :total-pages="totalPages"
                :disabled="loading"
                @change="page = $event"
              />
            </div>
          </div>
          <div v-if="report.batches.items.length" class="table-scroll"
            ><table>
              <thead
                ><tr
                  ><th>批次</th><th>状态</th><th>来源</th><th>模型</th><th>目标</th><th>成功</th
                  ><th>跳过</th><th>失败</th><th>开始时间</th><th>耗时</th><th>平均单项</th></tr
                ></thead
              >
              <tbody
                ><tr v-for="b in report.batches.items" :key="b.id">
                  <td
                    ><strong>#{{ b.id }}</strong
                    ><small class="run-source">{{
                      b.task === 'extract' ? '抽帧' : b.profile_title || `配置 #${b.profile_id}`
                    }}</small></td
                  >
                  <td
                    ><StatusBadge :status="b.status">{{
                      statusNames[b.status] || b.status
                    }}</StatusBadge></td
                  >
                  <td class="subtle">{{ b.source === 'manual' ? '手动' : '自动计划' }}</td>
                  <td
                    ><span
                      class="table-model"
                      :title="
                        b.task === 'extract'
                          ? '独立抽帧不调用模型'
                          : [b.provider, b.protocol, b.model].filter(Boolean).join(' · ')
                      "
                      >{{ b.task === 'extract' ? '不使用模型' : b.model || '未记录模型' }}</span
                    ></td
                  >
                  <td>{{ b.discovered.toLocaleString() }}</td>
                  <td>{{ b.done.toLocaleString() }}</td>
                  <td class="subtle">{{ b.skipped.toLocaleString() }}</td>
                  <td :class="{ 'error-text': b.failed > 0 }">{{ b.failed.toLocaleString() }}</td>
                  <td class="subtle date-cell"><DateTime :value="b.started || b.created" /></td>
                  <td>{{ duration(b.duration_ms) }}</td>
                  <td class="subtle">{{ duration(b.avg_item_ms) }}</td>
                </tr></tbody
              >
            </table></div
          >
          <div v-else class="empty compact">{{
            hasBatches ? '当前页没有批次' : '区间内没有批次记录'
          }}</div>
        </div>
      </div>
    </template>
    <div v-else-if="loading" class="panel empty"
      ><RefreshCw :size="22" class="spin" /><h3>正在统计批次性能…</h3></div
    >
  </div>
</template>
