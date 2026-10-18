<script setup lang="ts">
  import PageNavigation from '../../components/PageNavigation.vue';
  import ToastNotice from '../../components/ToastNotice.vue';
  import { ref, onActivated, onDeactivated, onUnmounted, computed, nextTick, watch } from 'vue';
  import {
    Folder,
    FileVideo,
    FileAudio,
    File,
    ChevronRight,
    ChevronLeft,
    RefreshCw,
    Info,
    ScanLine,
    Search,
    ArrowUpRight,
    PenLine,
    Undo2,
    Sparkles,
    Home,
    History,
    ArrowRight,
    Link,
    Image as ImageIcon,
    CircleCheck,
    CircleMinus,
    Images
  } from '@lucide/vue';
  import './library-actions.css';
  import { api, ApiError, key } from '../../app/api';
  import { time } from '../../lib/date/format';
  import { size } from '../../lib/bytes/format';
  import { type Entry, type Page, type DirectoryState, type Run } from '../../app/contracts';
  import { statusNames } from '../../app/labels';
  import { ModelPicker } from '../models';
  import StatusBadge from '../../components/StatusBadge.vue';
  import DirectoryStatus from './DirectoryStatus.vue';
  import DirectoryContents from './DirectoryContents.vue';
  import LibraryTableHeader from './LibraryTableHeader.vue';
  import DateTime from '../../components/DateTime.vue';
  import Modal from '../../components/Modal.vue';
  import ModalTransition from '../../components/ModalTransition.vue';
  import { Trash2 } from '@lucide/vue';
  import SelectField from '../../components/SelectField.vue';
  import VideoPlayer from './VideoPlayer.vue';
  import AudioPlayer from './AudioPlayer.vue';
  import MediaNumbering from './MediaNumbering.vue';
  import DirectoryNumbering from './DirectoryNumbering.vue';
  import ScanStatus from './ScanStatus.vue';
  import { useLibraryScan } from './library-scan';
  import {
    video,
    isImage,
    isAudio,
    markable,
    protectedMedia,
    marked,
    selectable,
    normalizedExtension
  } from './entry-types';
  import { useMediaPreview } from './media-preview';
  import { useLibrarySelection } from './selection';
  import { useInlineRename } from './inline-rename';
  defineProps<{ ready: boolean }>();
  const emit = defineEmits<{ run: [] }>();
  const toast = ref<InstanceType<typeof ToastNotice>>();
  const {
    multi,
    chosen,
    autoEntries,
    extractEntries,
    markEntries,
    restoreEntries,
    clearSelection,
    selectEntry,
    selectPage,
    resetSelectionAnchor
  } = useLibrarySelection(() => page.value?.items || []);
  const batch = ref(''),
    batchError = ref(''),
    batchProgress = ref('');
  const deleting = ref<Entry>(),
    deleteError = ref(''),
    deletePath = ref('');
  const deletingBatch = ref<Entry[]>([]),
    deleteBatchPath = ref(''),
    deleteBatchError = ref(''),
    deleteBatchProgress = ref('');
  function confirmBatchDelete() {
    if (saving.value || !chosen.value.size) return;
    deletingBatch.value = [...chosen.value.values()].map((e) => ({ ...e }));
    deleteBatchPath.value = [
      '媒体库',
      ...(page.value?.breadcrumbs.filter((c) => c.id !== 1).map((c) => c.name) || [])
    ].join(' / ');
    deleteBatchError.value = '';
    deleteBatchProgress.value = '';
  }
  async function deleteBatch() {
    if (saving.value || !deletingBatch.value.length) return;
    const entries = [...deletingBatch.value],
      directory = dir.value,
      failed: Entry[] = [],
      errors: string[] = [];
    saving.value = true;
    statusSeq++;
    clearTimeout(timer);
    deleteBatchError.value = '';
    try {
      for (const [index, e] of entries.entries()) {
        deleteBatchProgress.value = `正在删除 ${index + 1} / ${entries.length}：${e.name}`;
        try {
          await api(`/entries/${e.id}`, 'DELETE', { version: e.version, confirmed: true });
          chosen.value.delete(e.id);
        } catch (err) {
          failed.push(e);
          errors.push(`${e.name}：${(err as Error).message}`);
        }
      }
      const summary = `已删除 ${entries.length - failed.length} 项，失败 ${failed.length} 项`;
      toast.value?.show(summary);
      deletingBatch.value = active.value ? failed : [];
      deleteBatchError.value = failed.length ? [summary, ...errors].join('\n') : '';
      resetSelectionAnchor();
      if (directory === dir.value && !disposed) await load();
    } finally {
      saving.value = false;
      deleteBatchProgress.value = '';
    }
  }
  function confirmDelete(e: Entry) {
    if (saving.value) return;
    deleting.value = { ...e };
    deleteError.value = '';
    deletePath.value = [
      ...(page.value?.breadcrumbs.filter((c) => c.id !== 1).map((c) => c.name) || []),
      e.name
    ].join('/');
  }
  async function deleteEntry() {
    if (!deleting.value || saving.value) return;
    saving.value = true;
    deleteError.value = '';
    statusSeq++;
    try {
      const e = deleting.value;
      await api(`/entries/${e.id}`, 'DELETE', { version: e.version, confirmed: true });
      chosen.value.delete(e.id);
      deleting.value = undefined;
      toast.value?.show(`已永久删除 ${e.name}`);
      await load();
    } catch (err) {
      deleteError.value = (err as Error).message;
    } finally {
      saving.value = false;
    }
  }
  function matchesSearchAndType(e: Entry) {
    // SQLite lower() folds ASCII; use the same rule for rows retained locally.
    const lower = (value: string) => value.replace(/[A-Z]/g, (letter) => letter.toLowerCase());
    if (!lower(e.name).includes(lower(q.value))) return false;
    if (!kind.value) return true;
    if (e.kind === 'directory') return kind.value !== 'other';
    if (kind.value === 'directory') return false;
    if (kind.value === 'video') return e.kind === 'file' && video(e);
    if (kind.value === 'image') return isImage(e);
    if (kind.value === 'audio') return isAudio(e);
    return e.kind === 'file' && !video(e) && !isImage(e) && !isAudio(e);
  }
  function matchesProcessingStatus(e: Entry, matchingDirectories: Set<number>) {
    if (!processingStatus.value) return true;
    // Recursive directory status is evaluated by the server in the same
    // snapshot as pagination. Never reinsert a directory excluded by that query.
    if (e.kind === 'directory') return matchingDirectories.has(e.id);
    const value = e.task_status || e.processed || 'unprocessed';
    if (processingStatus.value === 'active')
      return ['queued', 'scanning', 'running', 'paused', 'cancelling'].includes(value);
    if (processingStatus.value === 'processed')
      return ['marked', 'manual', 'automatic'].includes(value);
    return value === processingStatus.value;
  }
  function startBatch(action: string) {
    if (saving.value) return;
    batch.value = action;
    batchError.value = '';
    batchProgress.value = '';
    modelConfigID.value = 0;
    recursive.value = true;
  }
  async function submitBatch() {
    if (saving.value) return;
    statusSeq++;
    saving.value = true;
    batchError.value = '';
    try {
      if (batch.value === 'auto' || batch.value === 'extract') {
        const extracting = batch.value === 'extract';
        const entries = [...(extracting ? extractEntries.value : autoEntries.value)];
        const run = await api('/runs', 'POST', {
          task: extracting ? 'extract' : 'rename',
          targets: entries.map((e) => e.id),
          recursive: recursive.value,
          model_config_id: extracting ? 0 : modelConfigID.value,
          key: key()
        });
        trackRun(run.id, entries);
        toast.value?.show(`${extracting ? '抽帧' : '重命名'}批次 #${run.id} 已加入队列`);
        batch.value = '';
      } else {
        const entries = [...restoreEntries.value];
        let done = 0;
        const failures: string[] = [];
        for (const e of entries) {
          batchProgress.value = `正在还原 ${++done} / ${entries.length}`;
          try {
            await api(`/operations/${e.restore_id}/restore`, 'POST', { key: key() });
            chosen.value.delete(e.id);
          } catch (err) {
            failures.push(`${e.name}：${(err as Error).message}`);
          }
        }
        toast.value?.show(
          `已还原 ${entries.length - failures.length} 项，失败 ${failures.length} 项`
        );
        batchProgress.value = '';
        if (failures.length) batchError.value = failures.join('\n');
        else batch.value = '';
      }
      void syncEntries();
    } catch (err) {
      batchError.value = (err as Error).message;
    } finally {
      saving.value = false;
    }
  }
  const modelConfigID = ref(0);
  const sort = ref('name_asc');
  function toggleSort(field: string) {
    sort.value = sort.value === `${field}_asc` ? `${field}_desc` : `${field}_asc`;
    pageNumber.value = 1;
    load();
  }
  function routeDirectory() {
    const value = Number(location.hash.match(/^#library\/(\d+)$/)?.[1] || 1);
    return Number.isSafeInteger(value) && value > 0 ? value : 1;
  }
  const dir = ref(routeDirectory()),
    page = ref<Page>(),
    busy = ref(false),
    error = ref(''),
    q = ref(''),
    kind = ref(''),
    processingStatus = ref(''),
    pageNumber = ref(1),
    limit = ref(100);
  const {
    preview,
    imageError,
    imageBusy,
    galleryError,
    playing,
    videoBusy,
    videoNotice,
    audioPlaying,
    audioBusy,
    audioNotice,
    closeImage,
    showImage,
    switchImage,
    imageKeys,
    closePlayback,
    closeAudio,
    switchVideo,
    switchAudio,
    play,
    playAudio
  } = useMediaPreview(() => ({
    directory: dir.value,
    search: q.value,
    status: processingStatus.value,
    sort: sort.value
  }));
  const {
    inlineFullName,
    inlineEntry,
    inlineName,
    inlineExtension,
    inlineSaving,
    inlineError,
    setInlineInput,
    cancelInlineRename,
    beginInlineRename,
    editInlineExtension,
    inlineFocusOut,
    saveInlineRename
  } = useInlineRename({
    beforeSave: () => {
      statusSeq++;
    },
    onSaved: async () => {
      toast.value?.show('名称已更新，历史已保存');
      await syncEntries();
    }
  });
  watch(
    [dir, pageNumber, q, kind, processingStatus, sort, limit, multi],
    () => {
      resetSelectionAnchor();
    },
    { flush: 'sync' }
  );
  const selected = ref<Entry>(),
    mode = ref(''),
    newName = ref(''),
    saving = ref(false),
    dialogError = ref(''),
    recursive = ref(true),
    history = ref<any[]>([]);
  let historySequence = 0;
  const trackedRuns = new Set<number>();
  let disposed = false,
    statusSeq = 0;
  let seq = 0,
    timer: ReturnType<typeof setTimeout> | undefined,
    searchTimer: ReturnType<typeof setTimeout> | undefined;
  const extension = computed(() =>
    selected.value?.kind === 'file' ? normalizedExtension(selected.value.name) : ''
  );
  function scheduleSync() {
    clearTimeout(timer);
    if (!disposed && active.value)
      timer = setTimeout(() => {
        if (saving.value) scheduleSync();
        else void syncEntries();
      }, 1500);
  }
  function trackRun(id: number, entries: Entry[]) {
    trackedRuns.add(id);
    for (const e of entries) if (e.kind === 'file' && !e.processed) e.task_status = 'queued';
    scheduleSync();
  }
  async function syncEntries() {
    const n = seq,
      request = ++statusSeq;
    clearTimeout(timer);
    if (disposed || !active.value || busy.value || !page.value) return;
    try {
      const runs = await Promise.all([...trackedRuns].map((id) => api<Run>(`/runs/${id}`)));
      if (n !== seq || request !== statusSeq || disposed) return;
      for (const run of runs)
        if (!['queued', 'scanning', 'running', 'paused', 'cancelling'].includes(run.status))
          trackedRuns.delete(run.id);
      const ids = [
        ...new Set([
          page.value.directory.id,
          ...page.value.items.map((e) => e.id),
          ...chosen.value.keys()
        ])
      ];
      const entries: Entry[] = [];
      for (let i = 0; i < ids.length; i += 300)
        entries.push(
          ...(await api<Entry[]>(`/entries/status?ids=${ids.slice(i, i + 300).join(',')}`))
        );
      if (n !== seq || request !== statusSeq || disposed) return;
      // A queued/scanning run may not have populated run_items yet.
      for (const run of runs)
        if (['queued', 'scanning', 'running', 'paused', 'cancelling'].includes(run.status))
          for (const e of entries)
            if ((run.targets || []).includes(e.id) && !e.processed && !e.task_status)
              e.task_status = run.status;
      const latest = new Map(entries.map((e) => [e.id, e]));
      const directory = latest.get(page.value.directory.id);
      if (directory) Object.assign(page.value.directory, directory);
      page.value.items = page.value.items.filter((e) => latest.has(e.id));
      for (const e of page.value.items) Object.assign(e, latest.get(e.id));
      for (const id of chosen.value.keys()) {
        const e = latest.get(id);
        if (e) chosen.value.set(id, e);
        else chosen.value.delete(id);
      }
      error.value = '';
      // Status and name changes can move an entry into or out of a filter.
      // Recount the whole directory while retaining matching rows and selections.
      if (q.value || kind.value || processingStatus.value) {
        await load(true);
        return;
      }
      if (trackedRuns.size || entries.some((e) => e.task_status || e.progress?.active))
        scheduleSync();
    } catch (e) {
      if (n === seq && request === statusSeq && !disposed) {
        error.value = `状态更新失败：${(e as Error).message}`;
        scheduleSync();
      }
    }
  }
  async function load(quiet = false, keepRows = true, canApply?: () => boolean) {
    if (!quiet) resetSelectionAnchor();
    const n = ++seq;
    clearTimeout(timer);
    busy.value = !quiet;
    error.value = '';
    try {
      const requestedPage = pageNumber.value;
      const params = new URLSearchParams({
        page: String(requestedPage),
        limit: String(limit.value),
        q: q.value,
        kind: kind.value,
        status: processingStatus.value,
        sort: sort.value
      });
      const p = await api<Page>(`/directories/${dir.value}/entries?${params}`);
      if (n !== seq) return;
      // Background scans must not move the rows being read or selected. Fetch by
      // ID as renamed entries can have moved outside this page's latest sort range.
      if (
        quiet &&
        keepRows &&
        page.value?.items.length &&
        (p.page ?? requestedPage) === requestedPage
      ) {
        const entries = await api<Entry[]>(
          `/entries/status?ids=${page.value.items.map((e) => e.id).join(',')}`
        );
        if (n !== seq) return;
        const latest = new Map(entries.map((e) => [e.id, e]));
        const visible = new Set(page.value.items.map((e) => e.id));
        const matchingDirectories = new Set(
          p.items.filter((e) => e.kind === 'directory').map((e) => e.id)
        );
        const rows = page.value.items
          .filter((e) => latest.has(e.id))
          .map((e) => latest.get(e.id)!)
          .filter(matchesSearchAndType)
          .filter((e) => matchesProcessingStatus(e, matchingDirectories));
        p.items = [
          ...rows,
          ...p.items
            .filter((e) => !visible.has(e.id))
            .slice(0, Math.max(0, limit.value - rows.length))
        ];
      }
      // An automatic update may return after selection or editing has begun.
      // Leave the old rows intact until the user explicitly applies the update.
      if (canApply && !canApply()) {
        directoryUpdateAvailable.value = true;
        if (trackedRuns.size) scheduleSync();
        return;
      }
      pageNumber.value = p.page ?? requestedPage;
      page.value = p;
      // This response applies the latest list, which may already be newer than
      // the last lightweight state poll. Do not keep an outdated update notice.
      directoryUpdateAvailable.value = false;
      if (inlineEntry.value && !p.items.some((e) => e.id === inlineEntry.value?.id))
        cancelInlineRename();
      await restorePosition(n, p);
      if (n !== seq) return;
      for (const e of p.items) if (chosen.value.has(e.id)) chosen.value.set(e.id, e);
      if (p.refreshing) timer = setTimeout(() => load(true, keepRows, canApply), 1500);
      else if (
        trackedRuns.size ||
        p.directory.task_status ||
        p.directory.progress?.active ||
        p.items.some((e) => e.task_status || e.progress?.active)
      )
        scheduleSync();
    } catch (e) {
      if (n === seq) {
        error.value = (e as Error).message;
        if (canApply) directoryUpdateAvailable.value = true;
        if (quiet && (q.value || kind.value || processingStatus.value)) scheduleSync();
      }
    } finally {
      if (n === seq) busy.value = false;
    }
  }
  type DirectoryPosition = {
    pageNumber: number;
    q: string;
    kind: string;
    status: string;
    sort: string;
    limit: number;
    scrollY: number;
    anchor?: number;
  };
  const positions = new Map<number, DirectoryPosition>();
  const snapshots = new Map<number, Page>();
  const listRoot = ref<HTMLElement>(),
    returnAnchor = ref<number>();
  const fixedControls = ref<HTMLElement>(),
    fileTable = ref<HTMLTableElement>();
  const headerScroll = ref<HTMLElement>();
  const headerColumns = ref<number[]>([]),
    headerWidth = ref(0);
  let layoutObserver: ResizeObserver | undefined,
    headerFrame = 0;
  // Keep the visible header in the sticky controls. Only column geometry changes
  // need measuring; vertical scrolling is handled entirely by CSS.
  function updateHeaderLayout() {
    headerFrame = 0;
    const table = fileTable.value,
      head = table?.tHead;
    if (!table || !head) return;
    headerColumns.value = Array.from(
      head.rows[0].cells,
      (cell) => cell.getBoundingClientRect().width
    );
    headerWidth.value = table.getBoundingClientRect().width;
    if (headerScroll.value) headerScroll.value.scrollLeft = table.parentElement!.scrollLeft;
  }
  function scheduleHeaderLayout() {
    if (!headerFrame) headerFrame = requestAnimationFrame(updateHeaderLayout);
  }
  function syncTableScroll(event: Event) {
    const source = event.currentTarget as HTMLElement;
    const target =
      source === headerScroll.value ? fileTable.value?.parentElement : headerScroll.value;
    if (target && target.scrollLeft !== source.scrollLeft) target.scrollLeft = source.scrollLeft;
  }
  function observeLayout() {
    layoutObserver = new ResizeObserver(scheduleHeaderLayout);
    if (fixedControls.value) layoutObserver.observe(fixedControls.value);
    if (fileTable.value) layoutObserver.observe(fileTable.value);
    scheduleHeaderLayout();
  }
  function stopObservingLayout() {
    layoutObserver?.disconnect();
    layoutObserver = undefined;
    cancelAnimationFrame(headerFrame);
    headerFrame = 0;
  }
  watch([multi, sort, page], () => {
    void nextTick(scheduleHeaderLayout);
  });
  let pendingPosition: DirectoryPosition | undefined;
  const nativeScrollRestoration = window.history.scrollRestoration;
  const active = ref(false);
  let parkedScrollY = 0,
    positionSerial = 0;
  const directoryUpdateAvailable = ref(false),
    directoryPollError = ref('');
  const scan = useLibraryScan(pollDirectoryState);
  const {
    state: scanState,
    error: scanError,
    actionError: scanActionError,
    submitting: scanSubmitting,
    scanning
  } = scan;
  // A closing dialog lingers for its fade-out marked aria-hidden; it no longer blocks updates.
  const openDialog = '[role="dialog"]:not([aria-hidden="true"] *)';
  // Never replace a list while the user is selecting, editing or viewing media.
  // The explicit update button keeps selected IDs and the current page intact.
  function listInUse(ignoreSelection = false) {
    return (
      (!ignoreSelection && chosen.value.size > 0) ||
      saving.value ||
      inlineEntry.value ||
      playing.value ||
      audioPlaying.value ||
      preview.value ||
      document.querySelector(openDialog) ||
      document.activeElement?.matches('input,textarea,[contenteditable="true"]')
    );
  }
  async function pollDirectoryState(signal: AbortSignal) {
    const directory = dir.value,
      request = seq;
    if (!page.value || busy.value || !active.value) return;
    try {
      const state = await api<DirectoryState>(
        `/directories/${directory}/state`,
        'GET',
        undefined,
        signal
      );
      if (signal.aborted || !active.value || directory !== dir.value || request !== seq) return;
      directoryPollError.value = state.error ? `目录扫描未完成：${state.error}` : '';
      if (state.generation === page.value?.generation) directoryUpdateAvailable.value = false;
      else if (listInUse()) directoryUpdateAvailable.value = true;
      // Idle updates stay silent. Only show a notice if applying the response
      // is deferred by an interaction, or if loading the new list fails.
      else await applyDirectoryUpdate();
    } catch (e) {
      if (!signal.aborted && active.value && directory === dir.value && request === seq)
        directoryPollError.value = `目录更新检查失败：${(e as Error).message}`;
    }
  }
  async function applyDirectoryUpdate(manual = false) {
    if (busy.value || saving.value || inlineEntry.value || document.querySelector(openDialog))
      return;
    const directory = dir.value,
      top = window.scrollY,
      position = positionSerial;
    // A quiet load keeps filters, page number and selected IDs. Request the
    // current sorted page so new files become visible without another disk scan.
    await load(true, false, () => active.value && !document.hidden && !listInUse(manual));
    if (active.value && directory === dir.value && position === positionSerial) {
      await nextTick();
      window.scrollTo({ top, behavior: 'instant' });
    }
  }
  watch(dir, () => {
    directoryUpdateAvailable.value = false;
    directoryPollError.value = '';
  });
  async function restorePosition(request: number, result: Page) {
    const saved = pendingPosition;
    if (!saved || (result.refreshing && !result.items.some((e) => e.id === saved.anchor))) return;
    await nextTick();
    if (disposed || request !== seq || saved !== pendingPosition) return;
    pendingPosition = undefined;
    const row = saved.anchor
      ? listRoot.value?.querySelector<HTMLElement>(`[data-entry-id="${saved.anchor}"]`)
      : undefined;
    window.scrollTo({
      top: row
        ? window.scrollY +
          row.getBoundingClientRect().top +
          row.offsetHeight / 2 -
          window.innerHeight / 2
        : saved.scrollY,
      behavior: 'instant'
    });
    if (row) {
      returnAnchor.value = saved.anchor;
      // 行内异步内容(如目录状态)在滚动后才渲染会改变行高,半秒内多次复核并校正,保持锚定行精确居中。
      for (const delay of [0, 60, 180, 400]) {
        setTimeout(() => {
          if (disposed || request !== seq || pendingPosition || !returnAnchor.value) return;
          const r = listRoot.value?.querySelector<HTMLElement>(`[data-entry-id="${saved.anchor}"]`);
          if (!r) return;
          const rect = r.getBoundingClientRect(),
            offset = rect.top + rect.height / 2 - window.innerHeight / 2;
          if (Math.abs(offset) > 1)
            window.scrollTo({ top: window.scrollY + offset, behavior: 'instant' });
        }, delay);
      }
    }
  }
  function cancelPendingPosition() {
    pendingPosition = undefined;
    positionSerial++;
  }
  function scrollKey(event: KeyboardEvent) {
    if (['ArrowDown', 'ArrowUp', 'PageDown', 'PageUp', 'Home', 'End', ' '].includes(event.key))
      cancelPendingPosition();
  }
  function clearReturnOnBlank(event: MouseEvent) {
    const target = event.target;
    if (
      !(target instanceof Element) ||
      target.closest(
        '[data-entry-id],button,a,input,select,textarea,[role="dialog"],[role="listbox"]'
      )
    )
      return;
    returnAnchor.value = undefined;
    pendingPosition = undefined;
  }
  function enter(id: number, updateRoute = true) {
    if (id === dir.value) return;
    cancelInlineRename();
    clearTimeout(searchTimer);
    returnAnchor.value = undefined;
    clearSelection();
    const child = page.value?.items.find((e) => e.id === id && e.kind === 'directory');
    positions.set(dir.value, {
      pageNumber: pageNumber.value,
      q: q.value,
      kind: kind.value,
      status: processingStatus.value,
      sort: sort.value,
      limit: limit.value,
      scrollY: window.scrollY,
      anchor: child?.id ?? positions.get(dir.value)?.anchor
    });
    // Bound only the heavy lists. Evicting a snapshot must never discard the
    // page, filters or anchor needed to restore an older directory from the API.
    snapshots.delete(dir.value);
    if (page.value) snapshots.set(dir.value, page.value);
    if (snapshots.size > 30) snapshots.delete(snapshots.keys().next().value!);
    const saved = positions.get(id);
    pendingPosition = saved ? { ...saved } : undefined;
    if (!saved) window.scrollTo({ top: 0, behavior: 'instant' });
    dir.value = id;
    if (updateRoute && (routeDirectory() !== id || !location.hash.startsWith('#library/')))
      location.hash = `library/${id}`;
    pageNumber.value = saved?.pageNumber || 1;
    q.value = saved?.q || '';
    kind.value = saved?.kind ?? '';
    processingStatus.value = saved?.status ?? '';
    sort.value = saved?.sort ?? sort.value;
    limit.value = saved?.limit ?? limit.value;
    page.value = snapshots.get(id);
    void load(!!page.value);
    if (page.value) void restorePosition(seq, page.value);
  }
  function search() {
    pageNumber.value = 1;
    seq++;
    clearTimeout(timer);
    clearTimeout(searchTimer);
    searchTimer = setTimeout(load, 250);
  }
  async function refresh() {
    const directory = dir.value;
    try {
      await api(`/directories/${directory}/refresh`, 'POST', {});
      if (directory === dir.value) await load(false, false);
    } catch (e) {
      if (directory === dir.value) error.value = (e as Error).message;
    }
  }
  async function open(e: Entry, m: string) {
    if (saving.value) return;
    const request = ++historySequence;
    if (m === 'auto') modelConfigID.value = 0;
    if (m === 'extract') recursive.value = true;
    selected.value = e;
    mode.value = m;
    dialogError.value = '';
    newName.value =
      e.kind === 'file'
        ? e.name.slice(0, e.name.length - (e.name.match(/\.[^.]+$/)?.[0].length || 0))
        : e.name;
    history.value = [];
    if (m === 'restore' || m === 'history') {
      try {
        const records = await api(`/entries/${e.id}/history`);
        if (request === historySequence && mode.value === m && selected.value?.id === e.id)
          history.value = records;
      } catch (err) {
        if (request === historySequence && mode.value === m && selected.value?.id === e.id)
          dialogError.value = (err as Error).message;
      }
    }
  }
  function closeDialog() {
    historySequence++;
    mode.value = '';
  }
  async function submit() {
    if (!selected.value) return;
    statusSeq++;
    saving.value = true;
    dialogError.value = '';
    try {
      const e = selected.value;
      if (mode.value === 'rename') {
        await api(`/entries/${e.id}/rename`, 'POST', {
          name: newName.value + extension.value,
          version: e.version,
          key: key()
        });
        toast.value?.show('名称已更新，历史已保存');
      } else if (mode.value === 'restore') {
        await api(`/operations/${e.restore_id}/restore`, 'POST', { key: key() });
        toast.value?.show('名称已还原，可以再次自动命名');
      } else {
        const extracting = mode.value === 'extract';
        const run = await api('/runs', 'POST', {
          task: extracting ? 'extract' : 'rename',
          model_config_id: extracting ? 0 : modelConfigID.value,
          targets: [e.id],
          recursive: recursive.value,
          key: key()
        });
        trackRun(run.id, [e]);
        toast.value?.show(`${extracting ? '抽帧' : '重命名'}批次 #${run.id} 已加入队列`);
      }
      closeDialog();
      await syncEntries();
    } catch (err) {
      dialogError.value = (err as Error).message;
    } finally {
      saving.value = false;
    }
  }
  const marking = ref<Entry[]>([]),
    markValue = ref(true),
    markError = ref(''),
    markProgress = ref(''),
    markRetryUnavailable = ref(false);
  interface MarkResult {
    key: string;
    status: string;
    changed: number;
    unchanged: number;
    failed: number;
    interrupted: boolean;
    failures: { name: string; message: string }[];
  }
  let markKeys: string[] = [];
  function openMark(entries: Entry[], value: boolean) {
    if (saving.value) return;
    marking.value = [...entries];
    markValue.value = value;
    markError.value = '';
    markProgress.value = '';
    markRetryUnavailable.value = false;
    markKeys = [];
  }
  async function submitMark() {
    if (saving.value || markRetryUnavailable.value || !marking.value.length) return;
    const entries = [...marking.value],
      markedValue = markValue.value;
    statusSeq++;
    saving.value = true;
    markError.value = '';
    let changed = 0,
      unchanged = 0,
      failed = 0,
      confirmed = 0;
    let stage: 'submit' | 'query' | 'interrupted' = 'submit',
      rejected = false;
    const failures: string[] = [];
    try {
      for (let i = 0; i < entries.length; i += 100) {
        const batchIndex = i / 100,
          existingKey = markKeys[batchIndex],
          requestKey = existingKey ?? (markKeys[batchIndex] = key());
        markProgress.value = `正在处理第 ${i + 1}–${Math.min(i + 100, entries.length)} 个目标…`;
        stage = existingKey ? 'query' : 'submit';
        let result: MarkResult;
        if (existingKey) {
          result = await api<MarkResult>(
            `/entries/processed/status/${encodeURIComponent(requestKey)}`
          );
        } else {
          try {
            result = await api<MarkResult>('/entries/processed', 'POST', {
              targets: entries.slice(i, i + 100).map((e) => e.id),
              marked: markedValue,
              key: requestKey
            });
          } catch (e) {
            // A proxy or a lost response may fail after the task has started.
            // Only an explicit application rejection permits a new submission.
            if (e instanceof ApiError && [400, 401, 403, 409, 429].includes(e.status)) {
              delete markKeys[batchIndex];
              rejected = true;
            }
            throw e;
          }
        }
        while (result.status === 'running') {
          confirmed = changed + result.changed;
          markProgress.value = `后台处理中：已变更 ${changed + result.changed} 个文件，未变更 ${unchanged + result.unchanged} 个，失败 ${failed + result.failed} 项…`;
          await new Promise((resolve) => setTimeout(resolve, 1000));
          if (disposed) return;
          stage = 'query';
          result = await api<MarkResult>(
            `/entries/processed/status/${encodeURIComponent(requestKey)}`
          );
        }
        changed += result.changed;
        unchanged += result.unchanged;
        failed += result.failed;
        confirmed = changed;
        failures.push(...result.failures.map((e) => `${e.name}：${e.message}`));
        if (result.interrupted) {
          stage = 'interrupted';
          markRetryUnavailable.value = true;
          throw new Error('标记任务已中断，仍有目标未处理');
        }
      }
      markKeys = [];
      toast.value?.show(
        `${markedValue ? '已标记' : '已取消标记'} ${changed} 个文件，未变更 ${unchanged} 个，失败 ${failed} 项`
      );
      if (failed) {
        markRetryUnavailable.value = true;
        markError.value = [
          `已变更 ${changed} 个文件，未变更 ${unchanged} 个，失败 ${failed} 项；已完成的标记会保留。请关闭弹窗，重新选择失败目标后再操作。`,
          ...failures
        ].join('\n');
      } else marking.value = [];
    } catch (e) {
      if (stage === 'query' && e instanceof ApiError && e.status === 404) {
        markRetryUnavailable.value = true;
        markError.value = `已确认变更 ${confirmed} 个文件；标记任务结果已不可查询（可能已回收、过期或服务重启）；已完成的标记会保留。请关闭弹窗，重新选择目标后再操作。`;
      } else if (stage === 'interrupted') {
        markError.value = `已确认变更 ${confirmed} 个文件；${(e as Error).message}；已完成的标记会保留。请关闭弹窗，重新选择未处理目标后再操作。`;
      } else if (rejected) {
        markError.value = `已确认变更 ${confirmed} 个文件；${(e as Error).message}；本批次未启动，可再次确认提交；已完成的标记会保留。`;
      } else {
        markError.value = `已确认变更 ${confirmed} 个文件；${stage === 'query' ? '查询中断' : '提交结果未确认'}：${(e as Error).message}。后台任务可能仍在执行，可再次确认查询原任务；已完成的标记会保留。`;
      }
    } finally {
      markProgress.value = '';
      saving.value = false;
      await syncEntries();
    }
  }
  function status(e: Entry) {
    if (e.task_status) return statusNames[e.task_status] || e.task_status;
    return e.processed === 'marked'
      ? protectedMedia(e)
        ? '已标记 · 跳过编号'
        : '手动标记已处理'
      : e.processed === 'manual'
        ? '手动命名'
        : e.processed === 'automatic'
          ? '自动命名'
          : e.processed === 'restore'
            ? '已还原'
            : '未处理';
  }
  function routeChanged() {
    const id = routeDirectory();
    if ((!location.hash || location.hash.startsWith('#library')) && id !== dir.value)
      enter(id, false);
  }
  function activate() {
    if (active.value) return;
    active.value = true;
    scan.startPolling();
    window.history.scrollRestoration = 'manual';
    window.addEventListener('wheel', cancelPendingPosition, { passive: true });
    window.addEventListener('touchmove', cancelPendingPosition, { passive: true });
    window.addEventListener('pointerdown', cancelPendingPosition);
    window.addEventListener('keydown', scrollKey);
    window.addEventListener('keydown', imageKeys);
    document.addEventListener('click', clearReturnOnBlank);
    window.addEventListener('hashchange', routeChanged);
    void nextTick(() => {
      if (!active.value) return;
      observeLayout();
      window.scrollTo({ top: parkedScrollY, behavior: 'instant' });
      if (location.hash.startsWith('#library') && routeDirectory() !== dir.value)
        enter(routeDirectory(), false);
      else void load(!!page.value);
    });
  }
  function deactivate() {
    if (!active.value) return;
    parkedScrollY = window.scrollY;
    stopObservingLayout();
    active.value = false;
    scan.stopPolling();
    seq++;
    statusSeq++;
    closeDialog();
    batch.value = '';
    marking.value = [];
    deleting.value = undefined;
    deletingBatch.value = [];
    closeImage();
    cancelInlineRename();
    closePlayback();
    window.removeEventListener('wheel', cancelPendingPosition);
    window.removeEventListener('touchmove', cancelPendingPosition);
    window.removeEventListener('pointerdown', cancelPendingPosition);
    window.removeEventListener('keydown', scrollKey);
    window.removeEventListener('keydown', imageKeys);
    document.removeEventListener('click', clearReturnOnBlank);
    window.removeEventListener('hashchange', routeChanged);
    window.history.scrollRestoration = nativeScrollRestoration;
    clearTimeout(timer);
    clearTimeout(searchTimer);
  }
  onActivated(activate);
  onDeactivated(deactivate);
  onUnmounted(() => {
    deactivate();
    disposed = true;
  });
</script>
<template>
  <section ref="listRoot" class="library-page">
    <div class="page-heading library-heading"
      ><h1>文件目录<span class="title-dot">.</span></h1
      ><div class="heading-actions directory-mark-actions" role="group" aria-label="当前目录操作">
        <div class="directory-action-row"
          ><button
            class="library-action action-extract"
            aria-label="抽帧当前目录"
            :disabled="busy || !page || !!page.directory.task_status"
            @click="open(page!.directory, 'extract')"
            ><Images :size="16" />抽帧</button
          ><button
            class="library-action action-name"
            aria-label="重命名当前目录"
            :disabled="!ready || !page || !!page.directory.task_status"
            @click="open(page!.directory, 'auto')"
            ><Sparkles :size="16" />重命名</button
          ><DirectoryNumbering
            @notice="toast?.show($event)"
            :directory="dir"
            :disabled="busy || !page || !!page.directory.task_status"
            @changed="
              preview = undefined;
              clearSelection();
              syncEntries();
            "
          /><button
            class="library-action action-mark"
            title="标记当前目录及子目录中的现有媒体：视频跳过自动命名，图片和音频跳过编号"
            :disabled="!page"
            @click="openMark([page!.directory], true)"
            ><CircleCheck :size="16" />标记</button
          ><button
            class="library-action action-unmark"
            title="清除当前目录及子目录中媒体的标记，允许视频再次自动命名、图片和音频再次编号；文件名不变"
            :disabled="!page"
            @click="openMark([page!.directory], false)"
            ><CircleMinus :size="16" />取消标记</button
          ></div
        >
      </div></div
    >
    <ToastNotice ref="toast" /><div v-if="error" class="notice error" role="alert"
      >{{ error }}<button @click="enter(1)">返回媒体库</button
      ><button @click="load()">重试</button></div
    >
    <div
      v-if="scanError || scanActionError || directoryPollError"
      class="notice error"
      role="alert"
    >
      {{ scanActionError || scanError || directoryPollError
      }}<button @click="scan.refresh">重试状态读取</button>
    </div>
    <ScanStatus
      v-if="scanState?.status.started"
      class="library-scan-status"
      compact
      :status="scanState.status"
      :submitting="scanSubmitting"
      @cancel="scan.cancel"
    />
    <Teleport to="body">
      <div
        v-if="active && directoryUpdateAvailable"
        class="directory-update-notice"
        role="status"
        aria-live="polite"
        aria-atomic="true"
      >
        <Info class="directory-update-icon" :size="20" aria-hidden="true" />
        <div class="directory-update-content">
          <strong>目录有更新</strong>
          <p>当前选择和操作已保留，更新后显示最新文件。</p>
          <button
            :disabled="busy || saving || !!inlineEntry || !!playing || !!audioPlaying || !!preview"
            @click="applyDirectoryUpdate(true)"
            >更新文件列表</button
          >
        </div>
      </div>
    </Teleport>
    <div class="panel file-panel library-directory-overview">
      <div class="panel-toolbar directory-toolbar"
        ><div class="breadcrumbs"
          ><button @click="enter(1)"><Home :size="15" />媒体库</button
          ><template v-for="crumb in page?.breadcrumbs.filter((x) => x.id !== 1)" :key="crumb.id"
            ><ChevronRight :size="13" /><button @click="enter(crumb.id)">{{
              crumb.name
            }}</button></template
          ></div
        ><div class="directory-summary"
          ><DirectoryStatus v-if="page?.directory.progress" :entry="page.directory" /><div
            class="directory-refresh-summary"
            ><DirectoryContents inline :contents="page?.directory.contents" /><button
              :disabled="busy"
              @click="refresh"
              ><RefreshCw :size="16" :class="{ spin: busy }" />刷新</button
            ><span class="directory-scan-control"
              ><button
                class="directory-recursive-scan"
                aria-label="扫描当前目录及子目录"
                title="重新读取当前目录及全部子目录，只同步文件列表"
                :disabled="busy || !page || scanSubmitting || scanning"
                @click="scan.start([dir])"
                ><ScanLine :size="16" />扫描子目录</button
              ></span
            ></div
          ></div
        ></div
      >
    </div>
    <div ref="fixedControls" class="library-fixed-controls">
      <div class="panel file-panel library-panel-controls">
        <div class="filterbar"
          ><button
            :aria-pressed="multi"
            @click="
              multi = !multi;
              clearSelection();
            "
            >{{ multi ? '退出多选' : '多选' }}</button
          ><div class="search-input"
            ><Search :size="16" /><input
              v-model="q"
              placeholder="搜索当前目录…"
              aria-label="搜索当前目录"
              @input="search" /></div
          ><SelectField
            v-model="kind"
            @change="
              pageNumber = 1;
              load();
            "
            label="类型筛选"
            :options="[
              { value: '', label: '全部类型' },
              { value: 'directory', label: '目录' },
              { value: 'video', label: '视频' },
              { value: 'image', label: '图片' },
              { value: 'audio', label: '音频' },
              { value: 'other', label: '其他文件' }
            ]"
          /><SelectField
            v-model="processingStatus"
            @change="
              pageNumber = 1;
              load();
            "
            label="处理状态筛选"
            :options="[
              { value: '', label: '全部状态' },
              { value: 'unprocessed', label: '未处理' },
              { value: 'active', label: '处理中' },
              { value: 'processed', label: '已处理' },
              { value: 'marked', label: '已标记' },
              { value: 'manual', label: '手动命名' },
              { value: 'automatic', label: '自动命名' }
            ]"
          /><MediaNumbering
            @notice="toast?.show($event)"
            v-if="kind === 'image' || kind === 'audio'"
            :media="kind"
            :directory="dir"
            :query="q"
            :status="processingStatus"
            :sort="sort"
            :disabled="busy || !page || !!page.refreshing || !!page.directory.task_status"
            @changed="
              preview = undefined;
              clearSelection();
              syncEntries();
            "
          /><div class="filter-spacer" /><span class="subtle"
            >共 {{ page?.total ?? 0 }} 项 · {{ page?.items.length || 0 }} 项 / 当前页</span
          ></div
        >
        <div v-if="multi" class="batch-toolbar"
          ><strong>已选 {{ chosen.size }} 项</strong
          ><button title="全选本页" @click="selectPage()">全选</button
          ><button title="反选本页" @click="selectPage(true)">反选</button
          ><button :disabled="!chosen.size" @click="clearSelection()">清空</button
          ><span class="batch-delete-group"
            ><span class="batch-divider" aria-hidden="true" /><button
              class="delete-confirm"
              :disabled="saving || !chosen.size"
              @click="confirmBatchDelete"
              ><Trash2 :size="15" />删除</button
            ><span class="batch-divider" aria-hidden="true" /></span
          ><button
            class="library-action action-extract"
            :disabled="!extractEntries.length"
            @click="startBatch('extract')"
            ><Images :size="15" />抽帧</button
          ><button
            class="library-action action-name"
            :disabled="!ready || !autoEntries.length"
            @click="startBatch('auto')"
            ><Sparkles :size="15" />命名</button
          ><span class="batch-divider" aria-hidden="true" /><button
            class="library-action action-mark"
            title="标记选中媒体：视频跳过自动命名，图片和音频跳过编号；目录包含子目录"
            :disabled="!markEntries.length"
            @click="openMark(markEntries, true)"
            ><CircleCheck :size="15" />标记已处理</button
          ><button
            class="library-action action-unmark"
            title="清除选中媒体的标记，允许再次自动命名或编号；文件名不变"
            :disabled="!markEntries.length"
            @click="openMark(markEntries, false)"
            ><CircleMinus :size="15" />取消标记</button
          ><button :disabled="!restoreEntries.length" @click="startBatch('restore')"
            ><Undo2 :size="15" />还原</button
          ></div
        >
        <div v-if="page?.error" class="notice error">目录同步失败：{{ page.error }}</div
        ><div class="pagination"
          ><span class="subtle">{{
            page?.refreshed ? `更新于 ${time(page.refreshed)}` : '等待首次同步'
          }}</span
          ><SelectField
            v-model="limit"
            @change="
              pageNumber = 1;
              load();
            "
            label="每页条目数"
            :options="[
              { value: 50, label: '50 项 / 页' },
              { value: 100, label: '100 项 / 页' },
              { value: 200, label: '200 项 / 页' }
            ]" /><PageNavigation
            :page="pageNumber"
            :total-pages="Math.max(1, Math.ceil((page?.total || 0) / limit))"
            :disabled="busy"
            @change="
              pageNumber = $event;
              load();
            " /></div
      ></div>
      <div ref="headerScroll" class="library-table-header" @scroll="syncTableScroll">
        <table class="file-table" :style="{ width: headerWidth ? `${headerWidth}px` : undefined }">
          <colgroup
            ><col
              v-for="(width, index) in headerColumns"
              :key="index"
              :style="{ width: `${width}px` }"
          /></colgroup>
          <LibraryTableHeader :multi="multi" :sort="sort" @sort="toggleSort" />
        </table>
      </div>
    </div>
    <div class="panel file-panel library-file-rows">
      <div class="table-scroll" @scroll="syncTableScroll"
        ><table ref="fileTable" class="file-table"
          ><LibraryTableHeader :multi="multi" :sort="sort" sizing />
          <tbody
            ><tr
              v-for="e in page?.items"
              :key="e.id"
              :data-entry-id="e.id"
              :class="{
                'is-selected': multi && chosen.has(e.id),
                'return-anchor': returnAnchor === e.id
              }"
              ><td v-if="multi" class="selection-cell"
                ><input
                  v-if="selectable(e)"
                  type="checkbox"
                  :aria-label="`选择 ${e.name}`"
                  :checked="chosen.has(e.id)"
                  @click="selectEntry(e, $event)"
                  title="按住 Shift 点击可连续选择当前页条目"
              /></td>
              <td class="filename-cell"
                ><div class="file-name"
                  ><button
                    v-if="e.kind === 'file' && video(e)"
                    class="file-symbol video-symbol"
                    :aria-label="`播放视频 ${e.name}`"
                    @click="play(e)"
                    ><FileVideo :size="21" /></button
                  ><button
                    v-else-if="isImage(e)"
                    class="file-symbol image-symbol"
                    :aria-label="`预览图片 ${e.name}`"
                    @click="showImage(e)"
                    ><ImageIcon :size="21" /></button
                  ><button
                    v-else-if="isAudio(e)"
                    class="file-symbol audio-symbol"
                    :aria-label="`播放音频 ${e.name}`"
                    @click="playAudio(e)"
                    ><FileAudio :size="21" /></button
                  ><span v-else class="file-symbol" :class="{ directory: e.kind === 'directory' }"
                    ><Folder v-if="e.kind === 'directory'" :size="21" /><Link
                      v-else-if="e.kind === 'symlink'"
                      :size="21" /><FileVideo v-else-if="video(e)" :size="21" /><File
                      v-else
                      :size="21" /></span
                  ><div v-if="inlineEntry?.id === e.id" class="inline-rename"
                    ><div
                      class="inline-rename-field"
                      @focusout="inlineFocusOut"
                      @keydown.esc.prevent="cancelInlineRename"
                      ><input
                        :ref="setInlineInput"
                        v-model="inlineName"
                        :aria-label="`编辑 ${e.name}`"
                        :readonly="inlineSaving"
                        maxlength="240"
                        @keydown.enter.prevent="saveInlineRename"
                      /><button
                        v-if="inlineExtension && !inlineFullName"
                        type="button"
                        class="rename-extension"
                        aria-label="修改后缀名"
                        title="点击修改后缀名"
                        :disabled="inlineSaving"
                        @mousedown.prevent
                        @click="editInlineExtension"
                        >{{ inlineExtension }}</button
                      ></div
                    ><small v-if="inlineError" role="alert">{{ inlineError }}</small></div
                  ><button
                    v-else-if="e.kind === 'directory'"
                    class="name-button"
                    :title="e.name"
                    @click="enter(e.id)"
                    ><span class="name-text">{{ e.name }}</span
                    ><ChevronRight :size="14" /></button
                  ><button
                    v-else-if="e.kind === 'file'"
                    type="button"
                    class="name-text inline-rename-trigger"
                    :title="`双击重命名 ${e.name}`"
                    :aria-label="`双击或按 Enter 重命名 ${e.name}`"
                    @dblclick="beginInlineRename(e)"
                    @keydown.enter.prevent="beginInlineRename(e)"
                    @keydown.f2.prevent="beginInlineRename(e)"
                    >{{ e.name }}</button
                  ><span v-else class="name-text" :title="e.name">{{ e.name }}</span></div
                ></td
              >
              <td class="subtle mono">{{ e.kind === 'directory' ? '—' : size(e.size) }}</td
              ><td class="subtle contents-cell"
                ><DirectoryContents v-if="e.kind === 'directory'" :contents="e.contents" /><span
                  v-else
                  >—</span
                ></td
              ><td
                ><DirectoryStatus v-if="e.kind === 'directory'" :entry="e" /><StatusBadge
                  v-else
                  :status="e.task_status || e.processed || 'unprocessed'"
                  >{{ status(e) }}</StatusBadge
                ></td
              ><td class="subtle date-cell"><DateTime :value="e.mtime" /></td
              ><td class="subtle date-cell"><DateTime :value="e.renamed_at" /></td>
              <td
                ><div class="row-actions" v-if="e.kind === 'file' || e.kind === 'directory'"
                  ><button
                    v-if="isImage(e)"
                    :aria-label="`查看图片 ${e.name}`"
                    title="查看图片"
                    @click="showImage(e)"
                    ><ImageIcon :size="15" /></button
                  ><button
                    v-if="e.kind === 'directory'"
                    title="重命名"
                    :aria-label="`重命名 ${e.name}`"
                    @click="open(e, 'rename')"
                    ><PenLine :size="15" /></button
                  ><button
                    class="library-action action-name"
                    v-if="e.kind === 'directory' || video(e)"
                    :disabled="!ready || !!e.processed || !!e.task_status"
                    :aria-label="`自动命名 ${e.name}`"
                    :title="e.processed ? '已处理，自动任务将跳过' : '自动命名'"
                    @click="open(e, 'auto')"
                    ><Sparkles :size="15" /></button
                  ><button
                    class="library-action action-mark"
                    v-if="markable(e) && (e.kind === 'directory' || !marked(e))"
                    :aria-label="`标记已处理 ${e.name}`"
                    :title="isImage(e) || isAudio(e) ? '标记跳过编号' : '标记已处理'"
                    :disabled="!!e.task_status"
                    @click="openMark([e], true)"
                    ><CircleCheck :size="15" /></button
                  ><button
                    class="library-action action-unmark"
                    v-if="markable(e) && (e.kind === 'directory' || marked(e))"
                    :aria-label="`取消已处理标记 ${e.name}`"
                    title="取消已处理标记"
                    :disabled="!!e.task_status"
                    @click="openMark([e], false)"
                    ><CircleMinus :size="15" /></button
                  ><button
                    v-if="e.restore_id"
                    :aria-label="`还原 ${e.name}`"
                    title="还原"
                    @click="open(e, 'restore')"
                    ><Undo2 :size="15" /></button
                  ><button
                    title="命名历史"
                    :aria-label="`历史 ${e.name}`"
                    @click="open(e, 'history')"
                    ><History :size="15" /></button
                  ><button
                    class="delete-action"
                    title="永久删除"
                    :aria-label="`删除 ${e.name}`"
                    :disabled="!!e.task_status || !!e.progress?.active"
                    @click="confirmDelete(e)"
                    ><Trash2 :size="15" /></button></div
                ><span v-else class="subtle">不可操作</span></td
              ></tr
            ></tbody
          ></table
        >
        <div v-if="!page?.items.length" class="empty"
          ><Folder :size="35" /><h3>{{
            busy || page?.refreshing
              ? '正在读取目录'
              : q || kind || processingStatus
                ? '没有匹配的条目'
                : '这里还没有内容'
          }}</h3
          ><p>{{
            busy || page?.refreshing
              ? '首次进入时会建立当前层的目录索引。'
              : q || kind || processingStatus
                ? '可以调整搜索、类型或处理状态筛选。'
                : '可以返回上级目录，或刷新查看最新文件。'
          }}</p></div
        ></div
      >
    </div>
    <div class="page-footnote"
      ><button class="text-button" @click="emit('run')"
        >查看批次记录 <ArrowUpRight :size="14" /></button
    ></div>
    <ModalTransition
      ><Modal
        v-if="deleting"
        :title="deleting.kind === 'directory' ? '删除目录' : '删除文件'"
        @close="!saving && (deleting = undefined)"
        ><form @submit.prevent="deleteEntry"
          ><p>确定永久删除以下{{ deleting.kind === 'directory' ? '目录' : '文件' }}？</p
          ><p class="delete-path">媒体库 / {{ deletePath }}</p
          ><p v-if="deleting.kind === 'directory'">目录内的所有文件和子目录都会一并删除。</p
          ><p class="help">将从磁盘真实删除，不进入回收站，无法通过还原功能恢复。</p
          ><div v-if="deleteError" class="notice error" role="alert">{{ deleteError }}</div
          ><div class="modal-actions"
            ><button type="button" :disabled="saving" @click="deleting = undefined">取消</button
            ><button class="delete-confirm" :disabled="saving">{{
              saving ? '正在删除…' : '确认永久删除'
            }}</button></div
          ></form
        ></Modal
      ></ModalTransition
    >
    <ModalTransition
      ><Modal v-if="deletingBatch.length" title="批量删除" @close="!saving && (deletingBatch = [])"
        ><form @submit.prevent="deleteBatch">
          <p>确定永久删除选中的 {{ deletingBatch.length }} 个条目？包含其他分页的已选项。</p
          ><p class="delete-path">{{ deleteBatchPath }}</p>
          <ul class="delete-targets"
            ><li v-for="entry in deletingBatch" :key="entry.id"
              >{{ entry.name }}<span v-if="entry.kind === 'directory'">（目录）</span></li
            ></ul
          >
          <p v-if="deletingBatch.some((e) => e.kind === 'directory')"
            >所选目录内的所有文件和子目录都会一并删除。</p
          ><p class="help"
            >将从磁盘真实删除，不进入回收站，无法通过还原功能恢复。逐项执行，失败不会撤销已完成的删除。</p
          >
          <p v-if="deleteBatchProgress" role="status">{{ deleteBatchProgress }}</p
          ><div v-if="deleteBatchError" class="notice error batch-errors" role="alert">{{
            deleteBatchError
          }}</div>
          <div class="modal-actions"
            ><button type="button" :disabled="saving" @click="deletingBatch = []">取消</button
            ><button class="delete-confirm" :disabled="saving">{{
              saving ? '正在删除…' : '确认永久删除'
            }}</button></div
          >
        </form></Modal
      ></ModalTransition
    >
    <ModalTransition
      ><Modal
        v-if="mode"
        :title="
          mode === 'extract'
            ? '目录抽帧'
            : mode === 'rename'
              ? '重命名'
              : mode === 'restore'
                ? '还原名称'
                : mode === 'history'
                  ? '命名历史'
                  : '自动命名'
        "
        @close="!saving && closeDialog()"
        ><form @submit.prevent="submit">
          <div class="selection-summary"
            ><Folder v-if="selected?.kind === 'directory'" :size="23" /><FileVideo
              v-else
              :size="23"
            /><strong>{{ selected?.name || '整个媒体库' }}</strong></div
          >
          <template v-if="mode === 'rename'"
            ><label
              >新名称<div class="extension-input"
                ><input v-model="newName" aria-label="新名称" required maxlength="240" /><span>{{
                  extension
                }}</span></div
              ></label
            ><p class="help">只修改名称，文件保留在原目录。成功后可以还原。</p></template
          >
          <template v-else-if="mode === 'restore'"
            ><p>将恢复到最近一次改名前的名称：</p
            ><div class="restore-target"
              ><Undo2 :size="18" />{{
                history.find((x) => x.id === selected?.restore_id)?.old_name || '正在读取…'
              }}</div
            ><p class="help"
              >还原后可再次自动命名，也会被后续计划重新处理。不会覆盖同名文件。</p
            ></template
          >
          <template v-else-if="mode === 'auto' || mode === 'extract'"
            ><ModelPicker v-if="mode === 'auto'" v-model="modelConfigID" /><p>{{
              mode === 'extract'
                ? '为当前目录内的视频预先生成截图，之后重命名可直接复用缓存。'
                : '后台将抽取视频画面，由指定模型生成名称并自动修改。'
            }}</p
            ><label v-if="selected?.kind === 'directory'" class="check-label"
              ><input type="checkbox" v-model="recursive" />包含子目录</label
            ><p class="help">{{
              mode === 'extract'
                ? '覆盖目录内全部分页的视频，包括已处理视频；已有有效截图直接复用。进度见“批次记录”，结果见“抽帧记录”。'
                : '已命名的视频自动跳过。进度见“批次记录”，命名结果见“运行记录”。'
            }}</p></template
          >
          <template v-else
            ><div v-for="h in history" :key="h.id" class="history-item"
              ><span class="badge"
                >{{
                  h.source === 'restore' ? '还原' : h.source === 'manual' ? '手动命名' : '自动命名'
                }}
                · {{ h.status }}</span
              ><p>{{ h.old_name }} <ArrowRight :size="14" /> {{ h.new_name }}</p
              ><DateTime :value="h.created" /></div
            ><p v-if="!history.length" class="empty compact">暂无命名历史</p></template
          >
          <div v-if="dialogError" class="notice error" role="alert">{{ dialogError }}</div
          ><div class="modal-actions"
            ><button type="button" :disabled="saving" @click="closeDialog()">{{
              mode === 'history' ? '关闭' : '取消'
            }}</button
            ><button
              v-if="mode !== 'history'"
              class="primary"
              :disabled="
                saving ||
                (mode === 'restore' && !history.length) ||
                (mode === 'auto' && !modelConfigID)
              "
              >{{
                saving
                  ? '正在提交…'
                  : mode === 'rename'
                    ? '保存名称'
                    : mode === 'restore'
                      ? '确认还原'
                      : mode === 'extract'
                        ? '抽帧'
                        : '重命名'
              }}</button
            ></div
          ></form
        ></Modal
      ></ModalTransition
    >
    <ModalTransition
      ><Modal
        v-if="batch"
        :title="batch === 'extract' ? '批量抽帧' : batch === 'auto' ? '批量自动命名' : '批量还原'"
        @close="!saving && (batch = '')"
        ><form @submit.prevent="submitBatch">
          <template v-if="batch === 'auto' || batch === 'extract'"
            ><p
              >处理选中的
              {{ batch === 'extract' ? extractEntries.length : autoEntries.length }}
              个条目，创建一个批次。</p
            ><ModelPicker v-if="batch === 'auto'" v-model="modelConfigID" /><label
              class="check-label"
              ><input type="checkbox" v-model="recursive" />包含子目录</label
            ><p class="help">{{
              batch === 'extract'
                ? '预先生成截图供后续重命名使用，已有有效截图直接复用。结果见“抽帧记录”。'
                : '已命名和正在处理的视频会跳过。'
            }}</p></template
          >
          <template v-else
            ><p
              >还原选中的
              {{ restoreEntries.length }} 个条目最近一次改名。目录还原只恢复目录名称。</p
            ><p class="help">还原后可以再次自动命名。冲突条目保留原状。</p></template
          ><p v-if="batchProgress" role="status">{{ batchProgress }}</p
          ><div v-if="batchError" class="notice error batch-errors" role="alert">{{
            batchError
          }}</div
          ><div class="modal-actions"
            ><button type="button" :disabled="saving" @click="batch = ''">关闭</button
            ><button
              class="primary"
              :disabled="
                saving ||
                (batch === 'extract'
                  ? !extractEntries.length
                  : batch === 'auto'
                    ? !modelConfigID || !autoEntries.length
                    : !restoreEntries.length)
              "
              >{{
                saving
                  ? '正在处理…'
                  : batch === 'extract'
                    ? '抽帧'
                    : batch === 'auto'
                      ? '重命名'
                      : '确认批量还原'
              }}</button
            ></div
          ></form
        ></Modal
      ></ModalTransition
    >
    <ModalTransition
      ><Modal
        v-if="marking.length"
        :title="markValue ? '标记已处理' : '取消已处理标记'"
        @close="!saving && (marking = [])"
        ><form @submit.prevent="submitMark"
          ><p
            >已选择 {{ marking.length }} 个目标<span v-if="marking.length === 1"
              >：{{ marking[0].name || '整个媒体库' }}</span
            >。</p
          ><p v-if="marking.some((e) => e.kind === 'directory')"
            >包含所选目录及所有子目录中的现有视频、图片和音频；以后新增的文件不受影响。</p
          ><p>{{
            markValue
              ? '标记后，视频会跳过自动命名和定时任务；图片和音频会跳过全局编号及一键编号，保留你自己的名称。已经处理的视频保留原状态。'
              : '取消后，视频可以再次自动命名，图片和音频可以再次参与编号，包括之前实际改名的文件。'
          }}</p
          ><p class="help"
            >只修改处理状态，不修改文件名、不删除命名历史。活动任务中的文件会报告失败，请等待任务结束后重试。标记保存在数据库中，数据库丢失后需重新标记。</p
          ><p v-if="markProgress" role="status">{{ markProgress }}</p
          ><div v-if="markError" class="notice error batch-errors" role="alert">{{ markError }}</div
          ><div class="modal-actions"
            ><button type="button" :disabled="saving" @click="marking = []">关闭</button
            ><button class="primary" :disabled="saving || markRetryUnavailable">{{
              saving ? '正在处理…' : markValue ? '确认标记' : '确认取消标记'
            }}</button></div
          ></form
        ></Modal
      ></ModalTransition
    >
    <ModalTransition
      ><Modal v-if="playing" :title="playing.name" player @close="closePlayback"
        ><VideoPlayer
          :key="playing.id"
          :src="`/api/entries/${playing.id}/video`"
          :switching="videoBusy"
          @previous="switchVideo(true)"
          @next="switchVideo(false)"
        /><p v-if="videoNotice" role="status">{{ videoNotice }}</p
        ><p class="help">← 快退 10 秒 · → 快进 10 秒 · 两侧按钮切换上一集 / 下一集</p></Modal
      ></ModalTransition
    >
    <ModalTransition
      ><Modal v-if="audioPlaying" :title="audioPlaying.name" @close="closeAudio()"
        ><AudioPlayer
          :key="audioPlaying.id"
          :src="`/api/entries/${audioPlaying.id}/audio`"
          :switching="audioBusy"
          @previous="switchAudio(true)"
          @next="switchAudio(false)"
        /><p v-if="audioNotice" role="status">{{ audioNotice }}</p
        ><p class="help"
          >← 快退 10 秒 · → 快进 10 秒 · 按钮切换上一首 / 下一首 ·
          使用浏览器原生解码，不支持时会提示失败。</p
        ></Modal
      ></ModalTransition
    >

    <ModalTransition
      ><Modal v-if="preview" :title="preview.name" wide @close="closeImage"
        ><div class="image-gallery"
          ><button
            class="image-nav previous"
            aria-label="上一张图片"
            :disabled="imageBusy"
            @click="switchImage(true)"
            ><ChevronLeft :size="25" /></button
          ><img
            v-if="!imageError"
            :key="preview.id"
            class="image-preview"
            :src="`/api/entries/${preview.id}/image`"
            :alt="preview.name"
            @error="imageError = true" /><p v-else class="notice error" role="alert"
            >无法显示图片，文件可能不可用或浏览器不支持其格式。</p
          ><button
            class="image-nav next"
            aria-label="下一张图片"
            :disabled="imageBusy"
            @click="switchImage(false)"
            ><ChevronRight :size="25" /></button></div
        ><p v-if="galleryError" class="notice error" role="alert">{{ galleryError }}</p
        ><p class="help gallery-hint">← / ↑ 上一张 · → / ↓ 下一张 · 仅切换当前目录的图片</p></Modal
      ></ModalTransition
    >
  </section>
