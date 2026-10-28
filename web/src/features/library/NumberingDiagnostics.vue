<script setup lang="ts">
  import { errorText } from '../../app/errors';
  import { ref, watch } from 'vue';
  import { copyText } from '../../lib/clipboard/clipboard';
  const { message, diagnostics } = defineProps<{ message: string; diagnostics: string }>();
  const copyStatus = ref(''),
    copying = ref(false);
  let copySeq = 0;
  watch(
    () => [message, diagnostics],
    () => {
      copySeq++;
      copyStatus.value = '';
    }
  );
  async function copy() {
    if (copying.value) return;
    const seq = copySeq;
    copying.value = true;
    copyStatus.value = '';
    try {
      await copyText(message + '\n' + diagnostics);
      if (seq === copySeq) copyStatus.value = '已复制诊断';
    } catch (e) {
      if (seq === copySeq) copyStatus.value = errorText(e);
    } finally {
      copying.value = false;
    }
  }
</script>
<template>
  <details class="numbering-diagnostics"
    ><summary>查看诊断详情</summary><button :disabled="copying" @click="copy">复制诊断</button
    ><span role="status">{{ copyStatus }}</span
    ><pre>{{ diagnostics }}</pre>
  </details>
</template>
<style scoped>
  .numbering-diagnostics {
    margin: var(--space-12) 0;
  }
  .numbering-diagnostics summary {
    cursor: pointer;
    margin-bottom: var(--space-8);
  }
  .numbering-diagnostics span {
    margin-left: var(--space-8);
  }
  .numbering-diagnostics pre {
    max-height: 320px;
    overflow: auto;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    background: var(--surface);
    padding: var(--space-12);
    border: 1px solid var(--line);
    border-radius: var(--radius-8);
    font-size: var(--font-size-12);
  }
</style>
