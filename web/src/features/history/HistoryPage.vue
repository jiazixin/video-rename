<script setup lang="ts">
  import {
    useRouteState,
    queryText,
    queryChoice,
    queryInteger,
    queryIDs,
    queryBoolean
  } from '../../app/use-route-state';

  import './history.css';
  import { formatCompact } from '../../lib/format/number';
  import { errorText } from '../../app/errors';
  import PageNavigation from '../../components/PageNavigation.vue';
  import { useToast } from '../../app/use-toast';
  import { computed, ref, onMounted, onUnmounted, watch } from 'vue';
  import {
    CheckCircle2,
    AlertCircle,
    Search,
    Info,
    RefreshCw,
    ScrollText,
    Copy,
    Undo2,
    Trash2
  } from '@lucide/vue';
  import { useApi } from '../../app/use-api';
  import { newRequestKey } from '../../app/api';
  import { displayPath } from '../../lib/format/path';
  import {
    isActiveRunStatus,
    type LogPage,
    type LogRecord,
    type LogRetryResult,
    type Run
  } from '../../app/contracts';
  import { actionNames } from '../../app/labels';
  import { copyText } from '../../lib/clipboard/clipboard';
  import { vTabIndicator } from '../../lib/motion/tab-indicator';
  import DateTime from '../../components/DateTime.vue';
  import DateField from '../../components/DateField.vue';
  import ModalDialog from '../../components/ModalDialog.vue';
  import ModalTransition from '../../components/ModalTransition.vue';
  import SelectField from '../../components/SelectField.vue';
  import FailureDiagnostics from './FailureDiagnostics.vue';
  import { ModelPicker } from '../models';
  const { api, apiItems } = useApi();
  const toast = useToast();
  const category = ref('success'),
    rows = ref<LogRecord[]>([]),
    busy = ref(false),
    error = ref(''),
    q = ref(''),
    action = ref(''),
    source = ref(''),
    from = ref(''),
    to = ref(''),
    pageNumber = ref(1),
    selected = ref<LogRecord>(),
    detailError = ref(''),
    copying = ref(false),
    copyError = ref('');
  const limit = ref(50),
    total = ref(0),
    multi = ref(false),
    retrying = ref(false),
    retryRuns = ref<number[]>([]);
  const retryScope = ref<{ all: boolean; ids: number[]; name: string; category: string }>(),
    retryModel = ref(0),
    retryError = ref('');
  let retryKey = '';
  let selectionAnchor: number | undefined;
  const retryable = computed(() => rows.value.filter((r) => chosen.value.has(r.id) && canRetry(r)));
  const run = ref(''),
    batches = ref<number[]>([]);
  const batchOptions = computed(() => [
    { value: '', label: '全部批次' },
    ...batches.value.map((id) => ({ value: String(id), label: `批次 #${id}` }))
  ]);
  const chosen = ref(new Set<number>()),
    clearOpen = ref(false),
    clearAll = ref(false),
    clearing = ref(false),
    clearError = ref('');
  const selectedID = ref(0);
  const routeState = useRouteState(
    {
      category: queryChoice(category, ['success', 'failure']),
      q: queryText(q),
      action: queryChoice(action, [
        '',
        'automatic',
        'manual',
        'restore',
        'numbering',
        'numbering_restore'
      ]),
      model_source: queryChoice(source, ['', 'primary', 'fallback', 'unknown']),
      from: queryText(from, (value) => !Number.isNaN(Date.parse(value))),
      to: queryText(to, (value) => !Number.isNaN(Date.parse(value))),
      run: queryText(run, (value) => /^\d*$/.test(value)),
      page: queryInteger(pageNumber, { min: 1 }),
      limit: queryInteger(limit, { choices: [50, 100, 200] }),
      selected: queryIDs(chosen),
      multi: queryBoolean(multi),
      detail: queryInteger(selectedID)
    },
    {
      onRestore: () => {
        void load(true);
      }
    }
  );
  watch(selected, (value) => {
    selectedID.value = value?.id ?? 0;
  });
  function clearSelection() {
    chosen.value.clear();
    selectionAnchor = undefined;
  }
  function toggleRow(id: number) {
    if (chosen.value.has(id)) chosen.value.delete(id);
    else chosen.value.add(id);
  }
  function selectRow(id: number, event: MouseEvent) {
    const start = rows.value.findIndex((row) => row.id === selectionAnchor),
      end = rows.value.findIndex((row) => row.id === id);
    if (event.shiftKey && start >= 0 && end >= 0) {
      for (const row of rows.value.slice(Math.min(start, end), Math.max(start, end) + 1))
        chosen.value.add(row.id);
    } else {
      toggleRow(id);
      selectionAnchor = id;
    }
    (event.target as HTMLInputElement).checked = chosen.value.has(id);
  }
  function selectPage(invert = false) {
    selectionAnchor = undefined;
    for (const row of rows.value) {
      if (invert) toggleRow(row.id);
      else chosen.value.add(row.id);
    }
  }
  function togglePage() {
    selectionAnchor = undefined;
    if (rows.value.every((r) => chosen.value.has(r.id))) clearSelection();
    else selectPage();
  }
  function toggleMulti() {
    multi.value = !multi.value;
    clearSelection();
  }
  function openClear() {
    clearAll.value = !chosen.value.size;
    clearError.value = '';
    clearOpen.value = true;
  }
  async function clearHistory() {
    if (clearing.value) return;
    clearing.value = true;
    clearError.value = '';
    try {
      const result = await api<{ deleted: number }>('/logs/clear', {
        method: 'POST',
        body: {
          category: category.value,
          all: clearAll.value,
          ids: clearAll.value ? [] : [...chosen.value]
        }
      });
      toast.show(`已清除 ${result.deleted} 条记录`);
      clearOpen.value = false;
      reset();
    } catch (e) {
      clearError.value = errorText(e);
    } finally {
      clearing.value = false;
    }
  }
  let seq = 0,
    debounce: ReturnType<typeof setTimeout> | undefined;
  let loadController: AbortController | undefined;
  async function load(preserve = false) {
    loadController?.abort();
    if (category.value === 'extraction') return;
    const controller = new AbortController();
    loadController = controller;
    if (!preserve) clearSelection();
    const n = ++seq;
    busy.value = true;
    if (!preserve) error.value = '';
    try {
      const params = new URLSearchParams({
        limit: String(limit.value),
        category: category.value,
        q: q.value,
        action: action.value,
        model_source: source.value,
        offset: String((pageNumber.value - 1) * limit.value),
        from: from.value ? new Date(from.value + 'T00:00:00').toISOString() : '',
        to: to.value ? new Date(to.value + 'T23:59:59.999').toISOString() : ''
      });
      if (run.value) params.set('run', run.value);
      const [data, available] = await Promise.all([
        api<LogPage>(`/logs?${params}`, { signal: controller.signal }),
        apiItems<number>('/logs/batches', { signal: controller.signal })
      ]);
      if (n === seq) {
        error.value = '';
        rows.value = data.items;
        selected.value = rows.value.find((row) => row.id === selectedID.value);
        if (chosen.value.size) multi.value = true;
        total.value = data.total;
        pageNumber.value = Math.floor(data.offset / data.limit) + 1;
        chosen.value = new Set(
          [...chosen.value].filter((id) => rows.value.some((r) => r.id === id))
        );
        batches.value = available;
        if (run.value && !available.includes(Number(run.value)))
          batches.value = [...available, Number(run.value)].sort((a, b) => b - a);
      }
    } catch (e) {
      if (n === seq) error.value = errorText(e);
    } finally {
      if (n === seq) busy.value = false;
    }
  }
  function tab(value: string) {
    seq++;
    busy.value = false;
    error.value = '';
    toast.clear();
    category.value = value;
    reset();
  }
  function pathParts(path: string) {
    if (!path) return { directory: '—', name: '—' };
    if (path !== displayPath(path)) return { directory: '媒体库', name: displayPath(path) };
    const index = path.lastIndexOf('/');
    return {
      directory: index < 0 ? '媒体库' : path.slice(0, index) || '媒体库',
      name: path.slice(index + 1) || '媒体库'
    };
  }
  function retryProblemText(problem: LogRetryResult['errors'][number]) {
    if (problem.record) return `记录 #${problem.record}：${errorText(problem.error)}`;
    if (problem.files) return `${problem.files} 个文件未提交：${errorText(problem.error)}`;
    return errorText(problem.error);
  }
  function modelName(row: LogRecord) {
    return row.model_config_title || row.model || '—';
  }
  function modelHint(row: LogRecord) {
    return (
      [row.model_config_title, row.model, row.action === 'automatic' ? modelSourceHint(row) : '']
        .filter(Boolean)
        .join(' · ') || '未记录模型'
    );
  }
  function modelSource(row: LogRecord) {
    if (row.model_source === 'primary') return '主模型';
    if (row.model_source === 'fallback') return '兜底模型';
    return '未记录';
  }
  function modelSourceHint(row: LogRecord) {
    if (row.model_source !== 'primary' && row.model_source !== 'fallback')
      return '没有对应的模型请求记录，显示批次选定的配置，无法确认实际模型来源';
    return row.category === 'success'
      ? `本次标题由${modelSource(row)}生成`
      : `最后一次模型请求使用${modelSource(row)}`;
  }
  function reset() {
    if (routeState.isRestoring.value) return;
    pageNumber.value = 1;
    void load();
  }
  function search() {
    loadController?.abort();
    seq++;
    clearTimeout(debounce);
    debounce = setTimeout(reset, 250);
  }
  function changePage(value: number) {
    pageNumber.value = value;
    void load();
  }
  async function copy() {
    const row = selected.value;
    if (!row || copying.value) return;
    copying.value = true;
    copyError.value = '';
    try {
      await copyText(JSON.stringify(row, null, 2));
      if (selected.value === row) toast.show('已复制运行记录详情');
    } catch (e) {
      if (selected.value === row) copyError.value = errorText(e);
    } finally {
      copying.value = false;
    }
  }
  async function restore() {
    if (!selected.value) return;
    busy.value = true;
    try {
      await api<unknown>(`/operations/${selected.value.operation}/restore`, {
        method: 'POST',
        body: {}
      });
      toast.show('名称已还原');
      selected.value = undefined;
      reset();
    } catch (e) {
      detailError.value = errorText(e);
    } finally {
      busy.value = false;
    }
  }
  function canRetry(row: LogRecord) {
    return (
      row.action === 'automatic' &&
      row.entry > 0 &&
      (row.category === 'failure' || (row.category === 'success' && row.operation > 0))
    );
  }
  function openRetry(all = false, row?: LogRecord) {
    if (retrying.value) return;
    let ids: number[] = [];
    if (!all) ids = row ? [row.id] : retryable.value.map((r) => r.id);
    if (!all && !ids.length) return;
    retryScope.value = {
      all,
      ids,
      name: row ? pathParts(row.category === 'success' ? row.new_path : row.old_path).name : '',
      category: category.value
    };
    retryModel.value = 0;
    retryError.value = '';
    retryKey = newRequestKey();
    if (row) selected.value = undefined;
  }
  async function retry() {
    const scope = retryScope.value;
    if (retrying.value || !scope || retryModel.value <= 0) return;
    retrying.value = true;
    retryError.value = '';
    try {
      const result = await api<LogRetryResult>('/logs/retry', {
        method: 'POST',
        headers: { 'Idempotency-Key': retryKey },
        body: {
          category: scope.category,
          ...(scope.all ? { all: true } : { ids: scope.ids }),
          model_config_id: retryModel.value
        }
      });
      if (!result.accepted.length) {
        retryError.value =
          result.errors.map(retryProblemText).join('；') ||
          `没有可重试的自动命名${scope.category === 'success' ? '成功' : '失败'}记录。${result.skipped ? ` ${result.skipped} 条记录不支持自动重试，已保留。` : ''}`;
        return;
      }
      retryRuns.value.push(...result.runs);
      toast.show(
        `已提交 ${result.accepted.length} 条${scope.category === 'success' ? '成功' : '失败'}记录重试，${scope.category === 'success' ? '原记录保留，完成后新增命名记录' : '成功后会自动移除'}。${result.skipped ? ` ${result.skipped} 条记录不支持自动重试，已保留。` : ''}`
      );
      error.value = result.errors.map(retryProblemText).join('；');
      result.accepted.forEach((id) => chosen.value.delete(id));
      retryScope.value = undefined;
    } catch (e) {
      retryError.value = errorText(e);
    } finally {
      retrying.value = false;
    }
  }

  let polling = false;
  const timer = setInterval(() => void pollOnce(), 3000);
  async function pollOnce() {
    if (polling || busy.value) return;
    polling = true;
    try {
      const hadRetries = retryRuns.value.length > 0;
      if (retryRuns.value.length) {
        const results = await Promise.all(retryRuns.value.map((id) => api<Run>(`/runs/${id}`)));
        retryRuns.value = results.filter((r) => isActiveRunStatus(r.status)).map((r) => r.id);
      }
      if (category.value === 'failure' || hadRetries) {
        await load(true);
        if (!rows.value.length && pageNumber.value > 1) {
          pageNumber.value--;
          await load(true);
        }
      }
    } catch (e) {
      error.value = errorText(e);
    } finally {
      polling = false;
    }
  }
  onMounted(() => load(true));
  onUnmounted(() => {
    seq++;
    clearTimeout(debounce);
    clearInterval(timer);
  });
  const retryHint = computed(() => {
    if (category.value === 'success') return '仅重试选中的有效自动命名结果，保留已处理标记和原记录';
    return retryRuns.value.length
      ? '重试运行中，成功后自动移除失败记录'
      : '自动命名失败可重试，确认时可选择模型';
  });
  function runLabel(row: LogRecord) {
    if (row.run) return `#${row.run}`;
    return category.value === 'success' ? '手动操作' : '—';
  }
  function tokenHint(row: LogRecord) {
    if (!row.tokens) return '没有关联的模型请求';
    return row.token_estimated ? '按文字粗估的 token' : '来自模型 usage 的 token';
  }
  function tokenValue(row: LogRecord) {
    if (!row.tokens) return '—';
    return (row.token_estimated ? '≈' : '') + formatCompact(row.tokens);
  }
  const emptyHint = computed(() => {
    if (q.value || action.value || source.value || run.value || from.value || to.value)
      return '可以调整筛选条件后再试。';
    return category.value === 'success'
      ? '完成一次命名后，就能在这里查看。'
      : '后续运行中的问题会单独记录在这里。';
  });
