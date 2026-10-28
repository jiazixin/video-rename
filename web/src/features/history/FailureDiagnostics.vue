<script setup lang="ts">
  import { formatKiB } from '../../lib/format/bytes';
  import { formatThousands } from '../../lib/format/number';
  import { errorText } from '../../app/errors';
  import { computed, ref } from 'vue';
  import { useApi } from '../../app/use-api';
  import { copyText } from '../../lib/clipboard/clipboard';
  const { api } = useApi();
  const { id, processing } = defineProps<{ id: number; processing?: boolean }>();
  const active = ref(''),
    data = ref<Record<string, string>>(),
    error = ref(''),
    busy = ref(false),
    copied = ref(false),
    copying = ref(false),
    copyError = ref('');
  let copySeq = 0;
  const parsed = computed(() => {
    try {
      return JSON.parse(data.value?.request || '{}');
    } catch {
      return {};
    }
  });
  const context = computed(() => parsed.value._local_context);
  const actualInput = computed(() => {
    try {
      const response: unknown = JSON.parse(data.value?.response || '{}');
      const usage =
        typeof response === 'object' && response !== null && 'usage' in response
          ? (response.usage as Record<string, unknown> | null)
          : null;
      const count = (key: string) => (typeof usage?.[key] === 'number' ? usage[key] : 0);
      if (typeof usage?.prompt_tokens === 'number') return usage.prompt_tokens;
      if (typeof usage?.input_tokens === 'number')
        return (
          usage.input_tokens +
          count('cache_creation_input_tokens') +
          count('cache_read_input_tokens')
        );
    } catch {
      // The raw response is not JSON; the token count is simply unknown.
    }
    return undefined;
  });
  async function toggle(kind: string) {
    active.value = active.value === kind ? '' : kind;
    copySeq++;
    copied.value = false;
    copyError.value = '';
    if (!active.value || data.value || busy.value) return;
    busy.value = true;
    error.value = '';
    try {
      data.value = await api<Record<string, string>>(`/logs/${id}/diagnostics`);
    } catch (e) {
      error.value = errorText(e);
    } finally {
      busy.value = false;
    }
  }
  async function copy() {
    if (copying.value) return;
    const seq = copySeq,
      text = data.value?.[active.value] || '';
    copying.value = true;
    copied.value = false;
    copyError.value = '';
    try {
      await copyText(text);
      if (seq === copySeq) copied.value = true;
    } catch (e) {
      if (seq === copySeq) copyError.value = errorText(e);
    } finally {
      copying.value = false;
    }
  }
  const titles: Record<string, string> = {
    processing: '媒体处理诊断（包含失败采样点与工具报错）',
    request: '请求体（图片数据省略、密钥脱敏）'
  };
  const emptyTexts: Record<string, string> = {
    processing: '未保存媒体诊断日志；如需排查，可重试后查看诊断。',
    request: '未记录请求体（尚未发起模型请求或请求内容未保存）。'
  };
  const title = computed(() => titles[active.value] ?? '响应体（密钥脱敏）');
  const emptyText = computed(
    () => emptyTexts[active.value] ?? '未记录响应体（服务未返回响应或响应内容未保存）。'
  );
</script>
<template>
  <div class="failure-diagnostics" role="group" aria-label="失败诊断"
    ><div class="diagnostic-buttons"
      ><button
        v-if="processing"
        :aria-expanded="active === 'processing'"
        @click="toggle('processing')"
        >抽帧 / 媒体诊断日志</button
      ><button :aria-expanded="active === 'request'" @click="toggle('request')">原始请求</button
      ><button :aria-expanded="active === 'response'" @click="toggle('response')"
        >原始响应</button
      ></div
    ><section v-if="active" class="diagnostic-content" aria-label="诊断内容"
      ><p v-if="busy">正在读取…</p><p v-else-if="error" role="alert">{{ error }}</p
      ><template v-else-if="data?.[active]"
        ><header
          ><span>{{ title }}</span
          ><button :disabled="copying" @click="copy">{{
            copied ? '已复制' : '复制'
          }}</button></header
        ><p v-if="copyError" role="alert">{{ copyError }}</p
        ><div v-if="active === 'request'" class="context-summary">
          <p v-if="actualInput !== undefined"
            ><strong>实际输入上下文：{{ formatThousands(actualInput) }} Token</strong>（{{
              actualInput
            }}
            Token，服务返回）</p
          >
          <template v-if="context"
            ><p
              >文字粗估：约 {{ formatThousands(context.text_tokens_estimate) }} Token · 图片
              {{ context.images }} 张 · 输出上限
              {{
                context.max_output_tokens
                  ? formatThousands(context.max_output_tokens) + ' Token'
                  : '未设置'
              }}</p
            ><p>请求体：{{ formatKiB(context.request_bytes) }}（含图片编码，与上下文 Token 不同）</p
            ><p class="help"
              >文字粗估不含图片 Token
              和协议开销，不能当作总上下文；总输入未知时无法判断是否超限。不同模型的分词和图片处理规则不同，1K
              = 1000 Token。</p
            ></template
          >
          <p v-else-if="actualInput === undefined" class="help"
            >此记录未保存上下文统计，且没有服务返回的输入用量；图片数据已省略，无法还原总输入
            Token。</p
          > </div
        ><pre tabindex="0">{{ data[active] }}</pre></template
      ><p v-else>{{ emptyText }}</p></section
    ></div
  >
</template>
<style scoped>
  .diagnostic-buttons button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-height: 34px;
    padding: var(--space-6) var(--space-12);
    gap: var(--space-6);
    border: 1px solid var(--control-line);
    border-radius: var(--radius-8);
    background: var(--palette-ffffff);
    color: var(--steel);
    font-size: var(--font-size-12);
    font-weight: var(--font-weight-500);
    line-height: var(--line-height-20);
  }
  .diagnostic-buttons button:hover:not(:disabled) {
    background: var(--control-hover);
    border-color: var(--control-line-hover);
    color: var(--ink);
  }

  .context-summary {
    margin-top: var(--space-10);
    padding: var(--space-8) var(--space-12);
    background: var(--palette-eef4f0);
    border-radius: var(--radius-5);
  }
  .context-summary p {
    margin: var(--space-5) 0;
  }
  .diagnostic-buttons {
    display: flex;
    gap: var(--space-8);
    flex-wrap: wrap;
  }
  .diagnostic-buttons button {
    font-size: var(--font-size-11);
    padding: var(--space-4) var(--space-10);
  }
  .diagnostic-buttons button[aria-expanded='true'] {
    border-color: var(--palette-619982);
    color: var(--palette-28684e);
  }
  .diagnostic-content {
    text-align: left;
    margin-top: var(--space-10);
    border: 1px solid var(--line);
    border-radius: var(--radius-6);
    padding: var(--space-12);
    background: var(--palette-f8faf9);
  }
  .diagnostic-content header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    color: var(--steel);
    font-size: var(--font-size-11);
  }
  .diagnostic-content pre {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    max-height: 360px;
    overflow: auto;
    font: 12px/1.6 monospace;
    margin: var(--space-10) 0 0;
  }
  .diagnostic-content p {
    font-size: var(--font-size-12);
  }
</style>
