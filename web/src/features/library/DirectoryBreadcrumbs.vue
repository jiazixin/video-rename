<script setup lang="ts">
  import { computed, nextTick, onMounted, onUnmounted, ref, useId } from 'vue';
  import { Check, ChevronDown, ChevronRight, FolderRoot, HardDrive, Home } from '@lucide/vue';
  import type { Page } from '../../app/contracts';

  // Show direct one-click tabs for common multi-root setups; switch to a compact dropdown when
  // there are more roots so the toolbar stays tidy on narrow screens and inside modals.
  const INLINE_ROOT_LIMIT = 5;

  const { page } = defineProps<{ page: Page | undefined }>();
  const emit = defineEmits<{ enter: [id: number] }>();

  const mediaRoots = computed(() => page?.media_roots ?? []);
  const hasMultipleRoots = computed(() => mediaRoots.value.length > 1);
  const useInlineRoots = computed(
    () => hasMultipleRoots.value && mediaRoots.value.length <= INLINE_ROOT_LIMIT
  );
  const currentRootId = computed(() => page?.breadcrumbs[0]?.id ?? mediaRoots.value[0]?.id ?? 1);
  const currentDirId = computed(() => page?.directory.id ?? currentRootId.value);
  const subCrumbs = computed(() => page?.breadcrumbs.slice(1) ?? []);
  const currentRootIndex = computed(() => {
    const index = mediaRoots.value.findIndex((root) => root.id === currentRootId.value);
    return index >= 0 ? index : 0;
  });
  const currentRoot = computed(
    () => mediaRoots.value[currentRootIndex.value] ?? mediaRoots.value[0]
  );

  function rootTitle(root: { id: number; name: string }) {
    if (root.id === currentRootId.value && subCrumbs.value.length > 0) {
      return `返回 ${root.name} 根目录`;
    }
    return root.name;
  }

  function enterRoot(id: number) {
    if (id !== currentDirId.value) emit('enter', id);
  }

  // Dropdown state when mediaRoots.length > INLINE_ROOT_LIMIT.
  const dropdownRoot = ref<HTMLElement>();
  const trigger = ref<HTMLButtonElement>();
  const menu = ref<HTMLElement>();
  const menuOpen = ref(false);
  const activeIndex = ref(0);
  const listboxId = useId();
  const menuPosition = ref({ left: '0px', top: '0px', minWidth: '0px', maxHeight: '280px' });

  function closeMenu() {
    menuOpen.value = false;
  }

  function placeMenu() {
    if (!trigger.value) return;
    const rect = trigger.value.getBoundingClientRect();
    const roomBelow = window.innerHeight - rect.bottom - 12;
    const estimatedHeight = Math.min(280, mediaRoots.value.length * 38 + 44);
    const minWidth = Math.min(Math.max(rect.width, 220), window.innerWidth - 16);
    const left = Math.max(8, Math.min(rect.left, window.innerWidth - minWidth - 8));
    const top =
      roomBelow >= estimatedHeight ? rect.bottom + 6 : Math.max(8, rect.top - estimatedHeight - 6);
    menuPosition.value = {
      left: `${left}px`,
      top: `${top}px`,
      minWidth: `${minWidth}px`,
      maxHeight: `${Math.min(estimatedHeight, window.innerHeight - 16)}px`
    };
  }

  function revealActiveOption() {
    const option = document.getElementById(`${listboxId}-${activeIndex.value}`);
    const list = menu.value;
    if (!option || !list) return;
    if (option.offsetTop < list.scrollTop) list.scrollTop = option.offsetTop;
    else if (option.offsetTop + option.offsetHeight > list.scrollTop + list.clientHeight) {
      list.scrollTop = option.offsetTop + option.offsetHeight - list.clientHeight;
    }
  }

  async function toggleMenu() {
    if (menuOpen.value) {
      closeMenu();
      return;
    }
    placeMenu();
    activeIndex.value = currentRootIndex.value;
    menuOpen.value = true;
    await nextTick();
    revealActiveOption();
  }

  function chooseRoot(index: number) {
    const target = mediaRoots.value[index];
    if (!target) return;
    closeMenu();
    trigger.value?.focus();
    enterRoot(target.id);
  }

  async function onDropdownKeydown(event: KeyboardEvent) {
    const count = mediaRoots.value.length;
    if (!count) return;
    if (event.key === 'Escape' && menuOpen.value) {
      event.preventDefault();
      event.stopPropagation();
      closeMenu();
      return;
    }
    if (event.key === 'Tab') {
      closeMenu();
      return;
    }
    if (['ArrowDown', 'ArrowUp', 'Home', 'End', 'Enter', ' '].includes(event.key)) {
      event.preventDefault();
      if (!menuOpen.value) {
        await toggleMenu();
        return;
      }
      if (event.key === 'Enter' || event.key === ' ') {
        chooseRoot(activeIndex.value);
        return;
      }
      if (event.key === 'Home') activeIndex.value = 0;
      else if (event.key === 'End') activeIndex.value = count - 1;
      else
        activeIndex.value =
          (activeIndex.value + (event.key === 'ArrowDown' ? 1 : -1) + count) % count;
      await nextTick();
      revealActiveOption();
      return;
    }
    if (event.key.length === 1) {
      const match = mediaRoots.value.findIndex((root) =>
        root.name.toLowerCase().startsWith(event.key.toLowerCase())
      );
      if (match >= 0) {
        if (!menuOpen.value) await toggleMenu();
        activeIndex.value = match;
        await nextTick();
        revealActiveOption();
      }
    }
  }

  function onOutsidePointerDown(event: PointerEvent) {
    const target = event.target as Node | null;
    if (menuOpen.value && !dropdownRoot.value?.contains(target) && !menu.value?.contains(target)) {
      closeMenu();
    }
  }

  function onWindowResize() {
    if (menuOpen.value) placeMenu();
  }

  function onDocumentScroll(event: Event) {
    if (
      menuOpen.value &&
      !(event.target instanceof Element && event.target.closest('.root-dropdown-menu'))
    ) {
      placeMenu();
    }
  }

  onMounted(() => {
    document.addEventListener('pointerdown', onOutsidePointerDown);
    window.addEventListener('resize', onWindowResize);
    document.addEventListener('scroll', onDocumentScroll, true);
  });

  onUnmounted(() => {
    document.removeEventListener('pointerdown', onOutsidePointerDown);
    window.removeEventListener('resize', onWindowResize);
    document.removeEventListener('scroll', onDocumentScroll, true);
  });
