<script setup lang="ts">
  import { nextTick, onMounted, onUnmounted, ref } from 'vue';
  import { lockScroll, unlockScroll } from './modal-scroll';
  import { X } from '@lucide/vue';
  defineOptions({ inheritAttrs: false });
  const { title, wide, theater, contained } = defineProps<{
    title: string;
    wide?: boolean;
    theater?: boolean;
    contained?: boolean;
  }>();
  const emit = defineEmits<{ close: [] }>();
  const panel = ref<HTMLElement>();
  const previous = document.activeElement as HTMLElement | null;
  function keys(e: KeyboardEvent) {
    const topDialog = Array.from(
      document.querySelectorAll('[role="dialog"][aria-modal="true"]')
    ).at(-1);
    if (topDialog !== panel.value) return;
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
    void nextTick(() => {
      if (!panel.value?.isConnected) return;
      if (theater) panel.value.focus();
      else panel.value.querySelector<HTMLElement>('input,button')?.focus();
    });
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
  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: var(--palette-10191f55);
    backdrop-filter: blur(3px);
    z-index: 100;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: var(--space-24);
  }
  .modal {
    width: 480px;
    max-width: 100%;
    max-height: 90vh;
    background: var(--palette-ffffff);
    border: 1px solid var(--line);
    border-radius: var(--radius-15);
    box-shadow: var(--shadow-2);
    display: flex;
    flex-direction: column;
  }
  .modal.wide {
    width: 650px;
  }
  .modal > header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: var(--space-19) var(--space-24);
    border-bottom: 1px solid var(--line);
  }
  .modal > header h2 {
    font-size: var(--font-size-16);
  }
  .modal-content {
    padding: var(--space-24);
    overflow: auto;
  }
  .modal-content :deep(p) {
    font-size: var(--font-size-13);
  }
  .modal-content :deep(label) {
    margin-bottom: var(--space-16);
  }
  @media (max-width: 800px) {
    .modal-backdrop {
      padding: var(--space-14);
    }
  }
  @media (max-width: 800px) {
    .modal-content {
      padding: var(--space-20);
    }
  }
  @media (max-width: 800px) {
    .modal > header {
      padding: var(--space-15) var(--space-20);
    }
  }
  @media (max-width: 800px) {
    .modal {
      max-height: 94vh;
    }
  }

  .modal-backdrop,
  .modal-content {
    overscroll-behavior: contain;
  }
  .modal > header h2 {
    overflow-wrap: anywhere;
    min-width: 0;
  }

  .modal-heading {
    min-width: 0;
  }
  .modal-content {
    overflow-wrap: anywhere;
  }
  /* Theater: media viewers sit in a large dark frame. A clear margin of dimmed page stays
     around it so clicking outside closes the viewer. */
  .modal-backdrop.theater {
    padding: clamp(var(--space-16), 5vh, var(--space-56))
      clamp(var(--space-16), 6vw, var(--space-96));
    background: var(--palette-rgba-6-9-8-0_78);
    backdrop-filter: blur(6px);
  }
  .modal.theater-modal {
    --stage: var(--palette-0c1110);
    --stage-bar: var(--palette-121a17);
    --stage-ink: var(--palette-eef5f1);
    --stage-dim: var(--palette-9db0a6);
    --stage-line: var(--palette-rgba-238-245-241-0_14);
    --stage-hover: var(--palette-rgba-255-255-255-0_12);
    --stage-accent: var(--palette-6fcf9f);
    width: min(100%, 1440px);
    max-width: none;
    height: 100%;
    max-height: 960px;
    padding: var(--space-12);
    background: var(--stage);
    border: 1px solid var(--stage-line);
    border-radius: var(--radius-14);
    box-shadow: var(--shadow-3);
    color: var(--stage-ink);
    outline: none;
  }
  .theater-modal > header {
    gap: var(--space-12);
    padding: var(--space-2) var(--space-4) var(--space-10) var(--space-6);
    border-bottom: 0;
  }
  .theater-modal .modal-heading {
    flex: 1;
    display: flex;
    align-items: center;
    gap: var(--space-10);
  }
  .theater-modal .modal-heading h2 {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: var(--font-size-15);
    color: var(--stage-ink);
  }
  .theater-modal > header .icon-btn {
    color: var(--stage-ink);
    border-radius: var(--radius-8);
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
    padding: var(--space-16) var(--space-24);
  }
  .contained-modal > header,
  .modal-footer {
    flex-shrink: 0;
  }
  .modal-footer {
    padding: var(--space-12) var(--space-24) var(--space-16);
    border-top: 1px solid var(--line);
  }
  .modal-footer :deep(.modal-actions) {
    margin: 0;
    padding: 0;
    border: 0;
  }
  @media (max-width: 600px) {
    .modal-backdrop.theater {
      padding: var(--space-12);
    }
    .modal.theater-modal {
      padding: var(--space-8);
    }
    .contained-modal > .modal-content {
      padding: var(--space-12) var(--space-16);
    }
    .modal-footer {
      padding: var(--space-12) var(--space-16);
    }
  }
</style>
