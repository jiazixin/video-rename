<script setup lang="ts">
  import { nextTick, onMounted, onUnmounted, ref, useId } from 'vue';
  import { Check } from '@lucide/vue';
  // Fastest on top so the list reads in the same direction it opens.
  const speeds = [2, 1.5, 1.25, 1, 0.75, 0.5];
  const speed = defineModel<number>({ required: true });
  const id = useId();
  const root = ref<HTMLElement>(),
    trigger = ref<HTMLButtonElement>(),
    items = ref<HTMLButtonElement[]>([]),
    open = ref(false),
    active = ref(0);
  function focusItem(index: number) {
    active.value = (index + speeds.length) % speeds.length;
    items.value[active.value]?.focus();
  }
  async function show() {
    open.value = true;
    await nextTick();
    focusItem(Math.max(0, speeds.indexOf(speed.value)));
  }
  function close(refocus: boolean) {
    open.value = false;
    if (refocus) trigger.value?.focus();
  }
  function choose(value: number) {
    speed.value = value;
    close(true);
  }
  function triggerKeys(event: KeyboardEvent) {
    if (open.value || !['ArrowUp', 'ArrowDown'].includes(event.key)) return;
    event.preventDefault();
    void show();
  }
  // Menu keys stop here so Escape closes only the menu, not the player dialog behind it.
  function menuKeys(event: KeyboardEvent) {
    const moves: Record<string, number> = {
      ArrowDown: active.value + 1,
      ArrowUp: active.value - 1,
      Home: 0,
      End: speeds.length - 1
    };
    const target = moves[event.key];
    if (target !== undefined) focusItem(target);
    else if (event.key === 'Escape' || event.key === 'Tab') close(true);
    else return;
    event.preventDefault();
    event.stopPropagation();
  }
  function outside(event: PointerEvent) {
    if (open.value && !root.value?.contains(event.target as Node)) close(false);
  }
  onMounted(() => {
    document.addEventListener('pointerdown', outside);
  });
  onUnmounted(() => {
    document.removeEventListener('pointerdown', outside);
  });
</script>
<template>
  <div ref="root" class="speed-picker">
    <button
      ref="trigger"
      class="speed-trigger"
      :class="{ open }"
      :aria-label="`播放速度 ${speed}×`"
      title="播放速度"
      aria-haspopup="menu"
      :aria-expanded="open"
      :aria-controls="open ? id : undefined"
      @click="open ? close(false) : show()"
      @keydown="triggerKeys"
      >{{ speed }}×</button
    >
    <Transition name="speed-menu">
      <div
        v-if="open"
        :id="id"
        class="speed-menu"
        role="menu"
        aria-label="播放速度"
        @keydown="menuKeys"
      >
        <button
          v-for="(value, index) in speeds"
          :key="value"
          ref="items"
          role="menuitemradio"
          :aria-checked="value === speed"
          :tabindex="index === active ? 0 : -1"
          @click="choose(value)"
          @pointermove="active = index"
          ><span>{{ value }}×</span><Check v-if="value === speed" :size="14"
        /></button>
      </div>
    </Transition>
  </div>
</template>
<style scoped>
  .speed-picker {
    position: relative;
    display: flex;
  }
  .speed-picker button {
    border: 0;
    background: transparent;
    color: var(--stage-ink);
    border-radius: var(--radius-8);
  }
  .speed-picker button:hover:not(:disabled),
  .speed-picker .speed-menu button:focus-visible {
    border: 0;
    background: var(--stage-hover);
  }
  .speed-picker .speed-trigger {
    min-width: 46px;
    padding: var(--space-4) var(--space-8);
    border: 1px solid var(--stage-line);
    font-size: var(--font-size-12);
    font-variant-numeric: tabular-nums;
  }
  .speed-picker .speed-trigger:hover:not(:disabled),
  .speed-picker .speed-trigger.open {
    border: 1px solid var(--stage-line);
    background: var(--stage-hover);
  }
  /* Opens upward from the control bar into the picture, inside the fullscreen element. */
  .speed-menu {
    position: absolute;
    right: 50%;
    bottom: calc(100% + var(--space-8));
    z-index: 2;
    display: flex;
    flex-direction: column;
    min-width: 96px;
    padding: var(--space-5);
    transform: translateX(50%);
    border: 1px solid var(--stage-line);
    border-radius: var(--radius-9);
    background: var(--stage-bar);
    box-shadow: var(--shadow-11);
  }
  .speed-picker .speed-menu button {
    position: relative;
    justify-content: center;
    min-height: 32px;
    padding: var(--space-6) var(--space-26);
    border-radius: var(--radius-5);
    font-size: var(--font-size-12);
    font-variant-numeric: tabular-nums;
  }
  .speed-picker .speed-menu button[aria-checked='true'] {
    color: var(--stage-accent);
  }
  .speed-menu svg {
    position: absolute;
    right: var(--space-8);
    top: 50%;
    transform: translateY(-50%);
  }
  /* Only the entrance animates; closing is immediate like the other dropdowns. */
  .speed-menu-enter-active {
    transition:
      opacity var(--motion-exit) linear,
      transform var(--motion-exit) linear;
  }
  .speed-menu-enter-from {
    opacity: 0;
    transform: translate(50%, 4px);
  }
</style>