</script>
<template>
  <section>
    <div class="page-heading"
      ><div
        ><h1>运行记录<span class="title-dot">.</span></h1
        ><p>查看文件命名、还原等操作的成功与失败结果。</p></div
      ><div class="history-heading-actions" role="group" aria-label="记录操作"
        ><button
          v-if="category === 'success'"
          :disabled="busy || retrying || clearing || !retryable.length"
          title="使用所选模型重新命名选中的自动命名成功记录"
          @click="openRetry()"
          ><RefreshCw :size="16" :class="{ spin: retrying }" />{{
            retrying ? '正在提交…' : '重试选中'
          }}</button
        ><button
          v-if="category === 'failure'"
          :disabled="retrying || clearing"
          title="重试所有失败记录中的自动命名失败，不受分页、筛选或勾选限制"
          @click="openRetry(true)"
          ><RefreshCw :size="16" :class="{ spin: retrying }" />{{
            retrying ? '正在提交…' : '重试'
          }}</button
        ><button @click="openClear"><Trash2 :size="16" />清除记录</button
        ><button :disabled="busy" @click="reset"
          ><RefreshCw :size="16" :class="{ spin: busy }" />刷新</button
        ></div
      ></div
    >
    <div v-tab-indicator class="log-tabs" role="group" aria-label="运行记录状态"
      ><button
        :class="{ active: category === 'success' }"
        :aria-pressed="category === 'success'"
        @click="tab('success')"
        ><CheckCircle2 :size="18" />成功记录</button
      ><button
        :class="{ active: category === 'failure' }"
        :aria-pressed="category === 'failure'"
        @click="tab('failure')"
        ><AlertCircle :size="18" />失败记录</button
      ></div
    >
    <div v-if="error" role="alert" class="notice error"
      >{{ error }}<button @click="load()">重试</button></div
    ><div class="panel"
      ><div class="filterbar log-filters"
        ><button :aria-pressed="multi" @click="toggleMulti">{{
          multi ? '退出多选' : '多选'
        }}</button
        ><div class="search-input"
          ><Search :size="16" /><input
            v-model="q"
            placeholder="搜索文件名或记录内容…"
            aria-label="搜索运行记录"
            @input="search" /></div
        ><SelectField
          v-model="action"
          label="操作类型"
          :options="[
            { value: '', label: '全部操作' },
            ...Object.entries(actionNames).map(([value, label]) => ({ value, label }))
          ]"
          @change="reset"
        /><SelectField
          v-model="source"
          label="模型来源"
          :options="[
            { value: '', label: '全部模型来源' },
            { value: 'primary', label: '主模型' },
            { value: 'fallback', label: '兜底模型' },
            { value: 'unknown', label: '来源未记录' }
          ]"
          @change="reset"
        /><SelectField
          v-model="run"
          label="批次"
          :options="batchOptions"
          @change="reset"
        /><DateField v-model="from" label="起始日期" :max="to" @change="reset" /><span
          class="subtle"
          >至</span
        ><DateField v-model="to" label="结束日期" :min="from" @change="reset" /><div
          class="filter-spacer"
        /><span class="subtle">共 {{ total }} 项 · {{ rows.length }} 项 / 当前页</span></div
      >
      <div v-if="multi" class="batch-toolbar" role="group" aria-label="批量操作"
        ><strong>已选 {{ chosen.size }} 项</strong
        ><button :disabled="busy" title="全选本页" @click="selectPage()">全选</button
        ><button :disabled="busy" title="反选本页" @click="selectPage(true)">反选</button
        ><button :disabled="!chosen.size" @click="clearSelection">清空</button
        ><button :disabled="busy || retrying || !retryable.length" @click="openRetry()"
          ><RefreshCw :size="15" />{{
            retrying ? '正在提交…' : `批量重试（${retryable.length}）`
          }}</button
        ><span class="subtle">{{ retryHint }}</span></div
      >
      <div class="table-scroll history-table-scroll"
        ><table class="history-table" :class="[category, { multi }]"
          ><thead
            ><tr
              ><th v-if="multi" class="history-select"
                ><input
                  type="checkbox"
                  aria-label="选择本页记录"
                  :checked="rows.length > 0 && rows.every((row) => chosen.has(row.id))"
                  :indeterminate="
                    rows.some((row) => chosen.has(row.id)) &&
                    !rows.every((row) => chosen.has(row.id))
                  "
                  @change="togglePage" /></th
              ><th class="history-time">时间</th><th class="history-action">操作</th
              ><th class="history-model">模型配置</th><th class="history-run">批次</th
              ><th class="history-tokens">token数</th><th class="history-directory">目录</th
              ><th class="history-old-name">旧文件名</th
              ><th v-if="category === 'success'" class="history-result">新文件名</th
              ><th v-else class="history-reason">失败原因</th><th></th></tr
          ></thead>
          <tbody
            ><template v-for="row in rows" :key="row.id"
              ><tr :class="{ 'is-selected': multi && chosen.has(row.id) }"
                ><td v-if="multi" class="history-select"
                  ><input
                    type="checkbox"
                    :aria-label="`选择记录 ${row.id}`"
                    :checked="chosen.has(row.id)"
                    title="按住 Shift 点击可连续选择当前页记录"
                    @click="selectRow(row.id, $event)" /></td
                ><td class="subtle date-cell"><DateTime :value="row.created" /></td
                ><td
                  ><span class="action-badge" :data-action="row.action">{{
                    actionNames[row.action] || row.action
                  }}</span></td
                ><td
                  ><div class="history-model-content"
                    ><span class="table-model" :title="modelHint(row)">{{ modelName(row) }}</span
                    ><span
                      v-if="row.action === 'automatic'"
                      class="history-model-source"
                      role="note"
                      :data-source="row.model_source"
                      :title="modelSourceHint(row)"
                      >{{ modelSource(row) }}</span
                    ></div
                  ></td
                ><td class="subtle history-run-value"
                  ><span :title="row.run ? `批次 #${row.run}` : '没有关联的批次'">{{
                    runLabel(row)
                  }}</span></td
                ><td class="subtle run-tokens history-token-value" :title="tokenHint(row)">{{
                  tokenValue(row)
                }}</td
                ><td
                  ><span
                    class="log-directory"
                    :title="pathParts(row.old_path || row.new_path).directory"
                    >{{ pathParts(row.old_path || row.new_path).directory }}</span
                  ></td
                ><td
                  ><span class="history-filename" :title="pathParts(row.old_path).name">{{
                    pathParts(row.old_path).name
                  }}</span></td
                ><td v-if="category === 'success'"
                  ><span class="history-filename" :title="pathParts(row.new_path).name">{{
                    pathParts(row.new_path).name
                  }}</span></td
                ><td v-else class="subtle"
                  ><span class="error-summary" :title="errorText(row.error)">{{
                    errorText(row.error)
                  }}</span></td
                ><td class="history-row-actions"
                  ><button
                    v-if="canRetry(row)"
                    :disabled="retrying"
                    class="detail-button icon-only-action"
                    :aria-label="`重试记录 ${row.id}`"
                    title="重试"
                    @click="openRetry(false, row)"
                    ><RefreshCw :size="16" /></button
                  ><button
                    class="detail-button icon-only-action"
                    aria-label="详情"
                    title="详情"
                    @click="
                      selected = row;
                      detailError = '';
                      copyError = '';
                    "
                    ><Info :size="16" /></button></td></tr
              ><tr
                ><td :colspan="multi ? 10 : 9"
                  ><FailureDiagnostics :id="row.id" /></td></tr></template></tbody
        ></table>
        <div v-if="busy && !rows.length" class="empty" role="status"
          ><ScrollText :size="35" /><h3>正在读取运行记录…</h3></div
        ><div v-else-if="!rows.length" class="empty"
          ><ScrollText :size="35" /><h3>{{
            category === 'success' ? '还没有成功记录' : '没有失败记录'
          }}</h3
          ><p>{{ emptyHint }}</p></div
        ></div
      >
      <div class="pagination"
        ><span class="subtle">按时间从新到旧</span
        ><SelectField
          v-model="limit"
          label="每页条目数"
          :options="[
            { value: 50, label: '50 项 / 页' },
            { value: 100, label: '100 项 / 页' },
            { value: 200, label: '200 项 / 页' }
          ]"
          @change="reset" /><PageNavigation
          :page="pageNumber"
          :total-pages="Math.ceil(total / limit)"
          :disabled="busy"
          previous-label="上一页运行记录"
          next-label="下一页运行记录"
          @change="changePage" /></div
    ></div>
    <ModalTransition
      ><ModalDialog v-if="selected" title="运行记录详情" @close="selected = undefined"
        ><dl class="detail-list"
          ><dt>时间</dt><dd><DateTime :value="selected.created" /></dd><dt>操作类型</dt
          ><dd>{{ actionNames[selected.action] || selected.action }}</dd
          ><dt>原路径</dt><dd>{{ displayPath(selected.old_path) || '—' }}</dd
          ><dt>新路径</dt><dd>{{ selected.new_path || '—' }}</dd
          ><dt>批次</dt><dd>{{ selected.run ? `#${selected.run}` : '—' }}</dd
          ><dt>模型配置</dt><dd :title="modelHint(selected)">{{ modelName(selected) }}</dd
          ><template v-if="selected.action === 'automatic'"
            ><dt>{{ selected.category === 'success' ? '模型来源' : '最后请求来源' }}</dt
            ><dd
              ><span
                class="history-model-source"
                role="note"
                :data-source="selected.model_source"
                :title="modelSourceHint(selected)"
                >{{ modelSource(selected) }}</span
              ></dd
            ><dt>模型 ID</dt><dd>{{ selected.model || '—' }}</dd></template
          ><dt>详情</dt><dd>{{ errorText(selected.error) || '操作已成功完成' }}</dd></dl
        ><FailureDiagnostics
          :id="selected.id"
          :key="selected.id"
          :processing="selected.category === 'failure'"
        /><div v-if="detailError" role="alert" class="notice error">{{ detailError }}</div
        ><div v-if="copyError" role="alert" class="notice error">{{ copyError }}</div
        ><p v-if="selected.category === 'success' && selected.operation" class="help"
          >仅最近一次尚未撤销的改名可还原；提交前将重新核验文件和名称占用。</p
        ><div class="modal-actions"
          ><button
            v-if="canRetry(selected)"
            :disabled="retrying"
            @click="openRetry(false, selected)"
            ><RefreshCw :size="15" />重试</button
          ><button :disabled="copying" @click="copy"><Copy :size="15" />复制详情</button
          ><button
            v-if="
              selected.category === 'success' && selected.operation && selected.action !== 'restore'
            "
            :disabled="busy"
            class="primary"
            @click="restore"
            ><Undo2 :size="15" />还原此次命名</button
          ></div
        ></ModalDialog
      ></ModalTransition
    >
    <ModalTransition
      ><ModalDialog
        v-if="retryScope"
        :title="retryScope.category === 'success' ? '重试成功记录' : '重试失败记录'"
        @close="!retrying && (retryScope = undefined)"
        ><p v-if="retryScope.all">重试全部自动命名失败记录，包括其他页，不受筛选条件限制。</p
        ><p v-else-if="retryScope.name">重试文件：{{ retryScope.name }}</p
        ><p v-else
          >重试选中的 {{ retryScope.ids.length }} 条自动命名{{
            retryScope.category === 'success' ? '成功' : '失败'
          }}记录。</p
        ><div :inert="retrying"><ModelPicker v-model="retryModel" /></div
        ><p class="help"
          >使用所选模型配置的最新设置创建一个批次。同一文件只提交一次。{{
            retryScope.category === 'success'
              ? '本次会重新生成并修改当前文件名；仅对选中的有效自动命名结果允许本次重试。已处理标记始终保留，失败或取消仍受保护；原成功记录保留，新结果另行记录。手动改名、显式标记或已被后续改名取代的记录不支持重试。'
              : '成功后自动移除旧失败记录。'
          }}</p
        ><div v-if="retryError" class="notice error" role="alert">{{ retryError }}</div
        ><div class="modal-actions"
          ><button :disabled="retrying" @click="retryScope = undefined">取消</button
          ><button class="primary" :disabled="retrying || retryModel <= 0" @click="retry"
            ><RefreshCw :size="16" :class="{ spin: retrying }" />{{
              retrying ? '正在提交…' : '确认重试'
            }}</button
          ></div
        ></ModalDialog
      ></ModalTransition
    >
    <ModalTransition
      ><ModalDialog v-if="clearOpen" title="清除运行记录" @close="!clearing && (clearOpen = false)"
        ><p>清除{{ category === 'success' ? '成功' : '失败' }}记录</p
        ><label class="check-label"
          ><input
            type="radio"
            name="clear-scope"
            :checked="!clearAll"
            :disabled="!chosen.size"
            @change="clearAll = false"
          />已选 {{ chosen.size }} 条</label
        ><label class="check-label"
          ><input
            type="radio"
            name="clear-scope"
            :checked="clearAll"
            @change="clearAll = true"
          />当前分类的全部记录（包括其他页，不受筛选条件限制）</label
        ><p class="help">清除后无法恢复。文件、还原数据和已处理标记不受影响。</p
        ><div v-if="clearError" class="notice error" role="alert">{{ clearError }}</div
        ><div class="modal-actions"
          ><button :disabled="clearing" @click="clearOpen = false">取消</button
          ><button
            class="cancel-run"
            :disabled="clearing || (!clearAll && !chosen.size)"
            @click="clearHistory"
            ><Trash2 :size="16" />{{ clearing ? '正在清除…' : '确认清除' }}</button
          ></div
        ></ModalDialog
      ></ModalTransition
    >
  </section>
</template>
<style scoped>
  .history-table.success {
    --history-actions-width: 108px;
  }
  .history-model-content {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--space-5);
    min-width: 0;
  }
  .history-model-source {
    display: inline-flex;
    align-items: center;
    border-radius: var(--radius-4);
    padding: var(--space-2) var(--space-6);
    font-size: var(--font-size-10);
    line-height: var(--line-height-1-5);
    white-space: nowrap;
    color: var(--palette-656d77);
    background: var(--palette-f0f1f4);
  }
  .history-model-source[data-source='primary'] {
    color: var(--palette-2468af);
    background: var(--palette-edf4fc);
  }
  .history-model-source[data-source='fallback'] {
    color: var(--tone-amber-ink);
    background: var(--palette-fff4df);
  }
</style>
