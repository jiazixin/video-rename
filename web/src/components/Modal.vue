<script lang="ts">
  let locks = 0;
  let oldOverflow = '';
  let oldPadding = '';
  function lockScroll() {
    if (locks++ === 0) {
      oldOverflow = document.body.style.overflow;
      oldPadding = document.body.style.paddingRight;
      const gap = window.innerWidth - document.documentElement.clientWidth;
      document.body.style.overflow = 'hidden';
      if (gap) document.body.style.paddingRight = `${gap}px`;
    }
  }
  function unlockScroll() {
    if (--locks === 0) {
      document.body.style.overflow = oldOverflow;
      document.body.style.paddingRight = oldPadding;
    }
  }
</script>
<script setup lang="ts">
  import { onMounted, onUnmounted, ref } from 'vue';
  import { X } from '@lucide/vue';
  defineOptions({ inheritAttrs: false });
  const props = defineProps<{
    title: string;
    wide?: boolean;
    theater?: boolean;
    contained?: boolean;
  }>();
  const emit = defineEmits<{ close: [] }>();
  const panel = ref<HTMLElement>();
  const previous = document.activeElement as HTMLElement | null;
  function keys(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      emit('close');
      return;
    }
    if (e.key === 'Tab') {
      const items = panel.value?.querySelectorAll<HTMLElement>(
        'button:not(:disabled),input:not(:disabled),select:not(:disabled),textarea:not(:disabled),summary,[tabindex="0"]'
      );
      if (!items?.length) return;
      const visible = Array.from(items).filter((item) => item.getClientRects().length);
      const first = visible[0],
        last = visible[visible.length - 1];
      if (!first || !last) return;
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    }
  }
  onMounted(() => {
    lockScroll();
    // Media viewers take focus on the panel itself: landing on the first tool button would
    // light its focus ring at the first key press and let Space trigger it.
    if (props.theater) panel.value?.focus();
    else panel.value?.querySelector<HTMLElement>('input,button')?.focus();
    document.addEventListener('keydown', keys);
  });
  onUnmounted(() => {
    unlockScroll();
    document.removeEventListener('keydown', keys);
    previous?.focus();
  });
</script>
<!-- Wrap each v-if'd Modal in ModalTransition, which teleports it and animates open and close. -->
<template>
  <div class="modal-backdrop" :class="{ theater }" @mousedown.self.prevent="emit('close')"
    ><section
      v-bind="$attrs"
      ref="panel"
      role="dialog"
      aria-modal="true"
      :aria-label="title"
      :tabindex="theater ? -1 : undefined"
      class="modal"
      :class="{ wide, 'theater-modal': theater, 'contained-modal': contained }"
      ><header
        ><div class="modal-heading"
          ><h2>{{ title }}</h2
          ><slot name="header" /></div
        ><button class="icon-btn" aria-label="关闭" @click="emit('close')"
          ><X :size="20" /></button></header
      ><div class="modal-content"><slot /></div
      ><footer v-if="$slots.footer" class="modal-footer"><slot name="footer" /></footer></section
  ></div>
</template>

<style scoped>
  .modal-heading {
    min-width: 0;
  }
  .modal-content {
    overflow-wrap: anywhere;
  }
  /* Theater: media viewers sit in a large dark frame. A clear margin of dimmed page stays
     around it so clicking outside closes the viewer. */
  .modal-backdrop.theater {
    padding: clamp(16px, 5vh, 56px) clamp(16px, 6vw, 96px);
    background: rgba(6, 9, 8, 0.78);
    backdrop-filter: blur(6px);
  }
  .modal.theater-modal {
    --stage: #0c1110;
    --stage-bar: #121a17;
    --stage-ink: #eef5f1;
    --stage-dim: #9db0a6;
    --stage-line: rgba(238, 245, 241, 0.14);
    --stage-hover: rgba(255, 255, 255, 0.12);
    --stage-accent: #6fcf9f;
    width: min(100%, 1440px);
    max-width: none;
    height: 100%;
    max-height: 960px;
    padding: 12px;
    background: var(--stage);
    border: 1px solid var(--stage-line);
    border-radius: 14px;
    box-shadow: 0 24px 80px rgba(0, 0, 0, 0.55);
    color: var(--stage-ink);
    outline: none;
  }
  .theater-modal > header {
    gap: 12px;
    padding: 2px 4px 10px 6px;
    border-bottom: 0;
  }
  .theater-modal .modal-heading {
    flex: 1;
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .theater-modal .modal-heading h2 {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 15px;
    color: var(--stage-ink);
  }
  .theater-modal > header .icon-btn {
    color: var(--stage-ink);
    border-radius: 8px;
  }
  .theater-modal > header .icon-btn:hover {
    background: var(--stage-hover);
  }
  .theater-modal > .modal-content {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    padding: 0;
    overflow: hidden;
  }
  .contained-modal > .modal-content {
    display: flex;
    flex-direction: column;
    min-height: 0;
    overflow: hidden;
    padding: 16px 24px;
  }
  .contained-modal > header,
  .modal-footer {
    flex-shrink: 0;
  }
  .modal-footer {
    padding: 12px 24px 16px;
    border-top: 1px solid var(--line);
  }
  .modal-footer :deep(.modal-actions) {
    margin: 0;
    padding: 0;
    border: 0;
  }
  @media (max-width: 600px) {
    .modal-backdrop.theater {
      padding: 12px;
    }
    .modal.theater-modal {
      padding: 8px;
    }
    .contained-modal > .modal-content {
      padding: 12px 16px;
    }
    .modal-footer {
      padding: 12px 16px;
    }
  }
</style>
