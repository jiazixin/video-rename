<script setup lang="ts">
  import { computed, nextTick, onMounted, onUnmounted, ref, useId } from 'vue';
  import { Check, ChevronDown } from '@lucide/vue';
  type Value = string | number;
  const { options, label } = defineProps<{
    options: { value: Value; label: string }[];
    label: string;
  }>();
  const model = defineModel<Value>({ required: true });
  const emit = defineEmits<{ change: [] }>();
  const menu = ref<HTMLElement>();
  const root = ref<HTMLElement>(),
    trigger = ref<HTMLButtonElement>(),
    open = ref(false),
    active = ref(0);
  const id = useId(),
    position = ref({ left: '0px', top: '0px', width: '0px', maxHeight: '240px' });
  const selected = computed(() => options.find((o) => o.value === model.value)?.label || '请选择');
  function close() {
    open.value = false;
  }
  function place() {
    if (!trigger.value) return;
    const rect = trigger.value.getBoundingClientRect(),
      room = innerHeight - rect.bottom - 12;
    const height = Math.min(240, options.length * 36 + 12);
    const width = Math.min(Math.max(rect.width, 160), innerWidth - 16);
    position.value = {
      left: `${Math.max(8, Math.min(rect.left + (rect.width - width) / 2, innerWidth - width - 8))}px`,
      top: `${room >= height ? rect.bottom + 6 : Math.max(8, rect.top - height - 6)}px`,
      width: `${width}px`,
      maxHeight: `${Math.min(height, innerHeight - 16)}px`
    };
  }
  async function show() {
    if (open.value) {
      close();
      return;
    }
    place();
    active.value = Math.max(
      0,
      options.findIndex((o) => o.value === model.value)
    );
    open.value = true;
    await nextTick();
    reveal();
  }
  function reveal() {
    const option = document.getElementById(`${id}-${active.value}`),
      list = menu.value;
    if (!option || !list) return;
    if (option.offsetTop < list.scrollTop) list.scrollTop = option.offsetTop;
    else if (option.offsetTop + option.offsetHeight > list.scrollTop + list.clientHeight)
      list.scrollTop = option.offsetTop + option.offsetHeight - list.clientHeight;
  }
  function choose(index: number) {
    const option = options[index];
    if (!option) return;
    model.value = option.value;
    emit('change');
    close();
    trigger.value?.focus();
  }
  async function keys(event: KeyboardEvent) {
    if (event.key === 'Escape' && open.value) {
      event.preventDefault();
      event.stopPropagation();
      close();
      return;
    }
    if (event.key === 'Tab') {
      close();
      return;
    }
    if (['ArrowDown', 'ArrowUp', 'Home', 'End', 'Enter', ' '].includes(event.key)) {
      event.preventDefault();
      if (!open.value) {
        await show();
        return;
      }
      if (event.key === 'Enter' || event.key === ' ') {
        choose(active.value);
        return;
      }
      if (event.key === 'Home') active.value = 0;
      else if (event.key === 'End') active.value = options.length - 1;
      else
        active.value =
          (active.value + (event.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length;
      await nextTick();
      reveal();
    } else if (event.key.length === 1) {
      const index = options.findIndex((o) =>
        o.label.toLowerCase().startsWith(event.key.toLowerCase())
      );
      if (index >= 0) {
        if (!open.value) await show();
        active.value = index;
        await nextTick();
        reveal();
      }
    }
  }
  function outside(event: PointerEvent) {
    if (!root.value?.contains(event.target as Node) && !menu.value?.contains(event.target as Node))
      close();
  }
  function resize() {
    if (open.value) place();
  }
  function scroll(event: Event) {
    if (open.value && !(event.target instanceof Element && event.target.closest('.select-menu')))
      place();
  }
  onMounted(() => {
    document.addEventListener('pointerdown', outside);
    window.addEventListener('resize', resize);
    document.addEventListener('scroll', scroll, true);
  });
  onUnmounted(() => {
    document.removeEventListener('pointerdown', outside);
    window.removeEventListener('resize', resize);
    document.removeEventListener('scroll', scroll, true);
  });
</script>
<template>
  <span ref="root" class="select-field"
    ><button
      ref="trigger"
      type="button"
      class="select-trigger"
      role="combobox"
      :aria-label="label"
      aria-haspopup="listbox"
      :aria-expanded="open"
      :aria-controls="open ? id : undefined"
      :aria-activedescendant="open ? `${id}-${active}` : undefined"
      @click="show"
      @keydown="keys"
      ><span>{{ selected }}</span
      ><ChevronDown :size="15" :class="{ rotated: open }" /></button
    ><Teleport to="body"
      ><Transition name="popover"
        ><span
          v-if="open"
          :id="id"
          ref="menu"
          role="listbox"
          :aria-label="label"
          class="select-menu"
          :style="position"
          ><span
            v-for="(option, index) in options"
            :id="`${id}-${index}`"
            :key="option.value"
            role="option"
            :aria-selected="option.value === model"
            class="select-option"
            :class="{ highlighted: index === active }"
            @pointermove="active = index"
            @mousedown.prevent
            @click.prevent.stop="choose(index)"
            ><span>{{ option.label }}</span
            ><Check v-if="option.value === model" :size="14" /></span></span></Transition></Teleport
  ></span>
</template>
