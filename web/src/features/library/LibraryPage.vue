<script setup lang="ts">
  import { errorText } from '../../app/errors';
  import PageNavigation from '../../components/PageNavigation.vue';
  import { useToast } from '../../app/use-toast';
  import { ref, onActivated, onDeactivated, onUnmounted, computed, nextTick, watch } from 'vue';
  import { useRoute, useRouter } from 'vue-router';
  import {
    Folder,
    FileVideo,
    FileAudio,
    File,
    ChevronRight,
    RefreshCw,
    RotateCw,
    Scan,
    ZoomIn,
    ZoomOut,
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
    Images,
    Trash2
  } from '@lucide/vue';
  import './library-actions.css';
  import { api, newRequestKey, apiItems } from '../../app/api';
  import { formatTime } from '../../lib/format/date';
  import { formatSize } from '../../lib/format/bytes';
  import {
    isActiveRunStatus,
    type Entry,
    type Page,
    type DirectoryState,
    type Operation,
    type Run
  } from '../../app/contracts';
  import { statusNames } from '../../app/labels';
  import { ModelPicker } from '../models';
  import StatusBadge from '../../components/StatusBadge.vue';
  import DirectoryStatus from './DirectoryStatus.vue';
  import DeleteEntriesDialog from './DeleteEntriesDialog.vue';
  import BatchActionDialog from './BatchActionDialog.vue';
  import MarkEntriesDialog from './MarkEntriesDialog.vue';
  import DirectoryContents from './DirectoryContents.vue';
  import LibraryTableHeader from './LibraryTableHeader.vue';
  import DateTime from '../../components/DateTime.vue';
  import ModalDialog from '../../components/ModalDialog.vue';
  import ModalTransition from '../../components/ModalTransition.vue';
  import SelectField from '../../components/SelectField.vue';
  import VideoPlayer from './VideoPlayer.vue';
  import AudioPlayer from './AudioPlayer.vue';
  import ImageViewer from './ImageViewer.vue';
  import MediaNumbering from './MediaNumbering.vue';
  import DirectoryNumbering from './DirectoryNumbering.vue';
  import ScanStatus from './ScanStatus.vue';
  import { useLibraryScan } from './library-scan';
  import {
    isVideo,
    isImage,
    isAudio,
    markable,
    protectedMedia,
    isMarked,
    selectable,
    nameWithoutExtension,
    normalizedExtension
  } from './entry-types';
  import { useMediaPreview } from './media-preview';
  import { useLibrarySelection } from './selection';
  import { useInlineRename } from './inline-rename';
  import { useStickyHeader } from './sticky-header';
  import { useMarkEntries } from './mark-entries';
  const { ready } = defineProps<{ ready: boolean }>();
  type BatchAction = 'auto' | 'extract' | 'restore';
  type EntryDialogMode = 'rename' | 'auto' | 'extract' | 'restore' | 'history';
  type LoadOptions = {
    /** Reload in the background without the busy state or resetting the selection anchor. */
    quiet?: boolean;
    /** During a quiet load, keep the visible rows in place and refresh them by ID. */
    keepRows?: boolean;
    /** Checked before applying the response; false defers it behind the update notice. */
    canApply?: () => boolean;
  };
  const emit = defineEmits<{ run: [] }>();
  const toast = useToast();
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
  const batchAction = ref<BatchAction | ''>(''),
    batchError = ref(''),
    batchProgress = ref('');
  const deleting = ref<Entry>(),
    deleteError = ref(''),
    deletePath = ref('');
  const deletingBatch = ref<Entry[]>([]),
    deleteBatchPath = ref(''),
    deleteBatchError = ref(''),
    deleteBatchProgress = ref('');
  // Breadcrumb names below the library root, for confirmation dialogs.
  function directoryNames() {
    return page.value?.breadcrumbs.filter((c) => c.id !== 1).map((c) => c.name) || [];
  }
  function deleteRequest(e: Entry) {
    return api(`/entries/${e.id}`, {
      method: 'DELETE',
      body: { version: e.version, confirmed: true }
    });
  }
  function restoreRequest(e: Entry) {
    return api(`/operations/${e.restore_id}/restore`, {
      method: 'POST',
      body: { key: newRequestKey() }
    });
  }
  async function queueRun(task: 'rename' | 'extract', entries: Entry[]) {
    const extracting = task === 'extract';
    const run = await api<Run>('/runs', {
      method: 'POST',
      body: {
        task,
        targets: entries.map((e) => e.id),
        recursive: recursive.value,
        model_config_id: extracting ? 0 : modelConfigID.value,
        key: newRequestKey()
      }
    });
    trackRun(run.id, entries);
    toast.show(`${extracting ? '抽帧' : '重命名'}批次 #${run.id} 已加入队列`);
  }
  function confirmBatchDelete() {
    if (saving.value || !chosen.value.size) return;
    deletingBatch.value = [...chosen.value.values()].map((e) => ({ ...e }));
    deleteBatchPath.value = ['媒体库', ...directoryNames()].join(' / ');
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
    statusRequest++;
    clearTimeout(timer);
    deleteBatchError.value = '';
    try {
      for (const [index, e] of entries.entries()) {
        deleteBatchProgress.value = `正在删除 ${index + 1} / ${entries.length}：${e.name}`;
        try {
          await deleteRequest(e);
          chosen.value.delete(e.id);
        } catch (err) {
          failed.push(e);
          errors.push(`${e.name}：${errorText(err)}`);
        }
      }
      const summary = `已删除 ${entries.length - failed.length} 项，失败 ${failed.length} 项`;
      toast.show(summary);
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
    deletePath.value = [...directoryNames(), e.name].join('/');
  }
  async function deleteEntry() {
    if (!deleting.value || saving.value) return;
    saving.value = true;
    deleteError.value = '';
    statusRequest++;
    try {
      const e = deleting.value;
      await deleteRequest(e);
      chosen.value.delete(e.id);
      deleting.value = undefined;
      toast.show(`已永久删除 ${e.name}`);
      await load();
    } catch (err) {
      deleteError.value = errorText(err);
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
    if (kind.value === 'video') return e.kind === 'file' && isVideo(e);
    if (kind.value === 'image') return isImage(e);
    if (kind.value === 'audio') return isAudio(e);
    return e.kind === 'file' && !isVideo(e) && !isImage(e) && !isAudio(e);
  }
  function matchesProcessingStatus(e: Entry, matchingDirectories: Set<number>) {
    if (!processingStatus.value) return true;
    // Recursive directory status is evaluated by the server in the same
    // snapshot as pagination. Never reinsert a directory excluded by that query.
    if (e.kind === 'directory') return matchingDirectories.has(e.id);
    const value = e.task_status || e.processed || 'unprocessed';
    if (processingStatus.value === 'active') return isActiveRunStatus(value);
    if (processingStatus.value === 'processed')
      return ['marked', 'manual', 'automatic'].includes(value);
    return value === processingStatus.value;
  }
  function startBatch(action: BatchAction) {
    if (saving.value) return;
    batchAction.value = action;
    batchError.value = '';
    batchProgress.value = '';
    modelConfigID.value = 0;
    recursive.value = true;
  }
  async function submitBatch() {
    if (saving.value) return;
    statusRequest++;
    saving.value = true;
    batchError.value = '';
    try {
      if (batchAction.value === 'auto' || batchAction.value === 'extract') {
        const extracting = batchAction.value === 'extract';
        await queueRun(extracting ? 'extract' : 'rename', [
          ...(extracting ? extractEntries.value : autoEntries.value)
        ]);
        batchAction.value = '';
      } else {
        const entries = [...restoreEntries.value];
        let done = 0;
        const failures: string[] = [];
        for (const e of entries) {
          batchProgress.value = `正在还原 ${++done} / ${entries.length}`;
          try {
            await restoreRequest(e);
            chosen.value.delete(e.id);
          } catch (err) {
            failures.push(`${e.name}：${errorText(err)}`);
          }
        }
        toast.show(`已还原 ${entries.length - failures.length} 项，失败 ${failures.length} 项`);
        batchProgress.value = '';
        if (failures.length) batchError.value = failures.join('\n');
        else batchAction.value = '';
      }
      void syncEntries();
    } catch (err) {
      batchError.value = errorText(err);
    } finally {
      saving.value = false;
    }
  }
  const modelConfigID = ref(0);
  const sort = ref('name_asc');
  function toggleSort(field: string) {
    sort.value = sort.value === `${field}_asc` ? `${field}_desc` : `${field}_asc`;
    pageNumber.value = 1;
    void load();
  }
  const route = useRoute();
  const router = useRouter();
  function routeDirectory() {
    const value = Number(route.params.directory || 1);
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
    imageBusy,
    galleryError,
    imageZoom,
    imageTurn,
    imagePosition,
    imageStrip,
    playing,
    videoBusy,
    videoNotice,
    videoPosition,
    audioPlaying,
    audioBusy,
    audioNotice,
    audioPosition,
    audioQueue,
    closeImage,
    showImage,
    openImage,
    switchImage,
    imageKeys,
    closePlayback,
    closeAudio,
    switchVideo,
    switchAudio,
    play,
    playAudio,
    selectAudio
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
      statusRequest++;
    },
    onSaved: async () => {
      toast.show('名称已更新，历史已保存');
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
  const dialogEntry = ref<Entry>(),
    dialogMode = ref<EntryDialogMode | ''>(''),
    newName = ref(''),
    saving = ref(false),
    dialogError = ref(''),
    recursive = ref(true),
    history = ref<Operation[]>([]);
  // Request counters: a response applies only while its counter is unchanged.
  // historyRequest guards the dialog's history, statusRequest the lightweight
  // status polls, and listRequest full page loads.
  let historyRequest = 0;
  const trackedRuns = new Set<number>();
  let disposed = false,
    statusRequest = 0;
  let listRequest = 0,
    timer: ReturnType<typeof setTimeout> | undefined,
    searchTimer: ReturnType<typeof setTimeout> | undefined;
  const extension = computed(() =>
    dialogEntry.value?.kind === 'file' ? normalizedExtension(dialogEntry.value.name) : ''
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
    const listAtStart = listRequest,
      request = ++statusRequest;
    clearTimeout(timer);
    if (disposed || !active.value || busy.value || !page.value) return;
    try {
      const runs = await Promise.all([...trackedRuns].map((id) => api<Run>(`/runs/${id}`)));
      // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- disposed changes while awaiting
      if (listAtStart !== listRequest || request !== statusRequest || disposed) return;
      for (const run of runs) if (!isActiveRunStatus(run.status)) trackedRuns.delete(run.id);
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
          ...(await apiItems<Entry>(`/entries/status?ids=${ids.slice(i, i + 300).join(',')}`))
        );
      // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- the compared state can change while awaiting
      if (listAtStart !== listRequest || request !== statusRequest || disposed) return;
      // A queued/scanning run may not have populated run_items yet.
      for (const run of runs)
        if (isActiveRunStatus(run.status))
          for (const e of entries)
            if (run.targets.includes(e.id) && !e.processed && !e.task_status)
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
        await load({ quiet: true });
        return;
      }
      if (trackedRuns.size || entries.some((e) => e.task_status || e.progress?.active))
        scheduleSync();
    } catch (e) {
      // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- the compared state can change while awaiting
      if (listAtStart === listRequest && request === statusRequest && !disposed) {
        error.value = `状态更新失败：${errorText(e)}`;
        scheduleSync();
      }
    }
  }
  async function load({ quiet = false, keepRows = true, canApply }: LoadOptions = {}) {
    if (!quiet) resetSelectionAnchor();
    const request = ++listRequest;
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
      if (request !== listRequest) return;
      // Background scans must not move the rows being read or selected. Fetch by
      // ID as renamed entries can have moved outside this page's latest sort range.
      if (
        quiet &&
        keepRows &&
        page.value?.items.length &&
        (p.page ?? requestedPage) === requestedPage
      ) {
        const entries = await apiItems<Entry>(
          `/entries/status?ids=${page.value.items.map((e) => e.id).join(',')}`
        );
        if (request !== listRequest) return;
        const latest = new Map(entries.map((e) => [e.id, e]));
        const visible = new Set(page.value.items.map((e) => e.id));
        const matchingDirectories = new Set(
          p.items.filter((e) => e.kind === 'directory').map((e) => e.id)
        );
        const rows = page.value.items
          .flatMap((e) => latest.get(e.id) ?? [])
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
      await restorePosition(request, p);
      if (request !== listRequest) return;
      for (const e of p.items) if (chosen.value.has(e.id)) chosen.value.set(e.id, e);
      if (p.refreshing)
        timer = setTimeout(() => void load({ quiet: true, keepRows, canApply }), 1500);
      else if (
        trackedRuns.size ||
        p.directory.task_status ||
        p.directory.progress?.active ||
        p.items.some((e) => e.task_status || e.progress?.active)
      )
        scheduleSync();
    } catch (e) {
      if (request === listRequest) {
        error.value = errorText(e);
        if (canApply) directoryUpdateAvailable.value = true;
        if (quiet && (q.value || kind.value || processingStatus.value)) scheduleSync();
      }
    } finally {
      if (request === listRequest) busy.value = false;
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
    fileTable = ref<HTMLTableElement>(),
    headerScroll = ref<HTMLElement>();
  const {
    headerColumns,
    headerWidth,
    scheduleHeaderLayout,
    syncTableScroll,
    observeLayout,
    stopObservingLayout
  } = useStickyHeader({ fixedControls, fileTable, headerScroll });
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
  const openDialogSelector = '[role="dialog"]:not([aria-hidden="true"] *)';
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
      document.querySelector(openDialogSelector) ||
      document.activeElement?.matches('input,textarea,[contenteditable="true"]')
    );
  }
  async function pollDirectoryState(signal: AbortSignal) {
    const directory = dir.value,
      request = listRequest;
    if (!page.value || busy.value || !active.value) return;
    try {
      const state = await api<DirectoryState>(`/directories/${directory}/state`, { signal });
      // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- the compared state can change while awaiting
      if (signal.aborted || !active.value || directory !== dir.value || request !== listRequest)
        return;
      directoryPollError.value = state.error ? `目录扫描未完成：${errorText(state.error)}` : '';
      // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- the compared state can change while awaiting
      if (state.generation === page.value?.generation) directoryUpdateAvailable.value = false;
      else if (listInUse()) directoryUpdateAvailable.value = true;
      // Idle updates stay silent. Only show a notice if applying the response
      // is deferred by an interaction, or if loading the new list fails.
      else await applyDirectoryUpdate();
    } catch (e) {
      // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- the compared state can change while awaiting
      if (!signal.aborted && active.value && directory === dir.value && request === listRequest)
        directoryPollError.value = `目录更新检查失败：${errorText(e)}`;
    }
  }
  async function applyDirectoryUpdate(manual = false) {
    if (
      busy.value ||
      saving.value ||
      inlineEntry.value ||
      document.querySelector(openDialogSelector)
    )
      return;
    const directory = dir.value,
      top = window.scrollY,
      position = positionSerial;
    // A quiet load keeps filters, page number and selected IDs. Request the
    // current sorted page so new files become visible without another disk scan.
    await load({
      quiet: true,
      keepRows: false,
      canApply: () => active.value && !document.hidden && !listInUse(manual)
    });
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
    if (disposed || request !== listRequest || saved !== pendingPosition) return;
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
      // Rows whose async content (such as directory status) renders after scrolling change height;
      // re-check several times within half a second so the anchored row stays centered.
      for (const delay of [0, 60, 180, 400]) {
        setTimeout(() => {
          if (disposed || request !== listRequest || pendingPosition || !returnAnchor.value) return;
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
    const oldest = snapshots.keys().next();
    if (snapshots.size > 30 && !oldest.done) snapshots.delete(oldest.value);
    const saved = positions.get(id);
    pendingPosition = saved ? { ...saved } : undefined;
    if (!saved) window.scrollTo({ top: 0, behavior: 'instant' });
    dir.value = id;
    if (updateRoute && (routeDirectory() !== id || !route.params.directory))
      void router.push({ name: 'library', params: { directory: String(id) } });
    pageNumber.value = saved?.pageNumber || 1;
    q.value = saved?.q || '';
    kind.value = saved?.kind ?? '';
    processingStatus.value = saved?.status ?? '';
    sort.value = saved?.sort ?? sort.value;
    limit.value = saved?.limit ?? limit.value;
    page.value = snapshots.get(id);
    void load({ quiet: !!page.value });
    if (page.value) void restorePosition(listRequest, page.value);
  }
  function search() {
    pageNumber.value = 1;
    listRequest++;
    clearTimeout(timer);
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => void load(), 250);
  }
  async function refresh() {
    const directory = dir.value;
    try {
      await api<unknown>(`/directories/${directory}/refresh`, { method: 'POST', body: {} });
      if (directory === dir.value) await load({ keepRows: false });
    } catch (e) {
      if (directory === dir.value) error.value = errorText(e);
    }
  }
  async function openEntryDialog(e: Entry, nextMode: EntryDialogMode) {
    if (saving.value) return;
    const request = ++historyRequest;
    if (nextMode === 'auto') modelConfigID.value = 0;
    if (nextMode === 'extract') recursive.value = true;
    dialogEntry.value = e;
    dialogMode.value = nextMode;
    dialogError.value = '';
    newName.value = e.kind === 'file' ? nameWithoutExtension(e.name) : e.name;
    history.value = [];
    if (nextMode !== 'restore' && nextMode !== 'history') return;
    const current = () =>
      request === historyRequest && dialogMode.value === nextMode && dialogEntry.value?.id === e.id;
    try {
      const records = await apiItems<Operation>(`/entries/${e.id}/history`);
      if (current()) history.value = records;
    } catch (err) {
      if (current()) dialogError.value = errorText(err);
    }
  }
  function closeDialog() {
    historyRequest++;
    dialogMode.value = '';
  }
  async function submit() {
    if (!dialogEntry.value) return;
    statusRequest++;
    saving.value = true;
    dialogError.value = '';
    try {
      const e = dialogEntry.value;
      if (dialogMode.value === 'rename') {
        await api<unknown>(`/entries/${e.id}/rename`, {
          method: 'POST',
          body: {
            name: newName.value + extension.value,
            version: e.version,
            key: newRequestKey()
          }
        });
        toast.show('名称已更新，历史已保存');
      } else if (dialogMode.value === 'restore') {
        await restoreRequest(e);
        toast.show('名称已还原，可以再次自动命名');
      } else {
        await queueRun(dialogMode.value === 'extract' ? 'extract' : 'rename', [e]);
      }
      closeDialog();
      await syncEntries();
    } catch (err) {
      dialogError.value = errorText(err);
    } finally {
      saving.value = false;
    }
  }
  const {
    marking,
    markValue,
    markError,
    markProgress,
    markRetryUnavailable,
    openMark,
    submitMark
  } = useMarkEntries({
    saving,
    beforeSubmit: () => {
      statusRequest++;
    },
    afterSubmit: syncEntries,
    notify: toast.show,
    disposed: () => disposed
  });
  // The current-directory buttons are disabled until the page loads.
  function openDirectoryDialog(nextMode: EntryDialogMode) {
    if (page.value) void openEntryDialog(page.value.directory, nextMode);
  }
  function markDirectory(value: boolean) {
    if (page.value) openMark([page.value.directory], value);
  }
  const processedNames: Record<string, string> = {
    manual: '手动命名',
    automatic: '自动命名',
    restore: '已还原'
  };
  function status(e: Entry) {
    if (e.task_status) return statusNames[e.task_status] || e.task_status;
    if (e.processed === 'marked') return protectedMedia(e) ? '已标记 · 跳过编号' : '手动标记已处理';
    return processedNames[e.processed] ?? '未处理';
  }
  function routeChanged() {
    const id = routeDirectory();
    if (route.name === 'library' && id !== dir.value) enter(id, false);
  }
  // Kept alive while other pages show, so the watcher runs only while active.
  let stopRouteWatch: (() => void) | undefined;
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
    stopRouteWatch = watch(() => route.params.directory, routeChanged);
    void nextTick(() => {
      if (!active.value) return;
      observeLayout();
      window.scrollTo({ top: parkedScrollY, behavior: 'instant' });
      if (route.name === 'library' && routeDirectory() !== dir.value)
        enter(routeDirectory(), false);
      else void load({ quiet: !!page.value });
    });
  }
  function deactivate() {
    if (!active.value) return;
    parkedScrollY = window.scrollY;
    stopObservingLayout();
    active.value = false;
    scan.stopPolling();
    listRequest++;
    statusRequest++;
    closeDialog();
    batchAction.value = '';
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
    stopRouteWatch?.();
    stopRouteWatch = undefined;
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
  const isFiltered = computed(() => !!(q.value || kind.value || processingStatus.value));
  const emptyTitle = computed(() => {
    if (busy.value || page.value?.refreshing) return '正在读取目录';
    return isFiltered.value ? '没有匹配的条目' : '这里还没有内容';
  });
  const emptyHint = computed(() => {
    if (busy.value || page.value?.refreshing) return '首次进入时会建立当前层的目录索引。';
    return isFiltered.value
      ? '可以调整搜索、类型或处理状态筛选。'
      : '可以返回上级目录，或刷新查看最新文件。';
  });
  const entryDialogTitles: Record<string, string> = {
    extract: '目录抽帧',
    rename: '重命名',
    restore: '还原名称',
    history: '命名历史'
  };
  const entryDialogTitle = computed(() => entryDialogTitles[dialogMode.value] ?? '自动命名');
  const historySources: Record<string, string> = { restore: '还原', manual: '手动命名' };
  const historySource = (source: string) => historySources[source] ?? '自动命名';
  const entrySubmitLabels: Record<string, string> = {
    rename: '保存名称',
    restore: '确认还原',
    extract: '抽帧'
  };
  const entrySubmitLabel = computed(() => {
    if (saving.value) return '正在提交…';
    return entrySubmitLabels[dialogMode.value] ?? '重命名';
  });
  const batchCount = computed(() => {
    if (batchAction.value === 'extract') return extractEntries.value.length;
    if (batchAction.value === 'auto') return autoEntries.value.length;
    return restoreEntries.value.length;
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
            @click="openDirectoryDialog('extract')"
            ><Images :size="16" />抽帧</button
          ><button
            class="library-action action-name"
            aria-label="重命名当前目录"
            :disabled="!ready || !page || !!page.directory.task_status"
            @click="openDirectoryDialog('auto')"
            ><Sparkles :size="16" />重命名</button
          ><DirectoryNumbering
            :directory="dir"
            :disabled="busy || !page || !!page.directory.task_status"
            @notice="toast.show($event)"
            @changed="
              preview = undefined;
              clearSelection();
              syncEntries();
            "
          /><button
            class="library-action action-mark"
            title="标记当前目录及子目录中的现有媒体：视频跳过自动命名，图片和音频跳过编号"
            :disabled="!page"
            @click="markDirectory(true)"
            ><CircleCheck :size="16" />标记</button
          ><button
            class="library-action action-unmark"
            title="清除当前目录及子目录中媒体的标记，允许视频再次自动命名、图片和音频再次编号；文件名不变"
            :disabled="!page"
            @click="markDirectory(false)"
            ><CircleMinus :size="16" />取消标记</button
          ></div
        >
      </div></div
    >
    <div v-if="error" class="notice error" role="alert"
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
            label="类型筛选"
            :options="[
              { value: '', label: '全部类型' },
              { value: 'directory', label: '目录' },
              { value: 'video', label: '视频' },
              { value: 'image', label: '图片' },
              { value: 'audio', label: '音频' },
              { value: 'other', label: '其他文件' }
            ]"
            @change="
              pageNumber = 1;
              void load();
            "
          /><SelectField
            v-model="processingStatus"
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
            @change="
              pageNumber = 1;
              void load();
            "
          /><MediaNumbering
            v-if="kind === 'image' || kind === 'audio'"
            :media="kind"
            :directory="dir"
            :query="q"
            :status="processingStatus"
            :sort="sort"
            :disabled="busy || !page || !!page.refreshing || !!page.directory.task_status"
            @notice="toast.show($event)"
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
        <div v-if="page?.error" class="notice error">目录同步失败：{{ errorText(page.error) }}</div
        ><div class="pagination"
          ><span class="subtle">{{
            page?.refreshed ? `更新于 ${formatTime(page.refreshed)}` : '等待首次同步'
          }}</span
          ><SelectField
            v-model="limit"
            label="每页条目数"
            :options="[
              { value: 50, label: '50 项 / 页' },
              { value: 100, label: '100 项 / 页' },
              { value: 200, label: '200 项 / 页' }
            ]"
            @change="
              pageNumber = 1;
              void load();
            " /><PageNavigation
            :page="pageNumber"
            :total-pages="Math.max(1, Math.ceil((page?.total || 0) / limit))"
            :disabled="busy"
            @change="
              pageNumber = $event;
              void load();
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
                  title="按住 Shift 点击可连续选择当前页条目"
                  @click="selectEntry(e, $event)"
              /></td>
              <td class="filename-cell"
                ><div class="file-name"
                  ><button
                    v-if="e.kind === 'file' && isVideo(e)"
                    class="file-symbol video-symbol"
                    :aria-label="`播放视频 ${e.name}`"
                    @click="play(e)"
                    ><FileVideo :size="21" /></button
                  ><button
                    v-else-if="isImage(e)"
                    class="file-symbol image-symbol"
                    :aria-label="`预览图片 ${e.name}`"
                    @click="openImage(e)"
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
                      :size="21" /><FileVideo v-else-if="isVideo(e)" :size="21" /><File
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
              <td class="subtle mono">{{ e.kind === 'directory' ? '—' : formatSize(e.size) }}</td
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
                ><div v-if="e.kind === 'file' || e.kind === 'directory'" class="row-actions"
                  ><button
                    v-if="isImage(e)"
                    :aria-label="`查看图片 ${e.name}`"
                    title="查看图片"
                    @click="openImage(e)"
                    ><ImageIcon :size="15" /></button
                  ><button
                    v-if="e.kind === 'directory'"
                    title="重命名"
                    :aria-label="`重命名 ${e.name}`"
                    @click="openEntryDialog(e, 'rename')"
                    ><PenLine :size="15" /></button
                  ><button
                    v-if="e.kind === 'directory' || isVideo(e)"
                    class="library-action action-name"
                    :disabled="!ready || !!e.processed || !!e.task_status"
                    :aria-label="`自动命名 ${e.name}`"
                    :title="e.processed ? '已处理，自动任务将跳过' : '自动命名'"
                    @click="openEntryDialog(e, 'auto')"
                    ><Sparkles :size="15" /></button
                  ><button
                    v-if="markable(e) && (e.kind === 'directory' || !isMarked(e))"
                    class="library-action action-mark"
                    :aria-label="`标记已处理 ${e.name}`"
                    :title="isImage(e) || isAudio(e) ? '标记跳过编号' : '标记已处理'"
                    :disabled="!!e.task_status"
                    @click="openMark([e], true)"
                    ><CircleCheck :size="15" /></button
                  ><button
                    v-if="markable(e) && (e.kind === 'directory' || isMarked(e))"
                    class="library-action action-unmark"
                    :aria-label="`取消已处理标记 ${e.name}`"
                    title="取消已处理标记"
                    :disabled="!!e.task_status"
                    @click="openMark([e], false)"
                    ><CircleMinus :size="15" /></button
                  ><button
                    v-if="e.restore_id"
                    :aria-label="`还原 ${e.name}`"
                    title="还原"
                    @click="openEntryDialog(e, 'restore')"
                    ><Undo2 :size="15" /></button
                  ><button
                    title="命名历史"
                    :aria-label="`历史 ${e.name}`"
                    @click="openEntryDialog(e, 'history')"
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
          ><Folder :size="35" /><h3>{{ emptyTitle }}</h3
          ><p>{{ emptyHint }}</p></div
        ></div
      >
    </div>
    <div class="page-footnote"
      ><button class="text-button" @click="emit('run')"
        >查看批次记录 <ArrowUpRight :size="14" /></button
    ></div>
    <ModalTransition
      ><DeleteEntriesDialog
        v-if="deleting"
        :entries="[deleting]"
        :path="deletePath"
        :error="deleteError"
        :saving="saving"
        @close="deleting = undefined"
        @confirm="deleteEntry"
    /></ModalTransition>
    <ModalTransition
      ><DeleteEntriesDialog
        v-if="deletingBatch.length"
        is-batch
        :entries="deletingBatch"
        :path="deleteBatchPath"
        :progress="deleteBatchProgress"
        :error="deleteBatchError"
        :saving="saving"
        @close="deletingBatch = []"
        @confirm="deleteBatch"
    /></ModalTransition>
    <ModalTransition
      ><ModalDialog v-if="dialogMode" :title="entryDialogTitle" @close="!saving && closeDialog()"
        ><form @submit.prevent="submit">
          <div class="selection-summary"
            ><Folder v-if="dialogEntry?.kind === 'directory'" :size="23" /><FileVideo
              v-else
              :size="23"
            /><strong>{{ dialogEntry?.name || '整个媒体库' }}</strong></div
          >
          <template v-if="dialogMode === 'rename'"
            ><label
              >新名称<div class="extension-input"
                ><input v-model="newName" aria-label="新名称" required maxlength="240" /><span>{{
                  extension
                }}</span></div
              ></label
            ><p class="help">只修改名称，文件保留在原目录。成功后可以还原。</p></template
          >
          <template v-else-if="dialogMode === 'restore'"
            ><p>将恢复到最近一次改名前的名称：</p
            ><div class="restore-target"
              ><Undo2 :size="18" />{{
                history.find((x) => x.id === dialogEntry?.restore_id)?.old_name || '正在读取…'
              }}</div
            ><p class="help"
              >还原后可再次自动命名，也会被后续计划重新处理。不会覆盖同名文件。</p
            ></template
          >
          <template v-else-if="dialogMode === 'auto' || dialogMode === 'extract'"
            ><ModelPicker v-if="dialogMode === 'auto'" v-model="modelConfigID" /><p>{{
              dialogMode === 'extract'
                ? '为当前目录内的视频预先生成截图，之后重命名可直接复用缓存。'
                : '后台将抽取视频画面，由指定模型生成名称并自动修改。'
            }}</p
            ><label v-if="dialogEntry?.kind === 'directory'" class="check-label"
              ><input v-model="recursive" type="checkbox" role="switch" />包含子目录</label
            ><p class="help">{{
              dialogMode === 'extract'
                ? '覆盖目录内全部分页的视频，包括已处理视频；已有有效截图直接复用。进度见“批次记录”，结果见“抽帧记录”。'
                : '已命名的视频自动跳过。进度见“批次记录”，命名结果见“运行记录”。'
            }}</p></template
          >
          <template v-else
            ><div v-for="h in history" :key="h.id" class="history-item"
              ><span class="badge">{{ historySource(h.source) }} · {{ h.status }}</span
              ><p>{{ h.old_name }} <ArrowRight :size="14" /> {{ h.new_name }}</p
              ><DateTime :value="h.created" /></div
            ><p v-if="!history.length" class="empty compact">暂无命名历史</p></template
          >
          <div v-if="dialogError" class="notice error" role="alert">{{ dialogError }}</div
          ><div class="modal-actions"
            ><button type="button" :disabled="saving" @click="closeDialog()">{{
              dialogMode === 'history' ? '关闭' : '取消'
            }}</button
            ><button
              v-if="dialogMode !== 'history'"
              class="primary"
              :disabled="
                saving ||
                (dialogMode === 'restore' && !history.length) ||
                (dialogMode === 'auto' && !modelConfigID)
              "
              >{{ entrySubmitLabel }}</button
            ></div
          ></form
        ></ModalDialog
      ></ModalTransition
    >
    <ModalTransition
      ><BatchActionDialog
        v-if="batchAction"
        v-model:model-config-id="modelConfigID"
        v-model:recursive="recursive"
        :action="batchAction"
        :count="batchCount"
        :saving="saving"
        :progress="batchProgress"
        :error="batchError"
        @close="batchAction = ''"
        @submit="submitBatch"
    /></ModalTransition>
    <ModalTransition
      ><MarkEntriesDialog
        v-if="marking.length"
        :entries="marking"
        :value="markValue"
        :saving="saving"
        :progress="markProgress"
        :error="markError"
        :is-retry-unavailable="markRetryUnavailable"
        @close="marking = []"
        @submit="submitMark"
    /></ModalTransition>
    <ModalTransition
      ><ModalDialog v-if="playing" :title="playing.name" theater @close="closePlayback"
        ><template #header
          ><span v-if="videoPosition" class="theater-count"
            >第 {{ videoPosition }} 个</span
          ></template
        ><VideoPlayer
          :key="playing.id"
          :src="`/api/entries/${playing.id}/video`"
          :switching="videoBusy"
          :notice="videoNotice"
          @previous="switchVideo(true)"
          @next="switchVideo(false)" /></ModalDialog
    ></ModalTransition>
    <ModalTransition
      ><ModalDialog
        v-if="audioPlaying"
        :title="audioPlaying.name"
        class="audio-dialog"
        @close="closeAudio()"
        ><AudioPlayer
          :key="audioPlaying.id"
          :entry="audioPlaying"
          :switching="audioBusy"
          :notice="audioNotice"
          :position="audioPosition"
          :queue="audioQueue"
          @previous="switchAudio(true)"
          @next="switchAudio(false)"
          @select="selectAudio" /></ModalDialog
    ></ModalTransition>
    <ModalTransition
      ><ModalDialog v-if="preview" :title="preview.name" theater @close="closeImage"
        ><template #header
          ><span v-if="imagePosition" class="theater-count">第 {{ imagePosition }} 张</span
          ><span class="theater-tools" role="group" aria-label="图片查看"
            ><button
              aria-label="缩小"
              title="缩小"
              :disabled="imageZoom <= 0.5"
              @click="imageZoom = Math.max(0.5, imageZoom - 0.5)"
              ><ZoomOut :size="18" /></button
            ><span class="theater-zoom">{{ Math.round(imageZoom * 100) }}%</span
            ><button
              aria-label="放大"
              title="放大"
              :disabled="imageZoom >= 4"
              @click="imageZoom = Math.min(4, imageZoom + 0.5)"
              ><ZoomIn :size="18" /></button
            ><button
              aria-label="适应窗口"
              title="适应窗口"
              :disabled="imageZoom === 1 && !imageTurn"
              @click="
                imageZoom = 1;
                imageTurn = 0;
              "
              ><Scan :size="18" /></button
            ><button aria-label="旋转" title="旋转" @click="imageTurn = (imageTurn + 90) % 360"
              ><RotateCw :size="18" /></button></span></template
        ><ImageViewer
          :entry="preview"
          :strip="imageStrip"
          :busy="imageBusy"
          :zoom="imageZoom"
          :turn="imageTurn"
          :notice="galleryError"
          @previous="switchImage(true)"
          @next="switchImage(false)"
          @select="showImage"
          @zoom="imageZoom = $event" /></ModalDialog
    ></ModalTransition>
  </section>
</template>

<style scoped>
  /* Header extras for the theater viewers: the "3 / 12" counter and the image tools. */
  .theater-count {
    flex-shrink: 0;
    padding: 2px 9px;
    border: 1px solid var(--stage-line);
    border-radius: 999px;
    font-size: 12px;
    color: var(--stage-dim);
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }
  .theater-tools {
    display: flex;
    align-items: center;
    gap: 2px;
    margin-left: auto;
  }
  .theater-tools button {
    padding: 7px;
    border: 0;
    border-radius: 8px;
    background: transparent;
    color: var(--stage-ink);
  }
  .theater-tools button:hover:not(:disabled) {
    background: var(--stage-hover);
    border: 0;
  }
  .theater-zoom {
    min-width: 44px;
    text-align: center;
    font-size: 12px;
    color: var(--stage-dim);
    font-variant-numeric: tabular-nums;
  }
  @media (max-width: 600px) {
    .theater-zoom {
      display: none;
    }
  }
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
    border: 1px solid var(--palette-e8d7a6);
    border-radius: 12px;
    background: var(--palette-fffbef);
    color: var(--palette-73551b);
    box-shadow: 0 8px 30px var(--palette-49340b1a);
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
  /* Amber member of the shared tinted button family, matching the notice it sits in. */
  .directory-update-content button {
    border-color: var(--tone-amber-line);
    background: var(--tone-amber-bg);
    color: var(--tone-amber-ink);
    font-weight: 600;
  }
  .directory-update-content button:hover:not(:disabled) {
    border-color: var(--tone-amber-line);
    background: var(--tone-amber-bg);
    filter: saturate(1.35) brightness(0.97);
  }
  .directory-update-content button:focus-visible {
    outline-color: var(--tone-amber-ink);
  }
  .library-fixed-controls {
    position: sticky;
    top: 0;
    z-index: 6;
    background: var(--palette-ffffff);
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
    background: var(--palette-fafbfc);
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
    background: var(--palette-e3f2e9);
    box-shadow: inset 3px 0 var(--palette-398366);
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
  .delete-action {
    color: var(--tone-red-ink);
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
    outline: 2px solid var(--palette-89bca7);
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
    border: 1px solid var(--palette-89bca7);
    border-radius: 6px;
    background: var(--palette-ffffff);
    box-shadow: 0 0 0 2px var(--palette-e3f2e9);
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
    background: var(--palette-f1f7f3);
  }
  .rename-extension:focus-visible {
    outline: 2px solid var(--palette-89bca7);
    outline-offset: -2px;
  }
  .inline-rename small {
    display: block;
    margin-top: 5px;
    color: var(--palette-b44444);
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
