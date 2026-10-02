<script setup lang="ts">
  import { computed, onMounted, ref, watch } from 'vue';
  import { PenLine, Plus, RotateCcw, Save, Trash2 } from 'lucide-vue-next';
  import { api, time, type PromptProfile } from '../../app/api';
  import Modal from '../../components/Modal.vue';
  import './prompts.css';
  const fixed = ref(''),
    savedFixed = ref(''),
    defaultFixed = ref(''),
    rows = ref<PromptProfile[]>([]),
    loading = ref(true),
    savingFixed = ref(false),
    error = ref(''),
    fixedMessage = ref('');
  const dirtyFixed = computed(() => fixed.value !== savedFixed.value);
  watch(fixed, () => {
    fixedMessage.value = '';
  });
  const editing = ref(false),
    saving = ref(false),
    dialogError = ref(''),
    editId = ref(0),
    editVersion = ref(0),
    edit = ref({ title: '', body: '' });
  const deleteTarget = ref<PromptProfile>(),
    deleteError = ref(''),
    deleting = ref(false);
  async function load(preserveFixed = false) {
    loading.value = true;
    error.value = '';
    try {
      const page = await api<{
        fixed_prompt: string;
        default_fixed_prompt: string;
        prompts: PromptProfile[];
      }>('/prompts');
      if (!preserveFixed) {
        fixed.value = page.fixed_prompt;
        savedFixed.value = page.fixed_prompt;
      }
      defaultFixed.value = page.default_fixed_prompt;
      rows.value = page.prompts;
    } catch (e) {
      error.value = (e as Error).message;
    } finally {
      loading.value = false;
    }
  }
  async function saveFixed() {
    if (savingFixed.value || !dirtyFixed.value) return;
    savingFixed.value = true;
    error.value = '';
    fixedMessage.value = '';
    try {
      const out = await api<{ fixed_prompt: string }>('/prompts/fixed', 'PUT', {
        fixed_prompt: fixed.value
      });
      fixed.value = out.fixed_prompt;
      savedFixed.value = out.fixed_prompt;
      fixedMessage.value = '固定提示词已保存。之后创建的运行会把它附加在所选提示词末尾发送。';
    } catch (e) {
      error.value = (e as Error).message;
    } finally {
      savingFixed.value = false;
    }
  }
  function open(p?: PromptProfile) {
    editId.value = p?.id || 0;
    editVersion.value = p?.version || 0;
    edit.value = { title: p?.title || '', body: p?.body || '' };
    dialogError.value = '';
    editing.value = true;
  }
  async function save() {
    if (saving.value) return;
    saving.value = true;
    dialogError.value = '';
    try {
      await api(
        editId.value ? `/prompts/${editId.value}` : '/prompts',
        editId.value ? 'PATCH' : 'POST',
        { ...edit.value, version: editVersion.value }
      );
      editing.value = false;
      await load(true);
    } catch (e) {
      dialogError.value = (e as Error).message;
    } finally {
      saving.value = false;
    }
  }
  async function remove() {
    if (!deleteTarget.value || deleting.value) return;
    deleting.value = true;
    deleteError.value = '';
    try {
      await api(`/prompts/${deleteTarget.value.id}`, 'DELETE', {
        version: deleteTarget.value.version
      });
      deleteTarget.value = undefined;
      await load(true);
    } catch (e) {
      deleteError.value = (e as Error).message;
    } finally {
      deleting.value = false;
    }
  }
  onMounted(load);
</script>
<template>
  <section class="prompts-page">
    <div v-if="error" class="notice error" role="alert"
      >{{ error }}<button @click="load()">重试</button></div
    >
    <p v-if="loading" role="status">正在加载提示词…</p>
    <template v-else>
      <section class="panel processing-card prompts-fixed-card"
        ><div><h2>固定提示词</h2><p class="help">自动附加到命名提示词末尾，统一输出格式。</p></div>
        <div class="prompts-fixed-controls">
          <textarea
            v-model="fixed"
            aria-label="固定提示词"
            :disabled="savingFixed"
            rows="9"
            maxlength="16000"
            required
          /><div v-if="fixedMessage" class="notice success" role="status">{{ fixedMessage }}</div
          ><footer class="processing-footer"
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
      <section class="panel prompts-library"
        ><header class="prompts-library-head"
          ><div><h2>提示词库</h2><p class="help">在模型配置中选择使用，修改后对新批次生效。</p></div
          ><button class="primary" @click="open()"><Plus :size="17" />新增提示词</button></header
        >
        <div v-if="rows.length" class="prompt-list"
          ><article v-for="p in rows" :key="p.id" class="panel prompt-card"
            ><header
              ><div class="prompt-card-heading"
                ><h2>{{ p.title }}</h2
                ><span class="subtle">更新于 {{ time(p.updated) }}</span></div
              ><div class="prompt-card-actions"
                ><button :aria-label="`编辑提示词 ${p.title}`" @click="open(p)"
                  ><PenLine :size="15" />编辑</button
                ><button
                  :aria-label="`删除提示词 ${p.title}`"
                  class="model-delete"
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
  </section>
  <Modal
    v-if="editing"
    :title="editId ? '编辑提示词' : '新增提示词'"
    @close="!saving && (editing = false)"
    ><form @submit.prevent="save" class="prompt-form"
      ><label
        >提示词名称<input
          v-model="edit.title"
          aria-label="提示词名称"
          required
          maxlength="80"
          placeholder="例如：旅行视频标题" /></label
      ><label
        >提示词内容<textarea
          v-model="edit.body"
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
    ></Modal
  >
  <Modal v-if="deleteTarget" title="删除提示词" @close="!deleting && (deleteTarget = undefined)"
    ><p>确定删除“{{ deleteTarget.title }}”？</p
    ><p class="help">删除不可恢复；被模型配置引用时，需先更换提示词。</p
    ><div v-if="deleteError" role="alert" class="notice error">{{ deleteError }}</div
    ><div class="modal-actions"
      ><button :disabled="deleting" @click="deleteTarget = undefined">取消</button
      ><button class="cancel-run" :disabled="deleting" @click="remove"
        ><Trash2 :size="16" />{{ deleting ? '正在删除…' : '确认删除' }}</button
      ></div
    ></Modal
  >
</template>