</script>
<template>
  <nav aria-label="目录路径" class="directory-breadcrumbs">
    <template v-if="hasMultipleRoots">
      <div class="root-switcher">
        <span class="root-prefix" aria-hidden="true">
          <HardDrive :size="14" />
          <span>根目录</span>
        </span>
        <div v-if="useInlineRoots" class="root-tabs" role="group" aria-label="切换媒体根目录">
          <button
            v-for="root in mediaRoots"
            :key="root.id"
            type="button"
            class="root-tab"
            :class="{ active: root.id === currentRootId }"
            :aria-pressed="root.id === currentRootId"
            :title="rootTitle(root)"
            @click="enterRoot(root.id)"
          >
            <FolderRoot :size="14" class="root-tab-icon" aria-hidden="true" />
            <span class="root-tab-name">{{ root.name }}</span>
          </button>
        </div>
        <div v-else ref="dropdownRoot" class="root-dropdown">
          <button
            ref="trigger"
            type="button"
            class="root-dropdown-trigger"
            role="combobox"
            aria-label="切换媒体根目录"
            aria-haspopup="listbox"
            :aria-expanded="menuOpen"
            :aria-controls="menuOpen ? listboxId : undefined"
            :aria-activedescendant="menuOpen ? `${listboxId}-${activeIndex}` : undefined"
            :title="currentRoot ? rootTitle(currentRoot) : undefined"
            @click="toggleMenu"
            @keydown="onDropdownKeydown"
          >
            <FolderRoot :size="14" class="root-trigger-icon" aria-hidden="true" />
            <span class="root-trigger-name">{{ currentRoot?.name || '媒体库' }}</span>
            <span class="root-trigger-count" aria-hidden="true"
              >{{ currentRootIndex + 1 }}/{{ mediaRoots.length }}</span
            >
            <ChevronDown
              :size="14"
              class="root-trigger-chevron"
              :class="{ rotated: menuOpen }"
              aria-hidden="true"
            />
          </button>
          <Teleport to="body">
            <Transition name="popover">
              <div
                v-if="menuOpen"
                :id="listboxId"
                ref="menu"
                role="listbox"
                aria-label="媒体目录列表"
                class="root-dropdown-menu"
                :style="menuPosition"
              >
                <div class="root-menu-header" aria-hidden="true"
                  >媒体根目录（{{ mediaRoots.length }}）</div
                >
                <div
                  v-for="(root, index) in mediaRoots"
                  :id="`${listboxId}-${index}`"
                  :key="root.id"
                  role="option"
                  :aria-selected="root.id === currentRootId"
                  class="root-menu-option"
                  :class="{
                    highlighted: index === activeIndex,
                    selected: root.id === currentRootId
                  }"
                  @pointermove="activeIndex = index"
                  @mousedown.prevent
                  @click.prevent.stop="chooseRoot(index)"
                >
                  <FolderRoot :size="14" class="root-option-icon" aria-hidden="true" />
                  <span class="root-option-name" :title="root.name">{{ root.name }}</span>
                  <Check
                    v-if="root.id === currentRootId"
                    :size="14"
                    class="root-option-check"
                    aria-hidden="true"
                  />
                </div>
              </div>
            </Transition>
          </Teleport>
        </div>
      </div>
      <span v-if="subCrumbs.length" class="crumb-trail">
        <template v-for="(crumb, index) in subCrumbs" :key="crumb.id">
          <ChevronRight :size="14" class="crumb-sep" aria-hidden="true" />
          <button
            type="button"
            class="crumb-link"
            :class="{ current: index === subCrumbs.length - 1 }"
            :aria-current="index === subCrumbs.length - 1 ? 'location' : undefined"
            :title="crumb.name"
            @click="emit('enter', crumb.id)"
          >
            {{ crumb.name }}
          </button>
        </template>
      </span>
    </template>
    <template v-else>
      <button
        type="button"
        class="crumb-link"
        :class="{ current: !subCrumbs.length }"
        @click="emit('enter', currentRootId)"
      >
        <Home :size="15" aria-hidden="true" /><span>媒体库</span>
      </button>
      <template v-for="(crumb, index) in subCrumbs" :key="crumb.id">
        <ChevronRight :size="13" class="crumb-sep" aria-hidden="true" />
        <button
          type="button"
          class="crumb-link"
          :class="{ current: index === subCrumbs.length - 1 }"
          :aria-current="index === subCrumbs.length - 1 ? 'location' : undefined"
          @click="emit('enter', crumb.id)"
        >
          {{ crumb.name }}
        </button>
      </template>
    </template>
  </nav>
