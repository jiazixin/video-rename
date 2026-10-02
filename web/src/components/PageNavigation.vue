<script setup lang="ts">
  import { computed, ref, watch, useId } from 'vue';
  import { ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight } from 'lucide-vue-next';
  const props = withDefaults(
    defineProps<{
      page: number;
      totalPages: number;
      disabled?: boolean;
      label?: string;
      previousLabel?: string;
      nextLabel?: string;
    }>(),
    { label: '分页导航', previousLabel: '上一页', nextLabel: '下一页' }
  );
  const emit = defineEmits<{ change: [page: number] }>();
  const errorID = useId();
  const count = computed(() => Math.max(1, props.totalPages));
  const draft = ref(String(props.page)),
    error = ref('');
  watch(
    () => props.page,
    (value) => {
      draft.value = String(value);
      error.value = '';
    }
  );
  watch(count, () => {
    error.value = '';
  });
  function go(value: number) {
    if (props.disabled) return;
    error.value = '';
    draft.value = String(value);
    if (value !== props.page) emit('change', value);
  }
  function jump() {
    if (props.disabled) return;
    const value = Number(draft.value);
    if (
      !/^\d+$/.test(draft.value) ||
      !Number.isSafeInteger(value) ||
      value < 1 ||
      value > count.value
    ) {
      error.value = `请输入 1–${count.value} 之间的整数页码`;
      return;
    }
    go(value);
  }
</script>
<template>
  <nav class="page-navigation" :aria-label="label" :aria-busy="disabled || undefined">
    <div class="page-steps">
      <button
        type="button"
        class="page-edge"
        aria-label="首页"
        title="首页"
        :disabled="disabled || page <= 1"
        @click="go(1)"
        ><ChevronsLeft :size="16"
      /></button>
      <button
        type="button"
        :aria-label="previousLabel"
        :title="previousLabel"
        :disabled="disabled || page <= 1"
        @click="go(page - 1)"
        ><ChevronLeft :size="16"
      /></button>
      <span class="page-readout" aria-live="polite" aria-atomic="true"
        ><strong class="page-current">{{ page }}</strong
        ><span class="page-slash">/</span><span>{{ count }}</span
        ><span>页</span></span
      >
      <button
        type="button"
        :aria-label="nextLabel"
        :title="nextLabel"
        :disabled="disabled || page >= count"
        @click="go(page + 1)"
        ><ChevronRight :size="16"
      /></button>
      <button
        type="button"
        class="page-edge"
        aria-label="末页"
        title="末页"
        :disabled="disabled || page >= count"
        @click="go(count)"
        ><ChevronsRight :size="16"
      /></button>
    </div>
    <div class="page-jump"
      ><label
        >跳至<input
          v-model="draft"
          type="text"
          inputmode="numeric"
          aria-label="跳转页码"
          :aria-invalid="!!error"
          :aria-describedby="error ? errorID : undefined"
          :title="`输入 1–${count}，按 Enter 跳转`"
          :disabled="disabled || count <= 1"
          @input="error = ''"
          @keydown.enter.prevent.stop="jump"
          @keydown.esc.prevent.stop="
            draft = String(page);
            error = '';
          "
        />页</label
      ><button type="button" :disabled="disabled || count <= 1" @click="jump">跳转</button></div
    >
    <span v-if="error" :id="errorID" class="page-error" role="alert">{{ error }}</span>
  </nav>
</template>
<style scoped>
  .page-navigation {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    flex-wrap: wrap;
    gap: 10px 16px;
    min-width: 0;
    font-size: 12px;
    color: var(--steel);
  }
  .page-steps,
  .page-jump,
  .page-jump label {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .page-navigation button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 32px;
    min-height: 32px;
    padding: 5px 7px;
    border: 1px solid var(--line);
    border-radius: 7px;
    background: #fff;
    color: var(--steel);
    font-size: 12px;
    line-height: 20px;
  }
  .page-navigation button:hover:not(:disabled) {
    border-color: #89bca7;
    background: #edf7f0;
    color: #287452;
  }
  .page-navigation button:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }
  .page-readout {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    min-width: 72px;
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }
  .page-current {
    color: #287452;
    font-weight: 600;
  }
  .page-slash {
    color: var(--ash);
  }
  .page-jump {
    gap: 8px;
  }
  .page-jump label {
    flex-direction: row;
    gap: 8px;
    white-space: nowrap;
    font-size: 12px;
    font-weight: 400;
    margin: 0;
  }
  .page-jump input {
    width: 54px;
    min-width: 0;
    min-height: 32px;
    padding: 5px 6px;
    border: 1px solid var(--line);
    border-radius: 7px;
    background: #fff;
    color: var(--ink);
    font: inherit;
    text-align: center;
    font-variant-numeric: tabular-nums;
  }
  .page-jump input:disabled {
    background: var(--surface);
    opacity: 0.6;
  }
  .page-jump input[aria-invalid='true'] {
    border-color: #b14545;
  }
  .page-navigation button:focus-visible,
  .page-jump input:focus-visible {
    outline: 2px solid #287452;
    outline-offset: 2px;
  }
  .page-error {
    flex-basis: 100%;
    text-align: right;
    color: #b14545;
    font-size: 12px;
  }
  @media (max-width: 600px) {
    .page-navigation {
      justify-content: center;
      gap: 10px;
      width: 100%;
    }
    .page-steps {
      gap: 8px;
    }
    .page-navigation .page-edge {
      display: none;
    }
    .page-navigation button {
      min-width: 44px;
      min-height: 44px;
    }
    .page-jump input {
      min-height: 44px;
      width: 60px;
    }
    .page-error {
      text-align: center;
    }
  }
</style>
