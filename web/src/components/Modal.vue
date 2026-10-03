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
  import { X } from 'lucide-vue-next';
  defineOptions({ inheritAttrs: false });
  defineProps<{ title: string; wide?: boolean; player?: boolean; contained?: boolean }>();
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
    panel.value?.querySelector<HTMLElement>('input,button')?.focus();
    document.addEventListener('keydown', keys);
  });
  onUnmounted(() => {
    unlockScroll();
    document.removeEventListener('keydown', keys);
    previous?.focus();
  });
</script>
<template>
  <Teleport to="body"
    ><div class="modal-backdrop" @mousedown.self.prevent="emit('close')"
      ><section
        v-bind="$attrs"
        ref="panel"
        role="dialog"
        aria-modal="true"
        :aria-label="title"
        class="modal"
        :class="{ wide, 'player-modal': player, 'contained-modal': contained }"
        ><header
          ><div class="modal-heading"
            ><h2>{{ title }}</h2
            ><slot name="header" /></div
          ><button class="icon-btn" aria-label="关闭" @click="emit('close')"
            ><X :size="20" /></button></header
        ><div class="modal-content"><slot /></div
        ><footer v-if="$slots.footer" class="modal-footer"
          ><slot name="footer" /></footer></section></div
  ></Teleport>
</template>

<style scoped>
  .modal-heading {
    min-width: 0;
  }
  .modal-content {
    overflow-wrap: anywhere;
  }
  .modal.player-modal {
    width: 1280px;
    max-width: 96vw;
    max-height: 96vh;
  }
  .player-modal .modal-content {
    padding: 14px;
  }
  .player-modal > header {
    padding: 12px 18px;
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
    .contained-modal > .modal-content {
      padding: 12px 16px;
    }
    .modal-footer {
      padding: 12px 16px;
    }
  }
</style>
