<script setup lang="ts">
  import { onActivated, onDeactivated, onUnmounted, ref } from 'vue';
  import { CircleCheck, X } from '@lucide/vue';

  const duration = 5000;
  const notices = ref<{ id: number; message: string }[]>([]);
  const timers = new Map<number, ReturnType<typeof setTimeout>>();
  let nextID = 0;
  let active = true;
  function close(id: number) {
    clearTimeout(timers.get(id));
    timers.delete(id);
    notices.value = notices.value.filter((notice) => notice.id !== id);
  }
  function show(message: string) {
    if (!message || !active) return;
    const id = ++nextID;
    notices.value.unshift({ id, message });
    timers.set(
      id,
      setTimeout(() => close(id), duration)
    );
  }
  function clear() {
    timers.forEach((timer) => clearTimeout(timer));
    timers.clear();
    notices.value = [];
  }
  function deactivate() {
    active = false;
    clear();
  }
  onActivated(() => {
    active = true;
  });
  onDeactivated(deactivate);
  onUnmounted(deactivate);
  defineExpose({ show, clear });
</script>

<template>
  <Teleport to="body">
    <TransitionGroup name="toast" tag="div" class="toast-stack">
      <div
        v-for="notice in notices"
        :key="notice.id"
        class="toast-notice"
        role="status"
        aria-live="polite"
        aria-atomic="true"
      >
        <CircleCheck class="toast-icon" :size="20" aria-hidden="true" />
        <span class="toast-message">{{ notice.message }}</span>
        <button type="button" class="toast-close" aria-label="关闭提示" @click="close(notice.id)"
          ><X :size="16" aria-hidden="true"
        /></button>
        <div
          class="toast-countdown"
          :style="{ animationDuration: `${duration}ms` }"
          aria-hidden="true"
        />
      </div>
    </TransitionGroup>
  </Teleport>
</template>

<style scoped>
  .toast-stack {
    position: fixed;
    top: 24px;
    right: 24px;
    z-index: 300;
    display: flex;
    flex-direction: column;
    gap: 12px;
    width: 380px;
    max-width: calc(100vw - 32px);
    max-height: calc(100dvh - 48px);
    overflow-y: auto;
    pointer-events: none;
  }
  .toast-notice {
    position: relative;
    display: flex;
    flex-shrink: 0;
    align-items: flex-start;
    gap: 12px;
    width: 100%;
    box-sizing: border-box;
    padding: 18px 16px 21px;
    border: 1px solid #cee5db;
    border-radius: 12px;
    background: #f0f8f5;
    color: #287257;
    box-shadow: 0 8px 30px #182c201a;
    overflow: hidden;
    pointer-events: auto;
  }
  .toast-icon {
    flex-shrink: 0;
    margin-top: 1px;
  }
  .toast-message {
    flex: 1;
    min-width: 0;
    line-height: 1.6;
    font-size: 14px;
    overflow-wrap: anywhere;
    white-space: pre-line;
  }
  .toast-close {
    flex-shrink: 0;
    min-height: 0;
    padding: 3px;
    border: 0;
    background: transparent;
    color: inherit;
    border-radius: 4px;
  }
  .toast-close:hover {
    background: #dceee5;
  }
  .toast-close:focus-visible {
    outline: 2px solid #287257;
    outline-offset: 2px;
  }
  .toast-countdown {
    position: absolute;
    bottom: 0;
    left: 0;
    right: 0;
    height: 3px;
    background: #399874;
    transform-origin: left;
    animation: toast-countdown linear forwards;
  }
  @keyframes toast-countdown {
    from {
      transform: scaleX(1);
    }
    to {
      transform: scaleX(0);
    }
  }
  .toast-move,
  .toast-enter-active,
  .toast-leave-active {
    transition:
      opacity 0.18s ease,
      transform 0.18s ease;
  }
  .toast-enter-from,
  .toast-leave-to {
    opacity: 0;
    transform: translateX(16px);
  }
  @media (max-width: 600px) {
    .toast-stack {
      top: 16px;
      right: 16px;
      max-height: calc(100dvh - 32px);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .toast-move,
    .toast-enter-active,
    .toast-leave-active {
      transition: none;
    }
  }
</style>
