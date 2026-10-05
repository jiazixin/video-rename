<script setup lang="ts">
  import PageNavigation from '../../components/PageNavigation.vue';
  import { computed, onDeactivated, ref } from 'vue';
  import { Hash, History, Undo2 } from '@lucide/vue';
  import { api, ApiError } from '../../app/api';
  import DateTime from '../../components/DateTime.vue';
  import Modal from '../../components/Modal.vue';
  import NumberingDiagnostics from './NumberingDiagnostics.vue';
  import './media-numbering.css';
  type Batch = {
    id: string;
    parent: number;
    status: string;
    created: string;
    undo_of: string;
    error: string;
    processing_log?: string;
    total: number;
    changed: number;
    items?: { entry: number; old_name: string; new_name: string }[];
  };
  const props = defineProps<{
    directory: number;
    query: string;
    status?: string;
    sort: string;
    disabled: boolean;
    media: 'image' | 'audio';
  }>();
  const noun = computed(() => (props.media === 'audio' ? '音频' : '图片'));
  const unit = computed(() => (props.media === 'audio' ? '个' : '张'));
  const untouched = computed(() =>
    props.media === 'audio' ? '视频、图片和目录不变' : '视频和目录不变'
  );
  const emit = defineEmits<{ changed: []; notice: [message: string] }>();
  const busy = ref(false),
    show = ref(false),
    history = ref(false),
    diagnostics = ref(''),
    error = ref(''),
    preview = ref<Batch>(),
    records = ref<Batch[]>([]),
    page = ref(0);
  onDeactivated(() => {
    show.value = false;
  });
  const visible = computed(
    () => preview.value?.items?.slice(page.value * 100, (page.value + 1) * 100) || []
  );
  const states: Record<string, string> = {
    committed: '已完成',
    restored: '已还原',
    rolled_back: '已恢复原名',
    review: '待核对',
    running: '执行中',
    rollback: '正在恢复'
  };
  let parent = 0;
  async function open(restore?: Batch) {
    parent = props.directory;
    show.value = true;
    history.value = false;
    error.value = '';
    diagnostics.value = '';
    preview.value = undefined;
    page.value = 0;
    busy.value = true;
    try {
      preview.value = restore
        ? await api<Batch>(`/numberings/${restore.id}/restore-preview`, 'POST', {})
        : await api<Batch>(`/directories/${parent}/numberings/preview`, 'POST', {
            media: props.media,
            query: props.query,
            status: props.status || '',
            sort: props.sort
          });
    } catch (e) {
      diagnostics.value = e instanceof ApiError ? e.processingLog : '';
      error.value = (e as Error).message;
    } finally {
      busy.value = false;
    }
  }
  async function loadHistory() {
    records.value = await api<Batch[]>(`/directories/${parent}/numberings?media=${props.media}`);
  }
  async function openHistory() {
    parent = props.directory;
    show.value = true;
    history.value = true;
    preview.value = undefined;
    error.value = '';
    diagnostics.value = '';
    busy.value = true;
    try {
      await loadHistory();
    } catch (e) {
      diagnostics.value = e instanceof ApiError ? e.processingLog : '';
      error.value = (e as Error).message;
    } finally {
      busy.value = false;
    }
  }
  async function apply() {
    if (!preview.value || busy.value) return;
    busy.value = true;
    error.value = '';
    diagnostics.value = '';
    try {
      const batch = await api<Batch>(`/numberings/${preview.value.id}/apply`, 'POST', {});
      emit(
        'notice',
        `${batch.undo_of ? '已还原' : '已编号'} ${batch.changed} ${unit.value}${noun.value}`
      );
      preview.value = undefined;
      history.value = true;
      await loadHistory();
    } catch (e) {
      diagnostics.value = e instanceof ApiError ? e.processingLog : '';
      error.value = (e as Error).message;
    } finally {
      busy.value = false;
      emit('changed');
    }
  }
  async function recover(batch: Batch) {
    busy.value = true;
    error.value = '';
    diagnostics.value = '';
    try {
      const updated = await api<Batch>(`/numberings/${batch.id}/recover`, 'POST', {});
      if (updated.status === 'review') error.value = updated.error;
      else emit('notice', updated.error || '已恢复原名');
      await loadHistory();
    } catch (e) {
      diagnostics.value = e instanceof ApiError ? e.processingLog : '';
      error.value = (e as Error).message;
    } finally {
      busy.value = false;
      emit('changed');
    }
  }
</script>
<template>
  <button :disabled="disabled || busy" @click="open()"><Hash :size="16" />一键编号</button>
  <button :disabled="!directory || busy" @click="openHistory"
    ><History :size="16" />编号记录</button
  >
  <Modal
    v-if="show"
    :title="history ? `${noun}编号记录` : preview?.undo_of ? `还原${noun}编号` : `${noun}编号预览`"
    wide
    @close="!busy && (show = false)"
  >
    <p v-if="busy" role="status">正在处理，请稍候…</p>
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
          @change="page = $event - 1"
      /></div>
      <div class="modal-actions"
        ><button :disabled="busy" @click="show = false">取消</button
        ><button class="primary" :disabled="busy || !preview.changed" @click="apply">{{
          preview.undo_of ? '确认整批还原' : '确认编号'
        }}</button></div
      >
    </template>
    <template v-if="history">
      <p class="help">当前目录最近 50 次操作。未完成的操作会在重启时尝试恢复，也可手动重试恢复。</p>
      <article v-for="batch in records" :key="batch.id" class="numbering-record"
        ><div
          ><strong
            >{{ batch.undo_of ? '整批还原' : `${noun}编号` }} ·
            {{ states[batch.status] || batch.status }}</strong
          ><p class="numbering-record-meta"
            ><DateTime :value="batch.created" /><span
              >{{ batch.changed }} {{ unit }}修改 / {{ batch.total }} {{ unit }}{{ noun }}</span
            ></p
          ><p v-if="batch.error">{{ batch.error }}</p
          ><NumberingDiagnostics
            v-if="batch.processing_log"
            :message="batch.error"
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
  </Modal>
</template>