</template>

<style scoped>
  .directory-breadcrumbs {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    flex-wrap: wrap;
    min-width: 0;
  }
  .root-switcher {
    display: inline-flex;
    align-items: center;
    gap: var(--space-8);
    min-width: 0;
    max-width: 100%;
    flex-wrap: wrap;
  }
  .root-prefix {
    display: inline-flex;
    align-items: center;
    gap: var(--space-5);
    color: var(--steel);
    font-size: var(--font-size-12);
    font-weight: var(--font-weight-500);
    white-space: nowrap;
    flex-shrink: 0;
  }
  .root-tabs {
    display: inline-flex;
    align-items: center;
    flex-wrap: wrap;
    gap: var(--space-4);
    padding: var(--space-3);
    border: 1px solid var(--palette-c6cad0);
    border-radius: var(--radius-9);
    background: var(--palette-f1f4f3);
    max-width: 100%;
  }
  .root-tab {
    display: inline-flex;
    align-items: center;
    justify-content: flex-start;
    gap: var(--space-6);
    min-height: 30px;
    max-width: 190px;
    padding: var(--space-4) var(--space-10);
    border: 1px solid transparent;
    border-radius: var(--radius-6);
    background: transparent;
    color: var(--palette-454c48);
    font-size: var(--font-size-12);
    font-weight: var(--font-weight-500);
    line-height: var(--line-height-20);
    transition:
      background var(--motion-hover) linear,
      border-color var(--motion-hover) linear,
      color var(--motion-hover) linear;
  }
  .root-tab:hover:not(:disabled):not(.active) {
    background: var(--palette-ffffff);
    border-color: var(--palette-c9ced4);
    color: var(--ink);
  }
  .root-tab.active {
    background: var(--palette-ffffff);
    border-color: var(--palette-619982);
    color: var(--tone-green-ink);
    font-weight: var(--font-weight-600);
    box-shadow: var(--shadow-21);
  }
  .root-tab.active:hover:not(:disabled) {
    background: var(--tone-green-bg);
    border-color: var(--tone-green-ink);
    color: var(--tone-green-ink);
  }
  .root-tab:focus-visible {
    outline: 2px solid var(--tone-green-ink);
    outline-offset: 2px;
  }
  .root-tab-icon {
    color: var(--steel);
    flex-shrink: 0;
  }
  .root-tab.active .root-tab-icon {
    color: var(--tone-green-ink);
  }
  .root-tab-name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .root-dropdown {
    display: inline-flex;
    min-width: 0;
    max-width: 100%;
  }
  .root-dropdown-trigger {
    display: inline-flex;
    align-items: center;
    justify-content: flex-start;
    gap: var(--space-8);
    min-height: 34px;
    max-width: 280px;
    padding: var(--space-6) var(--space-10);
    border: 1px solid var(--palette-c6cad0);
    border-radius: var(--radius-8);
    background: var(--palette-ffffff);
    color: var(--ink);
    font-size: var(--font-size-12);
    font-weight: var(--font-weight-600);
    line-height: var(--line-height-20);
    box-shadow: var(--shadow-4);
    transition:
      background var(--motion-hover) linear,
      border-color var(--motion-hover) linear,
      box-shadow var(--motion-hover) linear;
  }
  .root-dropdown-trigger:hover:not(:disabled) {
    background: var(--palette-f6f9f7);
    border-color: var(--palette-619982);
  }
  .root-dropdown-trigger[aria-expanded='true'] {
    background: var(--palette-ffffff);
    border-color: var(--tone-green-ink);
    box-shadow: var(--shadow-14);
  }
  .root-dropdown-trigger:focus-visible {
    outline: 2px solid var(--tone-green-ink);
    outline-offset: 2px;
  }
  .root-trigger-icon {
    color: var(--tone-green-ink);
    flex-shrink: 0;
  }
  .root-trigger-name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .root-trigger-count {
    flex-shrink: 0;
    padding: var(--space-1) var(--space-6);
    border: 1px solid var(--palette-dce4df);
    border-radius: var(--radius-pill);
    background: var(--palette-f1f4f3);
    color: var(--palette-454c48);
    font-size: var(--font-size-11);
    font-weight: var(--font-weight-500);
    font-variant-numeric: tabular-nums;
    line-height: var(--line-height-16);
  }
  .root-trigger-chevron {
    color: var(--steel);
    flex-shrink: 0;
    transition: transform var(--motion-hover) linear;
  }
  .root-trigger-chevron.rotated {
    transform: rotate(180deg);
    color: var(--tone-green-ink);
  }
  .root-dropdown-menu {
    position: fixed;
    z-index: 220;
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    max-width: min(320px, calc(100vw - 16px));
    overflow-y: auto;
    overscroll-behavior: contain;
    padding: var(--space-6);
    border: 1px solid var(--palette-b9c1cb);
    border-radius: var(--radius-9);
    background: var(--palette-ffffff);
    box-shadow: var(--shadow-6);
  }
  .root-menu-header {
    padding: var(--space-4) var(--space-8) var(--space-6);
    border-bottom: 1px solid var(--palette-e9edf0);
    margin-bottom: var(--space-2);
    color: var(--palette-676e79);
    font-size: var(--font-size-11);
    font-weight: var(--font-weight-500);
  }
  .root-menu-option {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    min-height: 36px;
    padding: var(--space-6) var(--space-10);
    border: 1px solid transparent;
    border-radius: var(--radius-6);
    color: var(--palette-454c48);
    font-size: var(--font-size-12);
    font-weight: var(--font-weight-400);
    cursor: pointer;
    white-space: nowrap;
  }
  .root-menu-option.highlighted {
    background: var(--palette-f1f5f3);
    color: var(--ink);
  }
  .root-menu-option.selected {
    background: var(--tone-green-bg);
    border-color: var(--tone-green-line);
    color: var(--tone-green-ink);
    font-weight: var(--font-weight-600);
  }
  .root-menu-option.selected.highlighted {
    border-color: var(--palette-619982);
  }
  .root-option-icon {
    color: var(--steel);
    flex-shrink: 0;
  }
  .root-menu-option.selected .root-option-icon,
  .root-option-check {
    color: var(--tone-green-ink);
    flex-shrink: 0;
  }
  .root-option-name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .crumb-trail {
    display: inline-flex;
    align-items: center;
    gap: var(--space-4);
    flex-wrap: wrap;
    min-width: 0;
  }
  .crumb-sep {
    color: var(--palette-959ba3);
    flex-shrink: 0;
  }
  .crumb-link {
    display: inline-flex;
    align-items: center;
    gap: var(--space-5);
    min-height: var(--control-hit-area);
    min-width: var(--control-hit-area);
    padding: var(--space-2) var(--space-6);
    border: 0;
    border-radius: var(--radius-5);
    background: transparent;
    color: var(--steel);
    font-size: var(--font-size-12);
    font-weight: var(--font-weight-400);
    line-height: var(--line-height-20);
  }
  .crumb-link:hover:not(:disabled) {
    background: var(--palette-f1f4f3);
    color: var(--ink);
  }
  .crumb-link.current {
    color: var(--ink);
    font-weight: var(--font-weight-600);
  }
  .crumb-link:focus-visible {
    outline: 2px solid var(--tone-green-ink);
    outline-offset: 2px;
  }
  @media (max-width: 600px) {
    .root-prefix {
      display: none;
    }
    .root-tab {
      min-height: 34px;
    }
    .root-dropdown-trigger {
      min-height: 36px;
      max-width: 220px;
    }
  }
</style>
