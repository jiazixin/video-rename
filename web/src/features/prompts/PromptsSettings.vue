<script setup lang="ts">
  import { vFormErrors } from '../../app/form-errors';
  import { errorText } from '../../app/errors';
  import { useToast } from '../../app/use-toast';
  import { computed, nextTick, onMounted, ref } from 'vue';
  import { PenLine, Plus, RotateCcw, Save, Trash2, X } from '@lucide/vue';
  import { useApi } from '../../app/use-api';
  import { formatTime } from '../../lib/format/date';
  import { type PromptProfile } from '../../app/contracts';
  import ModalDialog from '../../components/ModalDialog.vue';
  import ModalTransition from '../../components/ModalTransition.vue';
  import './prompts.css';
  const { api, formError } = useApi();
  const toast = useToast();
  const fixed = ref(''),
    savedFixed = ref(''),
    defaultFixed = ref(''),
    rows = ref<PromptProfile[]>([]),
    promptsLoaded = ref(false),
    loading = ref(true),
    savingFixed = ref(false),
    error = ref('');
  const dirtyFixed = computed(() => fixed.value !== savedFixed.value);
  const failureKeywords = ref<string[]>([]),
    savedFailureKeywords = ref<string[]>([]),
    defaultFailureKeywords = ref<string[]>([]),
    failureKeywordsLoaded = ref(false),
    loadingFailureKeywords = ref(true),
    savingFailureKeywords = ref(false),
    failureKeywordsError = ref(''),
    keywordInput = ref(''),
    keywordInputError = ref(''),
    composingKeyword = ref(false),
    keywordInputElement = ref<HTMLInputElement>(),
    keywordListElement = ref<HTMLUListElement>();
  const dirtyFailureKeywords = computed(
    () => JSON.stringify(failureKeywords.value) !== JSON.stringify(savedFailureKeywords.value)
  );
  const defaultFailureKeywordsSelected = computed(
    () => JSON.stringify(failureKeywords.value) === JSON.stringify(defaultFailureKeywords.value)
  );
  const failureKeywordsBusy = computed(
    () =>
      !failureKeywordsLoaded.value || loadingFailureKeywords.value || savingFailureKeywords.value
  );
  const editing = ref(false),
    saving = ref(false),
    dialogError = ref(''),
    editId = ref(0),
    editETag = ref(''),
    edit = ref({ title: '', body: '' });
  const deleteTarget = ref<PromptProfile>(),
    deleteError = ref(''),
    deleting = ref(false);
  async function load(preserveDrafts = false) {
    loading.value = true;
    loadingFailureKeywords.value = true;
    error.value = '';
    try {
      const page = await api<{
        fixed_prompt: string;
        default_fixed_prompt: string;
        failure_keywords: string[];
        default_failure_keywords: string[];
        prompts: PromptProfile[];
      }>('/prompts');
      const loadedKeywords = [...page.failure_keywords];
      const loadedDefaultKeywords = [...page.default_failure_keywords];
      if (!preserveDrafts || !promptsLoaded.value) {
        fixed.value = page.fixed_prompt;
        savedFixed.value = page.fixed_prompt;
      }
      if (!preserveDrafts || !failureKeywordsLoaded.value) {
        failureKeywords.value = loadedKeywords;
        savedFailureKeywords.value = [...loadedKeywords];
        keywordInput.value = '';
        keywordInputError.value = '';
        failureKeywordsError.value = '';
      }
      defaultFailureKeywords.value = loadedDefaultKeywords;
      failureKeywordsLoaded.value = true;
      defaultFixed.value = page.default_fixed_prompt;
      rows.value = page.prompts;
      promptsLoaded.value = true;
    } catch (e) {
      error.value = errorText(e);
    } finally {
      loading.value = false;
      loadingFailureKeywords.value = false;
    }
  }
  async function saveFixed() {
    if (savingFixed.value || !dirtyFixed.value) return;
    savingFixed.value = true;
    error.value = '';
    try {
      const out = await api<{ fixed_prompt: string }>('/prompts/fixed', {
        method: 'PUT',
        body: {
          fixed_prompt: fixed.value
        }
      });
      fixed.value = out.fixed_prompt;
      savedFixed.value = out.fixed_prompt;
      toast.show('固定提示词已保存。之后创建的运行会把它附加在所选提示词末尾发送。');
    } catch (e) {
      error.value = errorText(e);
    } finally {
      savingFixed.value = false;
    }
  }
  function addFailureKeyword() {
    if (failureKeywordsBusy.value) return;
    const keyword = keywordInput.value.trim();
    if (!keyword) {
      keywordInputError.value = '请输入关键词或完整句子，不能只包含空白。';
    } else if (Array.from(keyword).length > 200) {
      keywordInputError.value = '每条关键词或句子不能超过 200 个字符。';
    } else if (
      failureKeywords.value.some((existing) => existing.toLowerCase() === keyword.toLowerCase())
    ) {
      keywordInputError.value = '这个关键词已存在，英文不区分大小写。';
    } else if (failureKeywords.value.length >= 100) {
      keywordInputError.value = '最多添加 100 条关键词，请先删除不需要的关键词。';
    } else {
      failureKeywords.value = [...failureKeywords.value, keyword];
      keywordInput.value = '';
      keywordInputError.value = '';
    }
    keywordInputElement.value?.focus();
  }
  function addFailureKeywordOnEnter(event: KeyboardEvent) {
    if (event.isComposing || composingKeyword.value) return;
    event.preventDefault();
    addFailureKeyword();
  }
  async function removeFailureKeyword(index: number) {
    if (failureKeywordsBusy.value) return;
    failureKeywords.value = failureKeywords.value.filter((_, i) => i !== index);
    keywordInputError.value = '';
    await nextTick();
    const buttons = keywordListElement.value?.querySelectorAll<HTMLButtonElement>('button');
    if (buttons?.length) buttons[Math.min(index, buttons.length - 1)]?.focus();
    else keywordInputElement.value?.focus();
  }
  function restoreFailureKeywords() {
    if (failureKeywordsBusy.value) return;
    failureKeywords.value = [...defaultFailureKeywords.value];
    keywordInput.value = '';
    keywordInputError.value = '';
    failureKeywordsError.value = '';
  }
  async function saveFailureKeywords() {
    if (failureKeywordsBusy.value || !dirtyFailureKeywords.value) return;
    savingFailureKeywords.value = true;
    failureKeywordsError.value = '';
    try {
      const out = await api<{ failure_keywords: string[] }>('/prompts/failure-keywords', {
        method: 'PUT',
        body: {
          failure_keywords: [...failureKeywords.value]
        }
      });
      failureKeywords.value = [...out.failure_keywords];
      savedFailureKeywords.value = [...out.failure_keywords];
      toast.show('失败关键词已保存，对之后创建的批次生效。');
    } catch (e) {
      failureKeywordsError.value = errorText(e);
    } finally {
      savingFailureKeywords.value = false;
    }
  }
  function open(p?: PromptProfile) {
    formError.value = undefined;
    editId.value = p?.id || 0;
    editETag.value = p?.etag || '';
    edit.value = { title: p?.title || '', body: p?.body || '' };
    dialogError.value = '';
    editing.value = true;
  }
  async function save() {
    if (saving.value) return;
    saving.value = true;
    dialogError.value = '';
    try {
      await api<unknown>(editId.value ? `/prompts/${editId.value}` : '/prompts', {
        method: editId.value ? 'PATCH' : 'POST',
        headers: editId.value ? { 'If-Match': editETag.value } : {},
        body: edit.value
      });
      editing.value = false;
      toast.show('提示词已保存');
      await load(true);
    } catch (e) {
      dialogError.value = errorText(e);
    } finally {
      saving.value = false;
    }
  }
  async function remove() {
    if (!deleteTarget.value || deleting.value) return;
    deleting.value = true;
    deleteError.value = '';
    try {
      await api<unknown>(`/prompts/${deleteTarget.value.id}`, {
        method: 'DELETE',
        headers: { 'If-Match': deleteTarget.value.etag },
        body: {}
      });
      deleteTarget.value = undefined;
      toast.show('提示词已删除');
      await load(true);
    } catch (e) {
      deleteError.value = errorText(e);
    } finally {
      deleting.value = false;
    }
  }
  onMounted(load);
