<script setup lang="ts">
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
  import Modal from '../../components/Modal.vue';
  import StatusBadge from '../../components/StatusBadge.vue';
  import PageNavigation from '../../components/PageNavigation.vue';
  import { clockPart, time } from '../../lib/date/format';
  import { statusNames } from '../../app/labels';
  import { type Run } from '../../app/contracts';
  import { copyText } from '../../lib/clipboard/clipboard';
  import { useRunRecords, type RunItem } from './use-run-records';
  import './run-detail.css';

  const props = defineProps<{
    run: Run;
    items: RunItem[];
    total: number;
    logTotal?: number;
    page: number;
    busy: boolean;
    elapsed: string;
  }>();
  const emit = defineEmits<{
    close: [];
    page: [page: number];
    pause: [];
    cancel: [];
    notice: [message: string];
  }>();
  const active = computed(() =>
    ['queued', 'scanning', 'running', 'paused', 'cancelling'].includes(props.run.status)
  );
  const sections = ref({ targets: true, logs: false, failures: false, config: false });
  const { state: logs, refresh: refreshLogs } = useRunRecords(
    () => props.run.id,
    () => sections.value.logs,
    () => active.value,
    'logs'
  );
  const { state: failures, refresh: refreshFailures } = useRunRecords(
    () => props.run.id,
    () => sections.value.failures,
    () => active.value,
    'failures'
  );
  const promptOpen = ref(false);
  const copyError = ref('');
  const processed = computed(
    () => (props.run.done || 0) + (props.run.skipped || 0) + (props.run.failed || 0)
  );
  const percent = (n: number) =>
    props.run.discovered > 0
      ? `${Math.min(100, (n / props.run.discovered) * 100).toFixed(1)}%`
      : '—';
  const progress = computed(() =>
    props.run.discovered > 0 ? Math.min(100, (processed.value / props.run.discovered) * 100) : 0
  );
  const stats = computed(() => [
    { label: '已完成', value: props.run.done || 0, tone: 'success' },
    { label: '已跳过', value: props.run.skipped || 0, tone: 'muted' },
    { label: '失败', value: props.run.failed || 0, tone: 'error' },
    { label: '总任务', value: props.run.discovered || 0, tone: 'primary' }
  ]);
  const tone = (status: string) => {
    if (['done', 'completed', 'success'].includes(status)) return 'success';
    if (['failed', 'partial_failure', 'interrupted'].includes(status)) return 'error';
    if (['queued', 'paused', 'cancelling'].includes(status)) return 'warning';
    if (['running', 'scanning'].includes(status)) return 'primary';
    return 'muted';
  };
  const clock = (stamp?: string) => (stamp ? clockPart(stamp) : '—');
  const lifecycle = computed(() => {
    const r = props.run;
    const ended = !active.value;
    const stateTone = tone(r.status);
    const steps = [
      {
        label: '提交',
        value: clock(r.created),
        tone: r.status === 'queued' ? 'warning' : r.created ? 'primary' : 'pending',
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
        label:
          r.status === 'paused'
            ? '已暂停'
            : r.status === 'scanning'
              ? '扫描与检查'
              : r.status === 'cancelling'
                ? '正在取消'
                : '处理中',
        value: r.started
          ? `${active.value ? '已运行' : '运行'} ${props.elapsed}`
          : r.status === 'scanning'
            ? '正在扫描目标目录'
            : '等待开始',
        tone: active.value && r.status !== 'queued' ? stateTone : r.started ? 'primary' : 'pending',
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
  const currentItems = computed(() => props.items.filter((item) => item.status === 'running'));
  const currentItem = computed(() => currentItems.value[0]);
  const targetNames = computed(() =>
    props.run.targets.map((target, i) => props.run.target_names?.[i] || `条目 #${target}`)
  );
  const retryLimit = computed(() =>
    props.run.request_config?.retry_enabled ? (props.run.request_config.retry_count ?? 0) : 0
  );
  const path = (item: RunItem) => item.current_path || item.original_path || `文件 #${item.entry}`;
  const filename = (item: RunItem) => path(item).split('/').filter(Boolean).at(-1) || path(item);
  const lastAttempt = (item: RunItem) => item.attempts?.at(-1);
  const attemptSummary = (item: RunItem) => {
    const last = lastAttempt(item);
    if (last?.status === 'success')
      return last.fallback
        ? '兜底成功'
        : (item.attempts?.length || 0) > 1
          ? '重试成功'
          : '主模型成功';
    if (item.status === 'running') return '重试中';
    if (item.status === 'cancelled') return '已取消';
    return '最终失败';
  };
  const itemStamp = (item: RunItem) =>
    item.updated_ms ? new Date(item.updated_ms).toISOString() : lastAttempt(item)?.started;
  const itemClock = (item: RunItem) => clock(itemStamp(item));
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
      copyError.value = (e as Error).message;
    }
  }
</script>
<template>
  <Modal :title="`运行 #${run.id}`" wide class="run-detail-dialog" @close="emit('close')">
    <template #header>
      <StatusBadge class="detail-header-status" :status="run.status">{{
        statusNames[run.status] || run.status
      }}</StatusBadge>
      <p class="detail-header-meta"
        ><span>提交于 {{ time(run.created) }}</span
        ><span><Clock3 :size="13" />{{ active ? '已耗时' : '总耗时' }} {{ elapsed }}</span></p
      >
    </template>
    <div class="run-detail">
      <ol class="detail-lifecycle" aria-label="批次生命周期">
        <li
          v-for="(step, i) in lifecycle"
          :key="i"
          :data-tone="step.tone"
          :data-reached="step.reached || undefined"
          :data-link-reached="step.linkReached || undefined"
          :aria-label="`${step.label} · ${step.current ? '当前阶段' : step.reached ? '已到达' : '未到达'}`"
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
          <strong>{{ stat.value.toLocaleString() }}</strong
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
            @click="promptOpen = !promptOpen"
            :title="`提示词快照 · 配置：${run.model_config_title || '未记录'}`"
            ><ChevronRight :size="13" :class="{ 'is-open': promptOpen }" /><span
              >提示词快照 · {{ run.model_config_title || '查看内容' }}</span
            ></button
          >
        </template>
      </div>
      <div v-if="promptOpen" ref="promptBody" id="run-prompt-snapshot" class="detail-prompt-body"
        ><p class="detail-caption">创建批次时保存的完整提示词</p><pre>{{ run.prompt_body }}</pre>
      </div>
      <div v-if="run.task === 'extract'" class="detail-metadata"
        ><span>独立抽帧 · 不使用模型</span></div
      >
      <div v-if="run.error" role="alert" class="detail-run-error"
        ><CircleAlert :size="16" /><span>{{ run.error }}</span></div
      >
      <div class="detail-sections">
        <section class="detail-section">
          <button
            class="detail-section-toggle"
            :aria-expanded="sections.targets"
            aria-controls="run-detail-targets"
            @click="sections.targets = !sections.targets"
          >
            <Folder :size="17" /><strong>目标目录与文件</strong
            ><span class="section-summary" :title="targetNames.join(' · ')">{{
              targetNames.join(' · ')
            }}</span
            ><ChevronRight :size="16" :class="{ 'is-open': sections.targets }" />
          </button>
          <div v-show="sections.targets" id="run-detail-targets" class="detail-section-content">
            <div class="detail-targets"
              ><span v-for="(target, i) in targetNames" :key="i" :title="target"
                ><Folder :size="15" /><span>{{ target }}</span></span
              ><small class="detail-recursive">{{
                run.recursive ? '包含子目录' : '仅所选目标的当前层'
              }}</small></div
            >
            <div v-if="active" class="detail-current-file">
              <div class="detail-current-path"
                ><span class="detail-caption">当前路径</span
                ><div v-if="currentItem" class="detail-path-line"
                  ><span :title="path(currentItem)">{{ path(currentItem) }}</span
                  ><button
                    class="icon-btn"
                    aria-label="复制当前路径"
                    @click="copyPath(path(currentItem))"
                    ><Copy :size="15" /></button></div
                ><p v-else class="detail-caption">{{
                  run.status === 'queued'
                    ? '等待开始'
                    : run.status === 'scanning'
                      ? '正在扫描目标目录'
                      : '当前页暂无正在处理的文件'
                }}</p></div
              >
              <div v-if="currentItem" class="detail-current-status"
                ><span class="detail-caption"
                  >{{ run.status === 'paused' ? '当前文件' : '正在处理' }}：</span
                ><strong :title="filename(currentItem)">{{ filename(currentItem) }}</strong
                ><small v-if="currentItems.length > 1"
                  >本页另有 {{ currentItems.length - 1 }} 个文件处理中</small
                ><StatusBadge :status="currentItem.status">{{
                  statusNames[currentItem.status]
                }}</StatusBadge></div
              >
            </div>
            <div class="detail-batch-progress"
              ><div
                ><span>批次处理进度 <small>含完成、跳过与失败</small></span
                ><strong
                  >{{ processed.toLocaleString() }} / {{ (run.discovered || 0).toLocaleString()
                  }}<span>{{ percent(processed) }}</span></strong
                ></div
              ><div
                class="detail-progress-track"
                role="progressbar"
                aria-label="批次处理进度"
                :aria-valuenow="progress"
                :aria-valuemin="0"
                :aria-valuemax="100"
                ><span :style="{ width: `${progress}%` }" :data-tone="tone(run.status)" /></div
            ></div>
            <div class="detail-file-heading"
              ><strong>文件清单</strong><span>本页 {{ items.length }} 项</span></div
            >
            <div class="detail-file-list">
              <div v-for="item in items" :key="item.id" class="run-item">
                <div class="run-file-paths"
                  ><strong :title="item.original_path || path(item)">{{
                    item.original_path || path(item)
                  }}</strong
                  ><small
                    v-if="item.current_path && item.current_path !== item.original_path"
                    :title="item.current_path"
                    >当前路径：{{ item.current_path }}</small
                  ></div
                >
                <StatusBadge :status="item.status">{{
                  statusNames[item.status] || item.status
                }}</StatusBadge>
                <button
                  class="icon-btn"
                  :aria-label="`复制路径 ${filename(item)}`"
                  @click="copyPath(path(item))"
                  ><Copy :size="14"
                /></button>
                <p v-if="item.message">{{ item.message }}</p>
              </div>
              <p v-if="!items.length" class="detail-empty">{{
                busy
                  ? '正在读取文件清单…'
                  : run.status === 'paused'
                    ? '批次已暂停，继续运行后会处理剩余任务。'
                    : active
                      ? '发现文件后会显示在这里。'
                      : '当前页没有文件任务。'
              }}</p>
            </div>
            <div class="pagination"
              ><span class="subtle">共 {{ total }} 项 · 每页 100 项</span
              ><PageNavigation
                :page="page"
                :total-pages="Math.ceil(total / 100)"
                :disabled="busy"
                previous-label="上页文件"
                next-label="下页文件"
                @change="emit('page', $event)"
            /></div>
          </div>
        </section>
        <section class="detail-section">
          <button
            class="detail-section-toggle"
            :aria-expanded="sections.logs"
            aria-controls="run-detail-logs"
            @click="sections.logs = !sections.logs"
            ><FileText :size="17" /><strong>运行日志</strong
            ><span class="section-summary">{{
              logTotal === undefined ? '正在统计…' : `共 ${logTotal} 条`
            }}</span
            ><ChevronRight :size="16" :class="{ 'is-open': sections.logs }"
          /></button>
          <div v-show="sections.logs" id="run-detail-logs" class="detail-section-content">
            <p class="detail-caption"
              >整个批次的处理记录，最新处理在前；运行期间展开时自动刷新，结束后停止。</p
            >
            <div v-if="logs.error" class="detail-record-error" role="alert"
              ><span>运行日志读取失败：{{ logs.error }}</span
              ><button :disabled="logs.busy" @click="refreshLogs()">重试</button></div
            >
            <div class="detail-log-list">
              <div
                v-for="item in logs.items"
                :key="item.id"
                class="detail-log-row"
                :data-tone="tone(item.status)"
              >
                <time
                  :datetime="itemStamp(item)"
                  :title="itemStamp(item) ? time(itemStamp(item)!) : '未记录处理时间'"
                  >{{ itemClock(item) }}</time
                ><span class="detail-log-dot" aria-hidden="true" />
                <div class="detail-log-content"
                  ><div class="detail-log-title"
                    ><strong>{{ statusNames[item.status] || item.status }}</strong
                    ><span :title="path(item)">{{ filename(item) }}</span></div
                  ><p v-if="item.message" class="detail-log-message">{{ item.message }}</p>
                  <details v-if="item.attempts?.length" class="attempt-history"
                    ><summary
                      >模型请求 {{ item.attempts.length }} 次 · {{ attemptSummary(item) }}</summary
                    >
                    <div
                      v-for="attempt in item.attempts"
                      :key="attempt.number"
                      class="detail-attempt"
                      :data-tone="tone(attempt.status)"
                      ><strong
                        >{{ attempt.number === 1 ? '首次请求' : `重试第 ${attempt.number - 1} 次`
                        }}{{ attempt.fallback ? ' · 兜底模型' : ' · 主模型' }}</strong
                      ><dl
                        ><dt>模型</dt><dd>{{ attempt.model_config_title }} · {{ attempt.model }}</dd
                        ><dt>耗时</dt><dd>{{ attempt.duration_ms }} ms</dd><dt>开始时间</dt
                        ><dd>{{ time(attempt.started) }}</dd
                        ><dt>请求状态</dt
                        ><dd>{{
                          attempt.status === 'success'
                            ? '成功'
                            : attempt.status === 'cancelled'
                              ? '已取消'
                              : '失败'
                        }}</dd
                        ><dt>错误信息</dt><dd>{{ attempt.message || '—' }}</dd></dl
                      ></div
                    >
                  </details>
                </div>
              </div>
              <p v-if="!logs.items.length" class="detail-empty">{{
                logs.busy
                  ? '正在读取运行日志…'
                  : logs.error
                    ? '暂时无法读取运行日志'
                    : '暂无处理记录'
              }}</p>
            </div>
            <div class="pagination" aria-label="运行日志分页"
              ><span class="subtle">共 {{ logs.total }} 条 · 每页 100 条</span
              ><button v-if="logs.page > 1" :disabled="logs.busy" @click="refreshLogs(1)"
                >查看最新日志</button
              ><PageNavigation
                :page="logs.page"
                :total-pages="Math.ceil(logs.total / 100)"
                :disabled="logs.busy"
                label="运行日志翻页"
                previous-label="上页日志"
                next-label="下页日志"
                @change="refreshLogs"
            /></div>
          </div>
        </section>
        <section class="detail-section">
          <button
            class="detail-section-toggle"
            :aria-expanded="sections.failures"
            aria-controls="run-detail-failures"
            @click="sections.failures = !sections.failures"
            ><CircleAlert :size="17" /><strong>失败记录</strong
            ><span class="section-summary"
              ><span class="detail-failure-count" :data-tone="run.failed > 0 ? 'error' : 'muted'">{{
                run.failed || 0
              }}</span>
              条</span
            ><ChevronRight :size="16" :class="{ 'is-open': sections.failures }"
          /></button>
          <div v-show="sections.failures" id="run-detail-failures" class="detail-section-content">
            <p class="detail-caption"
              >整个批次的失败记录，最新失败在前；运行期间展开时自动刷新，结束后停止。</p
            >
            <div v-if="failures.error" class="detail-record-error" role="alert"
              ><span>失败记录读取失败：{{ failures.error }}</span
              ><button :disabled="failures.busy" @click="refreshFailures()">重试</button></div
            >
            <div class="detail-failure-list">
              <div v-for="item in failures.items" :key="item.id" class="detail-failure"
                ><div class="detail-failure-heading"
                  ><strong :title="path(item)">{{ filename(item) }}</strong
                  ><StatusBadge status="failed">失败</StatusBadge></div
                ><p class="detail-failure-reason">{{
                  item.message || lastAttempt(item)?.message || '未记录失败原因'
                }}</p
                ><dl
                  ><dt>重试</dt
                  ><dd>{{ Math.max(0, (item.attempts?.length || 0) - 1) }} / {{ retryLimit }}</dd
                  ><dt>最终模型</dt><dd>{{ lastAttempt(item)?.model || '—' }}</dd
                  ><dt>最后请求</dt
                  ><dd>{{ lastAttempt(item) ? time(lastAttempt(item)!.started) : '—' }}</dd></dl
                ></div
              >
              <p v-if="!failures.items.length" class="detail-empty">{{
                failures.busy
                  ? '正在读取失败记录…'
                  : failures.error
                    ? '暂时无法读取失败记录'
                    : '暂无失败记录'
              }}</p>
            </div>
            <div class="pagination" aria-label="失败记录分页"
              ><span class="subtle">共 {{ failures.total }} 条 · 每页 100 条</span
              ><button
                v-if="failures.page > 1"
                :disabled="failures.busy"
                @click="refreshFailures(1)"
                >查看最新失败</button
              ><PageNavigation
                :page="failures.page"
                :total-pages="Math.ceil(failures.total / 100)"
                :disabled="failures.busy"
                label="失败记录翻页"
                previous-label="上页失败"
                next-label="下页失败"
                @change="refreshFailures"
            /></div>
          </div>
        </section>
        <section class="detail-section">
          <button
            class="detail-section-toggle"
            :aria-expanded="sections.config"
            aria-controls="run-detail-config"
            @click="sections.config = !sections.config"
            ><Settings2 :size="17" /><strong>配置信息</strong
            ><span class="section-summary">{{
              run.task === 'extract' ? '独立抽帧' : '自动命名'
            }}</span
            ><ChevronRight :size="16" :class="{ 'is-open': sections.config }"
          /></button>
          <div v-show="sections.config" id="run-detail-config" class="detail-section-content"
            ><dl class="detail-config">
              <div
                ><dt>任务类型</dt
                ><dd>{{ run.task === 'extract' ? '独立抽帧' : '自动命名' }}</dd></div
              ><div
                ><dt>触发方式</dt
                ><dd>{{
                  run.source === 'schedule' ? '定时触发' : run.plan ? '计划立即运行' : '手动触发'
                }}</dd></div
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
                ><dt>提交时间</dt><dd>{{ time(run.created) }}</dd></div
              ><div
                ><dt>开始时间</dt><dd>{{ run.started ? time(run.started) : '—' }}</dd></div
              ><div
                ><dt>结束时间</dt><dd>{{ run.finished ? time(run.finished) : '—' }}</dd></div
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
  </Modal>
</template>
