<script setup lang="ts">
  import { computed, nextTick, onMounted, onUnmounted, ref, useId } from 'vue';
  import { CalendarDays, ChevronLeft, ChevronRight } from '@lucide/vue';
  import SelectField from './SelectField.vue';
  const {
    label,
    min = '',
    max = ''
  } = defineProps<{
    label: string;
    min?: string;
    max?: string;
  }>();
  const model = defineModel<string>({ required: true });
  const emit = defineEmits<{ change: [] }>();
  const id = useId(),
    root = ref<HTMLElement>(),
    panel = ref<HTMLElement>(),
    trigger = ref<HTMLButtonElement>(),
    open = ref(false),
    active = ref('');
  const now = new Date(),
    year = ref(now.getFullYear()),
    month = ref(now.getMonth()),
    position = ref({ left: '0px', top: '0px' });
  const format = (date: Date) =>
    `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
  const parse = (value: string) => new Date(`${value}T12:00:00`);
  const today = format(now);
  const years = computed(() =>
    Array.from({ length: Math.max(now.getFullYear() + 21, year.value + 1) - 1900 }, (_, i) => ({
      value: 1900 + i,
      label: `${1900 + i} 年`
    }))
  );
  const months = Array.from({ length: 12 }, (_, i) => ({ value: i, label: `${i + 1} 月` }));
  const days = computed(() => {
    const first = new Date(year.value, month.value, 1, 12);
    first.setDate(1 - ((first.getDay() + 6) % 7));
    return Array.from({ length: 42 }, (_, i) => {
      const d = new Date(first);
      d.setDate(first.getDate() + i);
      return { value: format(d), day: d.getDate(), outside: d.getMonth() !== month.value };
    });
  });
  const disabled = (value: string) => !!((min && value < min) || (max && value > max));
  function place() {
    if (!trigger.value) return;
    const r = trigger.value.getBoundingClientRect(),
      height = panel.value?.offsetHeight || 360,
      width = panel.value?.offsetWidth || 296;
    position.value = {
      left: `${Math.max(8, Math.min(r.left + (r.width - width) / 2, innerWidth - width - 8))}px`,
      top: `${innerHeight - r.bottom >= height + 12 ? r.bottom + 6 : Math.max(8, r.top - height - 6)}px`
    };
  }
  function changeMonth() {
    active.value = format(new Date(year.value, month.value, 1, 12));
  }
  function close() {
    open.value = false;
  }
  async function show() {
    if (open.value) {
      close();
      return;
    }
    let value = model.value || today;
    if (min && value < min) value = min;
    if (max && value > max) value = max;
    const d = parse(value);
    year.value = d.getFullYear();
    month.value = d.getMonth();
    active.value = value;
    open.value = true;
    await nextTick();
    place();
    focusDay();
  }
  function focusDay() {
    document.getElementById(`${id}-${active.value}`)?.focus({ preventScroll: true });
  }
  function choose(value: string) {
    if (value && disabled(value)) return;
    model.value = value;
    emit('change');
    close();
    trigger.value?.focus();
  }
  async function shift(delta: number) {
    const d = new Date(year.value, month.value + delta, 1, 12);
    year.value = d.getFullYear();
    month.value = d.getMonth();
    active.value = format(d);
    await nextTick();
    place();
  }
  async function keys(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      close();
      trigger.value?.focus();
      return;
    }
    if (!(event.target as HTMLElement).matches('.calendar-day')) return;
    let d = parse(active.value);
    const shifts: Record<string, number> = {
      ArrowLeft: -1,
      ArrowRight: 1,
      ArrowUp: -7,
      ArrowDown: 7
    };
    const shift = shifts[event.key];
    if (shift !== undefined) d.setDate(d.getDate() + shift);
    else if (event.key === 'Home') d.setDate(d.getDate() - ((d.getDay() + 6) % 7));
    else if (event.key === 'End') d.setDate(d.getDate() + 6 - ((d.getDay() + 6) % 7));
    else if (event.key === 'PageUp' || event.key === 'PageDown') {
      d = new Date(year.value, month.value + (event.key === 'PageUp' ? -1 : 1), 1, 12);
    } else return;
    event.preventDefault();
    active.value = format(d);
    year.value = d.getFullYear();
    month.value = d.getMonth();
    await nextTick();
    focusDay();
  }
  function outside(event: PointerEvent) {
    const target = event.target as HTMLElement;
    if (
      !root.value?.contains(target) &&
      !panel.value?.contains(target) &&
      !target.closest('.select-menu')
    )
      close();
  }
  function reposition() {
    if (open.value) place();
  }
  onMounted(() => {
    document.addEventListener('pointerdown', outside);
    window.addEventListener('resize', reposition);
    window.addEventListener('scroll', reposition, true);
  });
  onUnmounted(() => {
    document.removeEventListener('pointerdown', outside);
    window.removeEventListener('resize', reposition);
    window.removeEventListener('scroll', reposition, true);
  });
</script>
<template>
  <span ref="root" class="date-field"
    ><button
      ref="trigger"
      type="button"
      class="date-trigger"
      :class="{ placeholder: !model }"
      :aria-label="label"
      aria-haspopup="dialog"
      :aria-expanded="open"
      @click="show"
      @keydown.down.prevent="show"
      ><CalendarDays :size="15" /><span>{{ model || label }}</span></button
    ><Teleport to="body"
      ><Transition name="popover"
        ><section
          v-if="open"
          ref="panel"
          class="calendar-panel"
          role="dialog"
          :aria-label="`${label}日历`"
          :style="position"
          @keydown="keys"
          ><header
            ><button type="button" class="icon-btn" aria-label="上个月" @click="shift(-1)"
              ><ChevronLeft :size="16" /></button
            ><SelectField
              v-model="year"
              :label="`${label}年份`"
              :options="years"
              @change="changeMonth" /><SelectField
              v-model="month"
              :label="`${label}月份`"
              :options="months"
              @change="changeMonth" /><button
              type="button"
              class="icon-btn"
              aria-label="下个月"
              @click="shift(1)"
              ><ChevronRight :size="16" /></button></header
          ><div class="calendar-week"
            ><span v-for="day in ['一', '二', '三', '四', '五', '六', '日']" :key="day">{{
              day
            }}</span></div
          ><div class="calendar-grid"
            ><button
              v-for="day in days"
              :id="`${id}-${day.value}`"
              :key="day.value"
              type="button"
              class="calendar-day"
              :class="{
                outside: day.outside,
                today: day.value === today,
                selected: day.value === model
              }"
              :aria-label="day.value"
              :aria-pressed="day.value === model"
              :aria-disabled="disabled(day.value)"
              :tabindex="day.value === active ? 0 : -1"
              @focus="active = day.value"
              @click="choose(day.value)"
              >{{ day.day }}</button
            ></div
          ><footer
            ><button type="button" @click="choose('')">清除</button
            ><button type="button" :disabled="disabled(today)" @click="choose(today)">今天</button
            ><button
              type="button"
              @click="
                close();
                trigger?.focus();
              "
              >关闭</button
            ></footer
          ></section
        ></Transition
      ></Teleport
    ></span
  >
</template>

<style scoped>
  .date-field {
    display: inline-flex;
    min-width: 140px;
  }
  .date-trigger {
    min-height: 38px;
    font-size: var(--font-size-12);
    gap: var(--space-9);
    justify-content: flex-start;
    width: 100%;
    font-variant-numeric: tabular-nums;
    font-weight: var(--font-weight-400);
  }
  .date-trigger svg {
    color: var(--palette-87948d);
  }
  .date-trigger.placeholder {
    color: var(--ash);
  }
  .calendar-panel {
    position: fixed;
    z-index: 190;
    width: 296px;
    padding: var(--space-12);
    border: 1px solid var(--palette-dce4df);
    border-radius: var(--radius-11);
    background: var(--palette-ffffff);
    box-shadow: var(--shadow-1);
  }
  .calendar-panel > header {
    display: flex;
    align-items: center;
    gap: var(--space-5);
    margin-bottom: var(--space-12);
  }
  .calendar-panel :deep(.select-field) {
    min-width: 0;
    flex: 1;
  }
  .calendar-panel :deep(.select-field:first-of-type) {
    flex: 1.35;
  }
  .calendar-panel :deep(.select-trigger) {
    min-height: 32px;
    gap: var(--space-3);
    padding: var(--space-5) var(--space-7);
    font-size: var(--font-size-12);
  }
  .calendar-panel .icon-btn {
    padding: var(--space-4);
  }
  .calendar-week,
  .calendar-grid {
    display: grid;
    grid-template-columns: repeat(7, minmax(0, 1fr));
    gap: var(--space-3);
  }
  .calendar-week {
    font-size: var(--font-size-10);
    color: var(--palette-929b96);
    text-align: center;
    margin-bottom: var(--space-7);
  }
  .calendar-day {
    padding: 0;
    min-height: 33px;
    font-size: var(--font-size-12);
    font-weight: var(--font-weight-400);
    border: 1px solid transparent;
    border-radius: var(--radius-6);
    line-height: var(--line-height-1);
  }
  .calendar-day.outside {
    color: var(--palette-b7beb9);
  }
  .calendar-day.today {
    border-color: var(--palette-b9d6c7);
    color: var(--tone-green-ink);
  }
  .calendar-day.selected {
    background: var(--tone-green-ink);
    color: var(--palette-ffffff);
    border-color: var(--tone-green-ink);
  }
  .calendar-day.selected:hover {
    background: var(--palette-226344);
    color: var(--palette-ffffff);
  }
  .calendar-day[aria-disabled='true'] {
    opacity: 0.3;
    cursor: not-allowed;
  }
  .calendar-panel footer {
    display: flex;
    justify-content: space-between;
    border-top: 1px solid var(--palette-edf0ee);
    padding-top: var(--space-10);
    margin-top: var(--space-10);
  }
  .calendar-panel footer button {
    border: 0;
    padding: var(--space-5) var(--space-8);
    font-size: var(--font-size-11);
    color: var(--palette-48735b);
  }
  .calendar-day:focus-visible {
    outline: 2px solid var(--palette-a8d9cb);
    outline-offset: 0;
  }
  @media (max-width: 800px) {
    .date-field {
      flex: 1;
      min-width: 120px;
    }
  }
</style>