</template>

<style scoped>
  .library-scan-status {
    margin-bottom: 12px;
  }
  .directory-update-notice {
    position: fixed;
    right: 24px;
    bottom: 24px;
    z-index: 90;
    display: flex;
    align-items: flex-start;
    gap: 12px;
    width: 380px;
    max-width: calc(100vw - 32px);
    max-height: calc(100dvh - 32px);
    overflow-y: auto;
    padding: 16px;
    border: 1px solid #e8d7a6;
    border-radius: 12px;
    background: #fffbef;
    color: #73551b;
    box-shadow: 0 8px 30px #49340b1a;
    font-size: 13px;
  }
  .directory-update-icon {
    flex-shrink: 0;
    margin-top: 2px;
  }
  .directory-update-content {
    flex: 1;
    min-width: 0;
  }
  .directory-update-content strong {
    font-size: 14px;
  }
  .directory-update-content p {
    margin: 4px 0 12px;
    line-height: 1.6;
    overflow-wrap: anywhere;
  }
  .directory-update-content button {
    border-color: #dcc28a;
    background: #fffdf6;
    color: #73551b;
  }
  .directory-update-content button:hover:not(:disabled) {
    border-color: #c6a45b;
    background: #f7edcf;
  }
  .directory-update-content button:focus-visible {
    outline-color: #c6a45b;
  }
  .library-fixed-controls {
    position: sticky;
    top: 0;
    z-index: 6;
    background: white;
  }
  .library-heading {
    flex-direction: column;
    align-items: flex-start;
    gap: 16px;
    margin-bottom: 24px;
  }
  .library-heading h1 {
    margin: 0;
    white-space: nowrap;
  }
  .library-table-header {
    position: absolute;
    top: 100%;
    left: 0;
    right: 0;
    border: 1px solid var(--line);
    border-top: 0;
    background: #fafbfc;
    overflow-x: auto;
    scrollbar-width: none;
  }
  .library-table-header::-webkit-scrollbar {
    display: none;
  }
  .library-table-header .file-table {
    table-layout: fixed;
    border-collapse: separate;
    border-spacing: 0;
  }
  .library-table-header :deep(th) {
    width: auto;
  }
  .library-table-header :deep(th:last-child) {
    text-align: right;
  }
  .library-directory-overview {
    border-radius: 12px 12px 0 0;
  }
  .library-panel-controls {
    border-top: 0;
    border-radius: 0;
    overflow: visible;
  }
  .library-file-rows {
    border-top: 0;
    border-radius: 0 0 12px 12px;
  }
  .library-file-rows .file-table {
    border-collapse: separate;
    border-spacing: 0;
  }
  .library-file-rows :deep(thead) {
    visibility: hidden;
  }
  .library-panel-controls .pagination {
    border-top: 0;
  }
  .library-panel-controls .filterbar {
    flex-wrap: wrap;
  }
  @media (max-width: 800px) {
    .library-page .page-heading {
      margin-bottom: 12px;
      gap: 8px;
    }
    .library-directory-overview .panel-toolbar {
      padding: 10px 12px;
      gap: 8px;
    }
    .library-panel-controls .filterbar {
      padding: 8px 12px;
      gap: 8px;
    }
    .library-panel-controls .batch-toolbar {
      flex-wrap: nowrap;
      overflow-x: auto;
      padding: 8px 12px;
    }
    .library-panel-controls .batch-toolbar > * {
      flex-shrink: 0;
      white-space: nowrap;
    }
    .library-panel-controls .pagination {
      padding: 8px 12px;
      gap: 8px;
    }
  }
  @media (max-width: 600px) {
    .directory-update-notice {
      right: 16px;
      bottom: max(16px, env(safe-area-inset-bottom));
    }
    .directory-update-content button {
      min-height: 44px;
    }
    .directory-recursive-scan {
      min-height: 44px;
    }
    .library-page .page-heading h1 {
      font-size: 22px;
      margin-bottom: 0;
    }
    .library-directory-overview .directory-toolbar {
      position: relative;
    }
    .library-directory-overview .breadcrumbs {
      padding-right: 42px;
      flex-wrap: nowrap;
      overflow-x: auto;
      scrollbar-width: thin;
    }
    .library-directory-overview .breadcrumbs button {
      white-space: nowrap;
      flex-shrink: 0;
    }
    .library-directory-overview .directory-refresh-summary > button {
      position: absolute;
      top: 8px;
      right: 12px;
      width: 44px;
      min-height: 44px;
      font-size: 0;
      padding: 8px;
      gap: 0;
    }
    .library-panel-controls .filterbar {
      display: grid;
      grid-template-columns: auto minmax(0, 1fr);
    }
    .library-panel-controls .filterbar .search-input {
      min-width: 0;
      width: 100%;
    }
    .library-panel-controls .filterbar > .subtle {
      font-size: 10px;
      text-align: right;
    }
    .library-panel-controls .filter-spacer {
      display: none;
    }
    .library-fixed-controls .pagination > .subtle {
      display: none;
    }
    .library-fixed-controls .pagination > .select-field {
      margin: 0 auto;
    }
  }

  .file-table tr.return-anchor {
    background: #e3f2e9;
    box-shadow: inset 3px 0 #398366;
  }
  .file-table .row-actions button {
    width: 36px;
    min-height: 36px;
    padding: 0;
  }
  .file-table :deep(th:last-child),
  .file-table td:last-child {
    text-align: right;
  }
  .file-table .row-actions {
    justify-content: flex-end;
  }
  .batch-delete-group {
    display: inline-flex;
    align-items: center;
    gap: 10px;
  }
  .batch-delete-group .batch-divider {
    display: block;
  }
  .delete-targets {
    max-height: 220px;
    overflow: auto;
    padding-left: 24px;
    overflow-wrap: anywhere;
  }
  .delete-targets li {
    margin: 6px 0;
  }
  .delete-action {
    color: #b14545;
  }
  .delete-path {
    overflow-wrap: anywhere;
    font-weight: 600;
  }
  .inline-rename-trigger {
    border: 0;
    background: transparent;
    padding: 0;
    text-align: center;
    color: var(--ink);
    font: inherit;
    font-weight: 500;
    cursor: text;
  }
  .inline-rename-trigger:focus-visible {
    outline: 2px solid #89bca7;
    outline-offset: 3px;
    border-radius: 2px;
  }
  .inline-rename {
    min-width: 0;
    max-width: 320px;
    flex: 1;
  }
  .inline-rename-field {
    display: flex;
    align-items: center;
    min-width: 0;
    border: 1px solid #89bca7;
    border-radius: 6px;
    background: white;
    box-shadow: 0 0 0 2px #e3f2e9;
  }
  .inline-rename-field input {
    min-width: 0;
    width: 100%;
    padding: 6px 8px;
    border: 0;
    background: transparent;
    font: inherit;
    color: var(--ink);
  }
  .inline-rename-field input:focus {
    outline: 0;
  }
  .rename-extension {
    flex-shrink: 0;
    border: 0;
    background: transparent;
    padding: 6px 8px;
    white-space: nowrap;
    color: var(--ash);
    font: inherit;
    cursor: text;
  }
  .rename-extension:hover {
    color: var(--ink);
    background: #f1f7f3;
  }
  .rename-extension:focus-visible {
    outline: 2px solid #89bca7;
    outline-offset: -2px;
  }
  .inline-rename small {
    display: block;
    margin-top: 5px;
    color: #b44444;
    overflow-wrap: anywhere;
  }
  .file-table {
    min-width: 1120px;
  }
  .file-table .contents-cell {
    min-width: 76px;
    white-space: nowrap;
    font-size: 11px;
  }
  .directory-summary > button + button {
    margin-left: 0;
  }
</style>
