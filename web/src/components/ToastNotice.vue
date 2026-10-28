<script setup lang="ts">
  import { CircleAlert, CircleCheck, X } from '@lucide/vue';
  import type { ToastItem } from './toast-types';
  const { notices, animated } = defineProps<{
    notices: readonly ToastItem[];
    animated: boolean;
  }>();
  defineEmits<{ close: [id: number] }>();
</script>

<template>
  <Teleport to="body">
    <TransitionGroup name="toast" tag="div" class="toast-stack" :css="animated">
      <div
        v-for="notice in notices"
        :key="notice.id"
        class="toast-notice"
        :class="notice.tone"
        role="status"
        aria-live="polite"
        aria-atomic="true"
      >
        <CircleAlert
          v-if="notice.tone === 'error'"
          class="toast-icon"
          :size="20"
          aria-hidden="true"
        />
        <CircleCheck v-else class="toast-icon" :size="20" aria-hidden="true" />
        <span class="toast-message">{{ notice.message }}</span>
        <button
          type="button"
          class="toast-close"
          aria-label="关闭提示"
          @click="$emit('close', notice.id)"
          ><X :size="16" aria-hidden="true"
        /></button>
        <div class="toast-countdown" aria-hidden="true" />
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
    gap: var(--space-12);
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
    gap: var(--space-12);
    width: 100%;
    box-sizing: border-box;
    padding: var(--space-18) var(--space-16) var(--space-21);
    border: 1px solid var(--palette-cee5db);
    border-radius: var(--radius-12);
    background: var(--palette-f0f8f5);
    color: var(--palette-287257);
    box-shadow: var(--shadow-7);
    overflow: hidden;
    pointer-events: auto;
  }
  .toast-icon {
    flex-shrink: 0;
    margin-top: var(--space-1);
  }
  .toast-notice.error {
    border-color: var(--tone-red-line);
    background: var(--tone-red-bg);
    color: var(--tone-red-ink);
  }
  .toast-notice.error .toast-close:hover {
    background: var(--tone-red-line);
  }
  .toast-notice.error .toast-close:focus-visible {
    outline-color: var(--tone-red-ink);
  }
  .toast-notice.error .toast-countdown {
    background: var(--tone-red-ink);
  }
  .toast-message {
    flex: 1;
    min-width: 0;
    line-height: var(--line-height-1-6);
    font-size: var(--font-size-14);
    overflow-wrap: anywhere;
    white-space: pre-line;
  }
  .toast-close {
    flex-shrink: 0;
    min-width: var(--control-hit-area);
    min-height: var(--control-hit-area);
    padding: var(--space-3);
    border: 0;
    background: transparent;
    color: inherit;
    border-radius: var(--radius-4);
  }
  .toast-close:hover {
    background: var(--palette-dceee5);
  }
  .toast-close:focus-visible {
    outline: 2px solid var(--palette-287257);
    outline-offset: 2px;
  }
  .toast-countdown {
    position: absolute;
    bottom: 0;
    left: 0;
    right: 0;
    height: 3px;
    background: var(--palette-399874);
    transform-origin: left;
    animation: toast-countdown var(--toast-duration) linear forwards;
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
      opacity var(--motion-enter) linear,
      transform var(--motion-enter) linear;
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
  @media (max-width: 600px), (pointer: coarse) {
    .toast-close {
      min-width: var(--control-touch-area);
      min-height: var(--control-touch-area);
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
