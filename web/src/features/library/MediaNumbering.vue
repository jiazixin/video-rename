<script setup lang="ts">
  import { errorText } from '../../app/errors';
  import PageNavigation from '../../components/PageNavigation.vue';
  import { computed, nextTick, onDeactivated, ref } from 'vue';
  import { useNumberingTask } from './numbering-task';
  import { Hash, History, Undo2 } from '@lucide/vue';
  import { useApi } from '../../app/use-api';
  import { ApiError } from '../../app/api';
  import type { ServerRecord } from '../../app/contracts';
  import DateTime from '../../components/DateTime.vue';
  import ModalDialog from '../../components/ModalDialog.vue';
  import ModalTransition from '../../components/ModalTransition.vue';
  import NumberingDiagnostics from './NumberingDiagnostics.vue';
  import './media-numbering.css';
  const { api, apiItems } = useApi();
  type Item = { entry: number; old_name: string; new_name: string };
  type Batch = {
    id: string;
    parent: number;
    status: string;
    created: string;
    undo_of: string;
    error?: ServerRecord;
    processing_log?: string;
    total: number;
    changed: number;
    items?: Item[];
  };
  const {
    directory,
    query,
    status = '',
    sort,
    disabled,
    media
  } = defineProps<{
    directory: number;
    query: string;
    status?: string;
    sort: string;
    disabled: boolean;
    media: 'image' | 'audio';
  }>();
  const noun = computed(() => (media === 'audio' ? '音频' : '图片'));
  const unit = computed(() => (media === 'audio' ? '个' : '张'));
  const untouched = computed(() => (media === 'audio' ? '视频、图片和目录不变' : '视频和目录不变'));
  const emit = defineEmits<{ changed: []; notice: [message: string] }>();
  const task = useNumberingTask();
  const progress = task.progress;
  const busy = ref(false),
    applying = ref(false),
    confirmingClose = ref(false),
    continueButton = ref<HTMLButtonElement>(),
    show = ref(false),
    history = ref(false),
    diagnostics = ref(''),
    error = ref(''),
    preview = ref<Batch>(),
    visible = ref<Item[]>([]),
    records = ref<Batch[]>([]),
    page = ref(0);
  function close() {
    show.value = false;
    confirmingClose.value = false;
    task.cancel();
  }
  // Only an applying job can be stopped; a preview request finishes first.
  async function requestClose() {
    if (confirmingClose.value) {
      confirmingClose.value = false;
      return;
    }
    if (!busy.value) {
      close();
      return;
    }
    if (!applying.value) return;
    confirmingClose.value = true;
    await nextTick();
    continueButton.value?.focus();
  }
  onDeactivated(close);
  const states: Record<string, string> = {
    committed: '已完成',
    restored: '已还原',
    rolled_back: '已恢复原名',
    review: '待核对',
    running: '执行中',
    rollback: '正在恢复'
  };
  let parent = 0;
  function report(e: unknown) {
    if (!show.value) return;
    diagnostics.value = e instanceof ApiError ? e.processingLog : '';
    error.value = errorText(e);
  }
  async function open(restore?: Batch) {
    parent = directory;
    show.value = true;
    history.value = false;
    error.value = '';
    diagnostics.value = '';
    preview.value = undefined;
    visible.value = [];
    page.value = 0;
    busy.value = true;
    try {
      const batch = restore
        ? await api<Batch>(`/numberings/${restore.id}/restore-preview`, {
            method: 'POST',
            feedback: 'inline',
            body: {}
          })
        : await api<Batch>(`/directories/${parent}/numberings/preview`, {
            method: 'POST',
            feedback: 'inline',
            body: {
              media: media,
              query: query,
              status: status || '',
              sort: sort
            }
          });
      // The response carries the first page; loadPage reads the others.
      preview.value = batch;
      visible.value = batch.items ?? [];
    } catch (e) {
      report(e);
    } finally {
      busy.value = false;
    }
  }
  async function loadPage(next: number) {
    if (!preview.value || busy.value) return;
    busy.value = true;
    error.value = '';
    diagnostics.value = '';
    try {
      const data = await api<{ items: Item[] }>(
        `/numberings/${preview.value.id}/items?offset=${next * 100}`
      );
      visible.value = data.items;
      page.value = next;
    } catch (e) {
      report(e);
    } finally {
      busy.value = false;
    }
  }
  async function loadHistory() {
    records.value = await apiItems<Batch>(`/directories/${parent}/numberings?media=${media}`);
  }
  async function openHistory() {
    parent = directory;
    show.value = true;
    history.value = true;
    preview.value = undefined;
    error.value = '';
    diagnostics.value = '';
    busy.value = true;
    try {
      await loadHistory();
    } catch (e) {
      report(e);
    } finally {
      busy.value = false;
    }
  }
  async function apply() {
    const batch = preview.value;
    if (!batch || busy.value) return;
    busy.value = true;
    applying.value = true;
    error.value = '';
    diagnostics.value = '';
    try {
      const state = await task.run({ parent, action: 'apply', batches: [batch.id] });
      emit(
        'notice',
        `${batch.undo_of ? '已还原' : '已编号'} ${state.files} ${unit.value}${noun.value}`
      );
    } catch (e) {
      report(e);
    } finally {
      applying.value = false;
      confirmingClose.value = false;
      preview.value = undefined;
      history.value = true;
      emit('changed');
      if (show.value) {
        try {
          await loadHistory();
        } catch (e) {
          report(e);
        }
      }
      busy.value = false;
    }
  }
  async function recover(batch: Batch) {
    busy.value = true;
    error.value = '';
    diagnostics.value = '';
    try {
      const updated = await api<Batch>(`/numberings/${batch.id}/recover`, {
        method: 'POST',
        feedback: 'inline',
        body: {}
      });
      if (updated.status === 'review') error.value = errorText(updated.error);
      else emit('notice', errorText(updated.error) || '已恢复原名');
      await loadHistory();
    } catch (e) {
      report(e);
    } finally {
      busy.value = false;
      emit('changed');
    }
  }
  const dialogTitle = computed(() => {
    if (confirmingClose.value) return '停止当前编号？';
    if (history.value) return `${noun.value}编号记录`;
    return preview.value?.undo_of ? `还原${noun.value}编号` : `${noun.value}编号预览`;
  });
