<script setup lang="ts">
  import {
    useRouteState,
    queryText,
    queryChoice,
    queryInteger,
    queryIDs,
    queryBoolean
  } from '../../app/use-route-state';
  import './library.css';
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
    Info,
    ScanLine,
    Search,
    ArrowUpRight,
    PenLine,
    Undo2,
    Sparkles,
    Home,
    History,
    Link,
    Image as ImageIcon,
    CircleCheck,
    CircleMinus,
    Images,
    Trash2
  } from '@lucide/vue';
  import './library-actions.css';
  import './theater.css';
  import { useApi } from '../../app/use-api';
  import { formatTime } from '../../lib/format/date';
  import { formatSize } from '../../lib/format/bytes';
  import {
    isActiveRunStatus,
    type Entry,
    type Page,
    type DirectoryState,
    type Run
  } from '../../app/contracts';
  import { statusNames } from '../../app/labels';
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
  import EntryDialog from './EntryDialog.vue';
  import ImagePreviewDialog from './ImagePreviewDialog.vue';
  import MediaNumbering from './MediaNumbering.vue';
  import DirectoryActions from './DirectoryActions.vue';
  import ScanStatus from './ScanStatus.vue';
  import { useLibraryScan } from './library-scan';
  import {
    isVideo,
    isImage,
    isAudio,
    markable,
    protectedMedia,
    isMarked,
    selectable
  } from './entry-types';
  import { useMediaPreview } from './media-preview';
  import { useLibrarySelection } from './selection';
  import { useInlineRename } from './inline-rename';
  import { useStickyHeader } from './sticky-header';
  import { useMarkEntries } from './mark-entries';
  import { useDeleteEntries } from './delete-entries';
  import { useBatchActions } from './batch-actions';
  import { useEntryDialog, type EntryDialogMode, type RunTask } from './entry-dialog';
  const { api, apiItems } = useApi();
  const { ready } = defineProps<{ ready: boolean }>();
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
  // Breadcrumb names below the library root, for confirmation dialogs.
  function directoryNames() {
    return page.value?.breadcrumbs.filter((c) => c.id !== 1).map((c) => c.name) || [];
  }
  async function queueRun(task: RunTask, entries: Entry[]) {
    const extracting = task === 'extract';
    const run = await api<Run>('/runs', {
      method: 'POST',
      body: {
        task,
        targets: entries.map((e) => e.id),
        recursive: recursive.value,
        model_config_id: extracting ? 0 : modelConfigID.value
      }
    });
    trackRun(run.id, entries);
    toast.show(`${extracting ? '抽帧' : '重命名'}批次 #${run.id} 已加入队列`);
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
  const pendingSelection = ref(new Set<number>());
  const selectedIDs = computed({
    get: () => new Set([...pendingSelection.value, ...chosen.value.keys()]),
    set: (ids: Set<number>) => {
      pendingSelection.value = new Set([...ids].filter((id) => !chosen.value.has(id)));
      chosen.value = new Map([...chosen.value].filter(([id]) => ids.has(id)));
    }
  });
  const routeState = useRouteState(
    {
      q: queryText(q),
      kind: queryChoice(kind, ['', 'file', 'directory', 'video', 'audio', 'image', 'other']),
      status: queryChoice(processingStatus, [
        '',
        'unprocessed',
        'processed',
        'manual',
        'automatic',
        'marked',
        'active',
        'queued',
        'scanning',
        'running',
        'paused',
        'cancelling'
      ]),
      sort: queryChoice(sort, [
        'name_asc',
        'name_desc',
        'size_asc',
        'size_desc',
        'mtime_asc',
        'mtime_desc',
        'renamed_asc',
        'renamed_desc',
        'status_asc',
        'status_desc'
      ]),
      page: queryInteger(pageNumber, { min: 1 }),
      limit: queryInteger(limit, { choices: [50, 100, 200] }),
      selected: queryIDs(selectedIDs),
      multi: queryBoolean(multi)
    },
    {
      canWrite: () => routeDirectory() === dir.value,
      onRestore: () => {
        void load({ quiet: true });
      }
    }
  );
  async function restoreSelection(signal: AbortSignal) {
    const ids = [...pendingSelection.value];
    if (!ids.length) return;
    const restored: Entry[] = [];
    // The status endpoint accepts at most 300 IDs per request.
    const chunkSize = 200;
    for (let offset = 0; offset < ids.length; offset += chunkSize) {
      const entries = await apiItems<Entry>(
        `/entries/status?ids=${ids.slice(offset, offset + chunkSize).join(',')}`,
        { signal }
      );
      restored.push(...entries);
    }
    if (signal.aborted) return;
    for (const entry of restored)
      if (pendingSelection.value.has(entry.id)) chosen.value.set(entry.id, entry);
    for (const id of ids) pendingSelection.value.delete(id);
    if (chosen.value.size) multi.value = true;
  }
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
  const saving = ref(false),
    recursive = ref(true);
  // Request counters: a response applies only while its counter is unchanged.
  // statusRequest guards the lightweight status polls, listRequest full page loads.
  const trackedRuns = new Set<number>();
  let disposed = false,
    statusRequest = 0;
  let listRequest = 0,
    timer: ReturnType<typeof setTimeout> | undefined,
    searchTimer: ReturnType<typeof setTimeout> | undefined;
  let listController: AbortController | undefined, statusController: AbortController | undefined;
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
    statusController?.abort();
    const controller = new AbortController();
    statusController = controller;
    const listAtStart = listRequest,
      request = ++statusRequest;
    clearTimeout(timer);
    if (disposed || !active.value || busy.value || !page.value) return;
    try {
      const runs = await Promise.all(
        [...trackedRuns].map((id) => api<Run>(`/runs/${id}`, { signal: controller.signal }))
      );
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
          ...(await apiItems<Entry>(`/entries/status?ids=${ids.slice(i, i + 300).join(',')}`, {
            signal: controller.signal
          }))
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
    listController?.abort();
    statusController?.abort();
    const controller = new AbortController();
    listController = controller;
    if (!quiet) resetSelectionAnchor();
    const request = ++listRequest;
    clearTimeout(timer);
    busy.value = !quiet;
    error.value = '';
    try {
      const requestedPage = pageNumber.value;
      const params = new URLSearchParams({
        offset: String((requestedPage - 1) * limit.value),
        limit: String(limit.value),
        q: q.value,
        kind: kind.value,
        status: processingStatus.value,
        sort: sort.value
      });
      const p = await api<Page>(`/directories/${dir.value}/entries?${params}`, {
        signal: controller.signal
      });
      if (request !== listRequest) return;
      // Background scans must not move the rows being read or selected. Fetch by
      // ID as renamed entries can have moved outside this page's latest sort range.
      if (
        quiet &&
        keepRows &&
        page.value?.items.length &&
        p.offset === (requestedPage - 1) * p.limit
      ) {
        const entries = await apiItems<Entry>(
          `/entries/status?ids=${page.value.items.map((e) => e.id).join(',')}`,
          { signal: controller.signal }
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
      pageNumber.value = Math.floor(p.offset / p.limit) + 1;
      page.value = p;
      // This response applies the latest list, which may already be newer than
      // the last lightweight state poll. Do not keep an outdated update notice.
      directoryUpdateAvailable.value = false;
      if (inlineEntry.value && !p.items.some((e) => e.id === inlineEntry.value?.id))
        cancelInlineRename();
      await restorePosition(request, p);
      if (request !== listRequest) return;
      for (const e of p.items) if (chosen.value.has(e.id)) chosen.value.set(e.id, e);
      await restoreSelection(controller.signal);
      if (request !== listRequest) return;
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
    pendingSelection.value.clear();
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
    pageNumber.value = saved?.pageNumber || 1;
    q.value = saved?.q || '';
    kind.value = saved?.kind ?? '';
    processingStatus.value = saved?.status ?? '';
    sort.value = saved?.sort ?? sort.value;
    limit.value = saved?.limit ?? limit.value;
    if (updateRoute && (routeDirectory() !== id || !route.params.directory))
      void router.push({
        name: 'library',
        params: { directory: String(id) },
        query: routeState.query()
      });
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
      toast.show('目录刷新已提交');
      if (directory === dir.value) await load({ keepRows: false });
    } catch (e) {
      if (directory === dir.value) error.value = errorText(e);
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
  const invalidateStatus = () => {
    statusRequest++;
  };
  const {
    dialogEntry,
    dialogMode,
    newName,
    dialogError,
    formError: entryFormError,
    history,
    hasMoreHistory,
    loadingHistory,
    loadHistory,
    extension,
    openEntryDialog,
    closeDialog,
    submit
  } = useEntryDialog({
    saving,
    modelConfigID,
    recursive,
    invalidateStatus,
    queueRun,
    notify: toast.show,
    afterSubmit: syncEntries
  });
  const { batchAction, batchError, batchProgress, batchCount, startBatch, submitBatch } =
    useBatchActions({
      saving,
      modelConfigID,
      recursive,
      chosen,
      autoEntries,
      extractEntries,
      restoreEntries,
      invalidateStatus,
      queueRun,
      notify: toast.show,
      afterSubmit: syncEntries
    });
  const {
    deleting,
    deleteError,
    deletePath,
    deletingBatch,
    deleteBatchPath,
    deleteBatchError,
    deleteBatchProgress,
    confirmDelete,
    deleteEntry,
    confirmBatchDelete,
    deleteBatch,
    closeDeletes
  } = useDeleteEntries({
    saving,
    chosen,
    directoryNames,
    invalidateStatus,
    pauseSync: () => {
      clearTimeout(timer);
    },
    directory: () => dir.value,
    reload: () => load(),
    keepFailures: () => active.value,
    resetSelectionAnchor,
    notify: toast.show,
    disposed: () => disposed
  });
  // The current-directory buttons are disabled until the page loads.
  function openDirectoryDialog(nextMode: EntryDialogMode) {
    if (page.value) void openEntryDialog(page.value.directory, nextMode);
  }
  // Numbered names replace the visible ones; drop the preview and the stale selection.
  function numbered() {
    preview.value = undefined;
    clearSelection();
    void syncEntries();
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
    closeDeletes();
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
</script>
<template>
  <section ref="listRoot" class="library-page">
    <div class="page-heading library-heading"
      ><h1>文件目录<span class="title-dot">.</span></h1
      ><DirectoryActions
        :directory="dir"
        :loaded="!!page"
        :running="!!page?.directory.task_status"
        :busy="busy"
        :ready="ready"
        @extract="openDirectoryDialog('extract')"
        @rename="openDirectoryDialog('auto')"
        @mark="markDirectory"
        @notice="toast.show($event)"
        @numbered="numbered"
    /></div>
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
        aria-label="目录有更新"
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
        ><div class="breadcrumbs" role="navigation" aria-label="目录路径"
          ><button @click="enter(1)"><Home :size="15" />媒体库</button
          ><template v-for="crumb in page?.breadcrumbs.filter((x) => x.id !== 1)" :key="crumb.id"
            ><ChevronRight :size="13" /><button @click="enter(crumb.id)">{{
              crumb.name
            }}</button></template
          ></div
        ><div class="directory-summary" role="group" aria-label="当前目录概况"
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
        <div v-if="multi" class="batch-toolbar" role="group" aria-label="批量操作"
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
              <td class="filename-cell" role="rowheader"
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
                        :aria-invalid="inlineError ? true : undefined"
                        :aria-describedby="inlineError ? 'inline-rename-error' : undefined"
                        :readonly="inlineSaving"
                        maxlength="240"
                        @input="inlineError = ''"
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
                    ><small v-if="inlineError" id="inline-rename-error" role="alert">{{
                      inlineError
                    }}</small></div
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
              <td class="subtle mono">{{
                e.kind === 'directory' ? '—' : formatSize(e.size_bytes)
              }}</td
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
      ><EntryDialog
        v-if="dialogMode && dialogEntry"
        v-model:new-name="newName"
        v-model:model-config-id="modelConfigID"
        v-model:recursive="recursive"
        :mode="dialogMode"
        :entry="dialogEntry"
        :history="history"
        :has-more-history="hasMoreHistory"
        :loading-history="loadingHistory"
        :extension="extension"
        :error="dialogError"
        :form-error="entryFormError"
        :saving="saving"
        @load-more="loadHistory()"
        @close="closeDialog()"
        @submit="submit"
    /></ModalTransition>
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
      ><ImagePreviewDialog
        v-if="preview"
        v-model:zoom="imageZoom"
        v-model:turn="imageTurn"
        :entry="preview"
        :strip="imageStrip"
        :busy="imageBusy"
        :notice="galleryError"
        :position="imagePosition"
        @close="closeImage"
        @previous="switchImage(true)"
        @next="switchImage(false)"
        @select="showImage"
    /></ModalTransition>
  </section>
</template>

<style scoped>
  .page-footnote {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding-top: var(--space-17);
    font-size: var(--font-size-10);
    color: var(--palette-93979c);
  }
  .page-footnote button {
    font-size: var(--font-size-10);
  }
  @media (max-width: 800px) {
    .page-footnote {
      gap: var(--space-10);
    }
  }

  .library-scan-status {
    margin-bottom: var(--space-12);
  }
  .directory-update-notice {
    position: fixed;
    right: 24px;
    bottom: 24px;
    z-index: 90;
    display: flex;
    align-items: flex-start;
    gap: var(--space-12);
    width: 380px;
    max-width: calc(100vw - 32px);
    max-height: calc(100dvh - 32px);
    overflow-y: auto;
    padding: var(--space-16);
    border: 1px solid var(--palette-e8d7a6);
    border-radius: var(--radius-12);
    background: var(--palette-fffbef);
    color: var(--palette-73551b);
    box-shadow: var(--shadow-12);
    font-size: var(--font-size-13);
  }
  .directory-update-icon {
    flex-shrink: 0;
    margin-top: var(--space-2);
  }
  .directory-update-content {
    flex: 1;
    min-width: 0;
  }
  .directory-update-content strong {
    font-size: var(--font-size-14);
  }
  .directory-update-content p {
    margin: var(--space-4) 0 var(--space-12);
    line-height: var(--line-height-1-6);
    overflow-wrap: anywhere;
  }
  /* Amber member of the shared tinted button family, matching the notice it sits in. */
  .directory-update-content button {
    border-color: var(--tone-amber-line);
    background: var(--tone-amber-bg);
    color: var(--tone-amber-ink);
    font-weight: var(--font-weight-600);
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
    gap: var(--space-16);
    margin-bottom: var(--space-24);
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
    border-radius: var(--radius-12) var(--radius-12) 0 0;
  }
  .library-panel-controls {
    border-top: 0;
    border-radius: 0;
    overflow: visible;
  }
  .library-file-rows {
    border-top: 0;
    border-radius: 0 0 var(--radius-12) var(--radius-12);
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
      margin-bottom: var(--space-12);
      gap: var(--space-8);
    }
    .library-directory-overview .panel-toolbar {
      padding: var(--space-10) var(--space-12);
      gap: var(--space-8);
    }
    .library-panel-controls .filterbar {
      padding: var(--space-8) var(--space-12);
      gap: var(--space-8);
    }
    .library-panel-controls .batch-toolbar {
      flex-wrap: nowrap;
      overflow-x: auto;
      padding: var(--space-8) var(--space-12);
    }
    .library-panel-controls .batch-toolbar > * {
      flex-shrink: 0;
      white-space: nowrap;
    }
    .library-panel-controls .pagination {
      padding: var(--space-8) var(--space-12);
      gap: var(--space-8);
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
      font-size: var(--font-size-22);
      margin-bottom: 0;
    }
    .library-directory-overview .directory-toolbar {
      position: relative;
    }
    .library-directory-overview .breadcrumbs {
      padding-right: var(--space-42);
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
      padding: var(--space-8);
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
      font-size: var(--font-size-10);
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
    box-shadow: var(--shadow-13);
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
    gap: var(--space-10);
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
    font-weight: var(--font-weight-500);
    cursor: text;
  }
  .inline-rename-trigger:focus-visible {
    outline: 2px solid var(--palette-89bca7);
    outline-offset: 3px;
    border-radius: var(--radius-2);
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
    border-radius: var(--radius-6);
    background: var(--palette-ffffff);
    box-shadow: var(--shadow-14);
  }
  .inline-rename-field input {
    min-width: 0;
    width: 100%;
    padding: var(--space-6) var(--space-8);
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
    padding: var(--space-6) var(--space-8);
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
    margin-top: var(--space-5);
    color: var(--palette-b44444);
    overflow-wrap: anywhere;
  }
  .file-table {
    min-width: 1120px;
  }
  .file-table .contents-cell {
    min-width: 76px;
    white-space: nowrap;
    font-size: var(--font-size-11);
  }
  .directory-summary > button + button {
    margin-left: 0;
  }
</style>