</script>
<template>
  <section v-form-errors="formError" class="prompts-page">
    <div v-if="error" class="notice error" role="alert"
      >{{ error }}<button @click="load(true)">重试</button></div
    >
    <p v-if="loading" role="status">正在加载提示词…</p>
    <template v-else-if="promptsLoaded">
      <section class="panel processing-card prompts-fixed-card"
        ><div><h2>固定提示词</h2><p class="help">自动附加到命名提示词末尾，统一输出格式。</p></div>
        <div class="prompts-fixed-controls">
          <textarea
            v-model="fixed"
            name="fixed_prompt"
            aria-label="固定提示词"
            :disabled="savingFixed"
            rows="9"
            maxlength="16000"
            required
          /><footer class="processing-footer"
            ><p class="help">保存后对新批次生效；恢复默认后也需保存。</p
            ><div class="prompts-fixed-actions"
              ><button
                type="button"
                :disabled="savingFixed || fixed === defaultFixed"
                @click="fixed = defaultFixed"
                ><RotateCcw :size="16" />恢复默认</button
              ><button class="primary" :disabled="savingFixed || !dirtyFixed" @click="saveFixed"
                ><Save :size="16" />{{ savingFixed ? '正在保存…' : '保存固定提示词' }}</button
              ></div
            ></footer
          ></div
        ></section
      >
      <section
        class="panel processing-card prompts-failure-card"
        aria-labelledby="failure-keywords-heading"
        ><div
          ><h2 id="failure-keywords-heading">标题失败关键词</h2
          ><p class="help">生成标题包含任意一条关键词或句子，就判定命名失败；英文不区分大小写。</p
          ><p class="help">普通标题也可能包含这些词，若出现误判，可删除对应关键词。</p></div
        >
        <div
          class="prompts-failure-controls"
          :aria-busy="loadingFailureKeywords || savingFailureKeywords"
          ><div
            v-if="failureKeywordsError"
            class="notice error failure-keywords-error"
            role="alert"
            >{{ failureKeywordsError }}</div
          ><template v-if="failureKeywordsLoaded"
            ><div class="failure-keyword-summary"
              ><span class="subtle">已添加 {{ failureKeywords.length }} / 100 条</span
              ><span v-if="dirtyFailureKeywords" class="failure-keywords-dirty" role="status"
                >有未保存的修改</span
              ></div
            ><ul
              v-if="failureKeywords.length"
              ref="keywordListElement"
              class="failure-keywords-list"
              aria-label="失败关键词列表"
              ><li
                v-for="(keyword, index) in failureKeywords"
                :key="keyword"
                class="failure-keyword"
                ><span>{{ keyword }}</span
                ><button
                  type="button"
                  class="failure-keyword-remove"
                  :aria-label="`删除失败关键词 ${keyword}`"
                  :disabled="failureKeywordsBusy"
                  @click="removeFailureKeyword(index)"
                  ><X :size="14" aria-hidden="true" /></button></li></ul
            ><p v-else class="help failure-keywords-empty" role="status">{{
              dirtyFailureKeywords
                ? '当前草稿没有关键词，保存后将不检查失败关键词。'
                : '未设置失败关键词，当前不检查失败关键词。'
            }}</p></template
          ><div class="failure-keyword-add"
            ><label for="failure-keyword-input"
              >新增失败关键词<input
                id="failure-keyword-input"
                ref="keywordInputElement"
                v-model="keywordInput"
                name="failure_keywords"
                aria-label="新增失败关键词"
                :aria-invalid="!!keywordInputError"
                :aria-describedby="
                  keywordInputError
                    ? 'failure-keyword-help failure-keyword-error'
                    : 'failure-keyword-help'
                "
                :disabled="failureKeywordsBusy"
                placeholder="输入一个词或完整句子"
                @input="keywordInputError = ''"
                @compositionstart="composingKeyword = true"
                @compositionend="composingKeyword = false"
                @keydown.enter="addFailureKeywordOnEnter" /></label
            ><button type="button" :disabled="failureKeywordsBusy" @click="addFailureKeyword"
              ><Plus :size="16" aria-hidden="true" />添加关键词</button
            ></div
          ><p id="failure-keyword-help" class="help failure-keyword-help"
            >每次添加一条，支持完整句子和 Enter 添加；每条 1–200 个字符，最多 100 条。</p
          ><p
            v-if="keywordInputError"
            id="failure-keyword-error"
            class="error-text failure-keyword-input-error"
            role="alert"
            >{{ keywordInputError }}</p
          ><footer class="processing-footer"
            ><p class="help">保存后对新批次生效，已有批次使用创建时的设置；恢复默认后也需保存。</p
            ><div class="prompts-failure-actions"
              ><button
                type="button"
                aria-label="恢复默认失败关键词"
                :disabled="failureKeywordsBusy || defaultFailureKeywordsSelected"
                @click="restoreFailureKeywords"
                ><RotateCcw :size="16" aria-hidden="true" />恢复默认</button
              ><button
                type="button"
                class="primary"
                aria-label="保存失败关键词"
                :disabled="failureKeywordsBusy || !dirtyFailureKeywords"
                @click="saveFailureKeywords"
                ><Save :size="16" aria-hidden="true" />{{
                  savingFailureKeywords ? '正在保存…' : '保存失败关键词'
                }}</button
              ></div
            ></footer
          ></div
        ></section
      >
      <section class="panel prompts-library"
        ><header class="prompts-library-head"
          ><div><h2>提示词库</h2><p class="help">在模型配置中选择使用，修改后对新批次生效。</p></div
          ><button class="primary" @click="open()"><Plus :size="17" />新增提示词</button></header
        >
        <div v-if="rows.length" class="prompt-list"
          ><article v-for="p in rows" :key="p.id" class="panel prompt-card" :aria-label="p.title"
            ><header
              ><div class="prompt-card-heading"
                ><h2>{{ p.title }}</h2
                ><span class="subtle">更新于 {{ formatTime(p.updated) }}</span></div
              ><div class="prompt-card-actions"
                ><button :aria-label="`编辑提示词 ${p.title}`" @click="open(p)"
                  ><PenLine :size="15" />编辑</button
                ><button
                  :aria-label="`删除提示词 ${p.title}`"
                  class="danger-button"
                  @click="
                    deleteTarget = p;
                    deleteError = '';
                  "
                  ><Trash2 :size="15" />删除</button
                ></div
              ></header
            ><p class="prompt-body">{{ p.body }}</p></article
          ></div
        >
        <p v-else class="help">暂无提示词，点击“新增提示词”创建。</p></section
      >
    </template>
    <ModalTransition
      ><ModalDialog
        v-if="editing"
        :title="editId ? '编辑提示词' : '新增提示词'"
        @close="!saving && (editing = false)"
        ><form v-form-errors="formError" class="prompt-form" @submit.prevent="save"
          ><label
            >提示词名称<input
              v-model="edit.title"
              name="title"
              aria-label="提示词名称"
              required
              maxlength="80"
              placeholder="例如：旅行视频标题" /></label
          ><label
            >提示词内容<textarea
              v-model="edit.body"
              name="body"
              aria-label="提示词内容"
              required
              rows="10"
              maxlength="16000"
            /></label
          ><div v-if="dialogError" role="alert" class="notice error">{{ dialogError }}</div
          ><div class="modal-actions"
            ><button type="button" :disabled="saving" @click="editing = false">取消</button
            ><button class="primary" :disabled="saving">{{
              saving ? '正在保存…' : '保存提示词'
            }}</button></div
          ></form
        ></ModalDialog
      ></ModalTransition
    >
    <ModalTransition
      ><ModalDialog
        v-if="deleteTarget"
        title="删除提示词"
        @close="!deleting && (deleteTarget = undefined)"
        ><p>确定删除“{{ deleteTarget.title }}”？</p
        ><p class="help">删除不可恢复；被模型配置引用时，需先更换提示词。</p
        ><div v-if="deleteError" role="alert" class="notice error">{{ deleteError }}</div
        ><div class="modal-actions"
          ><button :disabled="deleting" @click="deleteTarget = undefined">取消</button
          ><button class="cancel-run" :disabled="deleting" @click="remove"
            ><Trash2 :size="16" />{{ deleting ? '正在删除…' : '确认删除' }}</button
          ></div
        ></ModalDialog
      ></ModalTransition
    >
  </section>
</template>