</script>
<template>
  <button :disabled="disabled || busy" @click="open()"><Hash :size="16" />一键编号</button>
  <button :disabled="!directory || busy" @click="openHistory"
    ><History :size="16" />编号记录</button
  >
  <ModalTransition
    ><ModalDialog v-if="show" :title="dialogTitle" wide @close="requestClose">
      <div v-if="confirmingClose">
        <p>确定停止并关闭编号窗口吗？</p>
        <p class="help">尚未完成的改名会恢复原名，可在编号记录中查看结果。</p>
        <div class="modal-actions"
          ><button ref="continueButton" class="primary" @click="confirmingClose = false"
            >继续处理</button
          ><button @click="close">确定停止并关闭</button></div
        >
      </div>
      <template v-else>
        <p v-if="busy" role="status">{{ progress || '正在处理，请稍候…' }}</p>
        <div v-if="error" class="notice error" role="alert">{{ error }}</div>
        <NumberingDiagnostics v-if="diagnostics" :message="error" :diagnostics="diagnostics" />
        <template v-if="preview">
          <p
            >共 {{ preview.total }} {{ unit }}{{ noun }}，{{ preview.changed }} {{ unit }}需要{{
              preview.undo_of ? '还原' : '修改'
            }}，{{ preview.total - preview.changed }} {{ unit }}保持不变。</p
          >
          <p v-if="!preview.undo_of" class="help"
            >仅当前目录内符合搜索和处理状态筛选条件的{{
              noun
            }}，包含所有分页。按系统设置中保存的编号规则处理，实际号码与扩展名以本次预览为准。已标记的{{
              noun
            }}跳过编号并保留名称；筛选隐藏或已标记的{{ noun }}会保留其号码。{{ untouched }}。</p
          >
          <p v-else class="help"
            >整批恢复本次编号前的名称和扩展名大小写。文件有后续修改或名称被占用时会停止。</p
          >
          <div class="table-scroll numbering-preview"
            ><table
              ><thead
                ><tr
                  ><th>原名</th><th>{{ preview.undo_of ? '还原为' : '新名' }}</th></tr
                ></thead
              ><tbody
                ><tr v-for="item in visible" :key="item.entry"
                  ><td>{{ item.old_name }}</td
                  ><td
                    >{{ item.new_name
                    }}<small v-if="item.old_name === item.new_name">（不变）</small></td
                  ></tr
                ></tbody
              ></table
            ></div
          >
          <div class="pagination"
            ><PageNavigation
              :page="page + 1"
              :total-pages="Math.ceil(preview.total / 100)"
              :disabled="busy"
              previous-label="上页"
              next-label="下页"
              @change="loadPage($event - 1)"
          /></div>
          <div class="modal-actions"
            ><button v-if="applying" @click="requestClose">停止并关闭</button
            ><button v-else :disabled="busy" @click="show = false">取消</button
            ><button class="primary" :disabled="busy || !preview.changed" @click="apply">{{
              preview.undo_of ? '确认整批还原' : '确认编号'
            }}</button></div
          >
        </template>
        <template v-if="history">
          <p class="help"
            >当前目录最近 50 次操作。未完成的操作会在重启时尝试恢复，也可手动重试恢复。</p
          >
          <article v-for="batch in records" :key="batch.id" class="numbering-record"
            ><div
              ><strong
                >{{ batch.undo_of ? '整批还原' : `${noun}编号` }} ·
                {{ states[batch.status] || batch.status }}</strong
              ><p class="numbering-record-meta"
                ><DateTime :value="batch.created" /><span
                  >{{ batch.changed }} {{ unit }}修改 / {{ batch.total }} {{ unit }}{{ noun }}</span
                ></p
              ><p v-if="batch.error">{{ errorText(batch.error) }}</p
              ><NumberingDiagnostics
                v-if="batch.processing_log"
                :message="errorText(batch.error)"
                :diagnostics="batch.processing_log" /></div
            ><button
              v-if="batch.status === 'committed' && !batch.undo_of && batch.changed"
              :disabled="busy"
              @click="open(batch)"
              ><Undo2 :size="15" />还原本次编号</button
            ><button
              v-if="['review', 'running', 'rollback'].includes(batch.status)"
              :disabled="busy"
              @click="recover(batch)"
              >重试恢复原名</button
            ></article
          >
          <p v-if="!records.length && !busy" class="empty compact">暂无编号记录</p>
        </template>
        <div v-if="!preview" class="modal-actions"
          ><button :disabled="busy" @click="show = false">关闭</button></div
        >
      </template>
    </ModalDialog></ModalTransition
  >
</template>
