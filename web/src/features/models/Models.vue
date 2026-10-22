<script setup lang="ts">
  import { onMounted, onUnmounted, ref, reactive, watch, computed } from 'vue';
  import {
    Plus,
    Settings2,
    PenLine,
    Search,
    Check,
    Server,
    Copy,
    Trash2,
    ScanEye
  } from '@lucide/vue';
  import { api } from '../../app/api';
  import { formatTime } from '../../lib/date/format';
  import { type ModelProfile, type ProbeResult, type PromptProfile } from '../../app/contracts';
  import Modal from '../../components/Modal.vue';
  import ModalTransition from '../../components/ModalTransition.vue';
  import SelectField from '../../components/SelectField.vue';
  import './models.css';
  type Preset = { id: string; name: string; base_url: string; protocol: string };
  const emit = defineEmits<{ changed: [] }>();
  const rows = ref<ModelProfile[]>([]),
    presets = ref<Preset[]>([]),
    prompts = ref<PromptProfile[]>([]),
    loading = ref(false),
    error = ref(''),
    editing = ref(false),
    selected = ref<ModelProfile>(),
    saving = ref(false),
    dialogError = ref(''),
    settingDefault = ref(false);
  const initial = () => ({
    title: '',
    prompt_id: 0,
    provider: 'custom',
    protocol: 'openai',
    base_url: '',
    model: '',
    timeout: '180s',
    temperature: 0.7,
    max_tokens: 1024,
    workers: 2,
    omit_temperature: false,
    thinking: 'default',
    thinking_adapter: 'auto',
    thinking_effort: 'low',
    thinking_budget: 1024
  });
  const form = reactive(initial()),
    apiKey = ref(''),
    clearKey = ref(false),
    detected = ref<{ id: string; name: string }[]>([]),
    detectError = ref(''),
    detectNote = ref(''),
    detecting = ref(false);
  const deleteTarget = ref<ModelProfile>(),
    actionBusy = ref(false),
    deleteError = ref('');
  async function duplicate(p: ModelProfile) {
    if (actionBusy.value) return;
    actionBusy.value = true;
    error.value = '';
    try {
      const copy = await api<ModelProfile>(`/model-configs/${p.id}/copy`, 'POST', {});
      await load();
      emit('changed');
      open(copy);
    } catch (e) {
      error.value = (e as Error).message;
    } finally {
      actionBusy.value = false;
    }
  }
  async function remove() {
    if (!deleteTarget.value || actionBusy.value) return;
    actionBusy.value = true;
    deleteError.value = '';
    try {
      await api(`/model-configs/${deleteTarget.value.id}`, 'DELETE', {
        version: deleteTarget.value.version
      });
      deleteTarget.value = undefined;
      await load();
      emit('changed');
    } catch (e) {
      deleteError.value = (e as Error).message;
    } finally {
      actionBusy.value = false;
    }
  }
  const probing = ref(false),
    probeResult = ref<ProbeResult>(),
    probeError = ref(''),
    probeTimeout = ref(5);
  let probeController: AbortController | undefined;
  let probeSeq = 0;
  watch(
    () => JSON.stringify(form) + apiKey.value + clearKey.value + probeTimeout.value,
    () => {
      probeController?.abort();
      probeSeq++;
      probing.value = false;
      probeResult.value = undefined;
      probeError.value = '';
    }
  );
  async function testVision() {
    if (probing.value) return;
    const controller = new AbortController(),
      seconds = probeTimeout.value,
      n = ++probeSeq;
    probeController = controller;
    const timer = setTimeout(() => controller.abort(), seconds * 1000);
    probing.value = true;
    probeError.value = '';
    probeResult.value = undefined;
    try {
      const result = await api<ProbeResult>(
        '/model-configs/test-vision',
        'POST',
        { ...payload(), probe_timeout_seconds: seconds },
        controller.signal
      );
      if (n === probeSeq) probeResult.value = result;
    } catch (e) {
      if (n === probeSeq)
        probeError.value = controller.signal.aborted
          ? `图片识别测试超时（${seconds}s），请重试或增加测试超时`
          : (e as Error).message;
    } finally {
      clearTimeout(timer);
      if (n === probeSeq) {
        probing.value = false;
        probeController = undefined;
      }
    }
  }
  let detectController: AbortController | undefined;
  let detectSeq = 0;
  onUnmounted(() => {
    detectController?.abort();
    probeController?.abort();
  });
  watch(
    () => [form.base_url, form.provider, form.protocol, apiKey.value, clearKey.value],
    () => {
      detectController?.abort();
      detectSeq++;
      detected.value = [];
      detectError.value = '';
      detectNote.value = '';
      detecting.value = false;
    }
  );
  async function load() {
    loading.value = true;
    try {
      const [profiles, meta, library] = await Promise.all([
        api<ModelProfile[]>('/model-configs'),
        api<{ presets: Preset[] }>('/model-presets'),
        api<{ fixed_prompt: string; prompts: PromptProfile[] }>('/prompts')
      ]);
      rows.value = profiles;
      presets.value = meta.presets;
      prompts.value = library.prompts;
      error.value = '';
    } catch (e) {
      error.value = (e as Error).message;
    } finally {
      loading.value = false;
    }
  }
  function open(p?: ModelProfile) {
    selected.value = p;
    Object.assign(form, initial(), p || {});
    form.thinking = form.thinking || 'default';
    form.thinking_adapter = form.thinking_adapter || 'auto';
    form.thinking_effort = form.thinking_effort || 'low';
    form.thinking_budget = form.thinking_budget || 1024;
    apiKey.value = '';
    clearKey.value = false;
    dialogError.value = '';
    detected.value = [];
    detectError.value = '';
    detectNote.value = '';
    detectSeq++;
    editing.value = true;
  }
  function chooseProvider() {
    form.thinking = 'default';
    form.thinking_adapter = 'auto';
    const preset = presets.value.find((p) => p.id === form.provider);
    if (preset) {
      form.base_url = preset.base_url;
      form.protocol = preset.protocol;
    }
    form.model = '';
    apiKey.value = '';
    form.omit_temperature = ['openai', 'claude'].includes(form.provider);
    if (form.protocol === 'anthropic' && form.temperature > 1) form.temperature = 1;
  }
  function payload() {
    return {
      ...form,
      id: selected.value?.id || 0,
      version: selected.value?.version || 0,
      ...(clearKey.value ? { api_key: '' } : apiKey.value ? { api_key: apiKey.value } : {})
    };
  }
  const selectedPrompt = computed(() => prompts.value.find((p) => p.id === form.prompt_id));
  async function save() {
    if (!form.prompt_id) {
      dialogError.value = '请选择提示词；可先在系统设置的 Prompt配置 中新增。';
      return;
    }
    saving.value = true;
    dialogError.value = '';
    try {
      await api(
        selected.value ? `/model-configs/${selected.value.id}` : '/model-configs',
        selected.value ? 'PATCH' : 'POST',
        payload()
      );
      editing.value = false;
      apiKey.value = '';
      await load();
      emit('changed');
    } catch (e) {
      dialogError.value = (e as Error).message;
    } finally {
      saving.value = false;
    }
  }
  async function detect() {
    detectController?.abort();
    const controller = new AbortController(),
      n = ++detectSeq;
    detectController = controller;
    const timer = setTimeout(() => controller.abort(), 5000);
    detecting.value = true;
    detected.value = [];
    detectError.value = '';
    detectNote.value = '';
    try {
      const data = await api<{ models: { id: string; name: string }[]; truncated: boolean }>(
        '/model-configs/detect',
        'POST',
        payload(),
        controller.signal
      );
      if (n !== detectSeq) return;
      detected.value = data.models;
      detectNote.value = data.models.length
        ? `检测到 ${data.models.length} 个模型${data.truncated ? '（已达到列表上限）' : ''}`
        : '服务没有返回模型，可手动填写';
    } catch (e) {
      if (n === detectSeq)
        detectError.value = controller.signal.aborted
          ? '模型列表检测超时（5s），请重试或检查服务'
          : (e as Error).message;
    } finally {
      clearTimeout(timer);
      if (n === detectSeq) {
        detecting.value = false;
        detectController = undefined;
      }
    }
  }
  async function setDefault(p: ModelProfile) {
    if (p.is_default || settingDefault.value) return;
    settingDefault.value = true;
    try {
      await api(`/model-configs/${p.id}/default`, 'POST', {});
      await load();
      emit('changed');
    } catch (e) {
      error.value = (e as Error).message;
    } finally {
      settingDefault.value = false;
    }
  }
  function close() {
    if (saving.value) return;
    detectController?.abort();
    probeController?.abort();
    editing.value = false;
    apiKey.value = '';
    probeSeq++;
    probing.value = false;
    probeResult.value = undefined;
    probeError.value = '';
    detectSeq++;
    detecting.value = false;
  }
  const thinkingAdapters = computed(() =>
    form.protocol === 'anthropic'
      ? [
          { value: 'adaptive', label: 'Claude 自适应思考' },
          { value: 'budget', label: 'Claude 固定思考预算' }
        ]
      : [
          { value: 'thinking', label: 'DeepSeek / 智谱 · thinking' },
          { value: 'effort', label: 'OpenAI / Ollama · reasoning_effort' },
          { value: 'llama_cpp', label: 'llama.cpp · 思考预算' },
          { value: 'template', label: '本地模板 · enable_thinking' }
        ]
  );
  const manualClaude = computed(
    () =>
      form.provider === 'claude' &&
      !/4[.-][678]|claude-(opus|sonnet)-5|mythos|fable/i.test(form.model)
  );
  const isLlama = computed(
    () =>
      form.provider === 'custom' &&
      form.protocol === 'openai' &&
      form.thinking_adapter === 'llama_cpp'
  );
  function changeThinkingAdapter() {
    form.thinking_budget = isLlama.value ? 512 : 1024;
  }
  const showBudget = computed(
    () =>
      form.thinking === 'enabled' &&
      (isLlama.value || form.thinking_adapter === 'budget' || manualClaude.value)
  );
  const thinkingName = (mode: string) =>
    mode === 'enabled' ? '开启' : mode === 'disabled' ? '关闭' : '模型默认';
  const providerName = (id: string) => presets.value.find((p) => p.id === id)?.name || id;
  onMounted(load);
</script>
<template>
  <section
    ><div class="model-toolbar"
      ><div
        ><span class="eyebrow">模型服务</span><h2>命名使用的视觉模型</h2
        ><p class="help">管理服务连接、生成参数与提示词，选择一套配置作为全局默认。</p></div
      ><button class="primary" @click="open()"><Plus :size="17" />新增配置</button></div
    ><div v-if="error" class="notice error" role="alert"
      >{{ error }}<button @click="load">重试</button></div
    >
    <div class="model-grid"
      ><article v-for="p in rows" :key="p.id" class="panel model-card"
        ><header
          ><div class="model-title"
            ><input
              type="radio"
              name="default-model"
              :aria-label="`设为全局默认 ${p.title}`"
              :checked="p.is_default"
              :disabled="settingDefault || !p.ready"
              @change="setDefault(p)"
            /><div
              ><h2>{{ p.title }}</h2
              ><div class="model-labels"
                ><span class="model-provider">{{ providerName(p.provider) }}</span
                ><span v-if="p.is_default" class="badge processed">全局默认</span
                ><span v-if="!p.ready" class="model-incomplete">待完善</span></div
              ></div
            ></div
          ><button :aria-label="`编辑模型配置 ${p.title}`" @click="open(p)"
            ><PenLine :size="15" />{{ p.ready ? '编辑' : '配置' }}</button
          ></header
        >
        <div class="model-endpoint"
          ><Server :size="17" /><div
            ><strong>{{ p.model || '尚未选择模型' }}</strong
            ><span :title="p.base_url">{{ p.base_url || '请填写服务地址与模型' }}</span></div
          ></div
        ><div class="model-metrics"
          ><span
            >温度 <b>{{ p.omit_temperature ? '模型默认' : p.temperature }}</b></span
          ><span
            >输出上限 <b>{{ p.max_tokens }}</b></span
          ><span
            >思考 <b>{{ thinkingName(p.thinking) }}</b></span
          ><span
            >并发 <b>{{ p.workers || 2 }}</b></span
          ><span
            >密钥 <b>{{ p.has_api_key ? '已设置' : '未设置' }}</b></span
          ></div
        ><details class="model-prompt"
          ><summary>命名提示词 · {{ p.prompt_title || '未选择' }}</summary
          ><p>{{ p.body }}</p></details
        ><footer class="model-card-footer"
          ><span>更新于 {{ formatTime(p.updated) }}</span
          ><div class="model-card-actions"
            ><button
              :disabled="actionBusy"
              :aria-label="`复制模型配置 ${p.title}`"
              @click="duplicate(p)"
              ><Copy :size="14" />复制</button
            ><button
              :disabled="actionBusy"
              :aria-label="`删除模型配置 ${p.title}`"
              @click="
                deleteTarget = p;
                deleteError = '';
              "
              class="model-delete"
              ><Trash2 :size="14" />删除</button
            ></div
          ></footer
        ></article
      ></div
    >
    <div v-if="!rows.length" class="panel empty"
      ><Settings2 :size="32" /><h3>{{ loading ? '正在读取配置' : '还没有模型配置' }}</h3
      ><p>添加服务地址、模型并选择提示词后即可运行。</p></div
    ><p class="help"
      >圆点选中项为全局默认。已有计划保留原选择，已有批次的并发数跟随所选模型配置，其他参数保留创建时的快照。</p
    >
    <ModalTransition
      ><Modal v-if="editing" :title="selected ? '编辑模型配置' : '新增模型配置'" wide @close="close"
        ><form @submit.prevent="save" class="model-form"
          ><label
            >配置名称<input
              v-model="form.title"
              aria-label="配置名称"
              required
              maxlength="80"
              placeholder="例如：旅行视频 · 本地模型" /></label
          ><div class="form-grid"
            ><label
              >服务类型<SelectField
                v-model="form.provider"
                label="服务类型"
                :options="presets.map((p) => ({ value: p.id, label: p.name }))"
                @change="chooseProvider" /></label
            ><label v-if="form.provider === 'custom'"
              >接口协议<SelectField
                v-model="form.protocol"
                label="接口协议"
                @change="
                  form.thinking = 'default';
                  form.thinking_adapter = 'auto';
                "
                :options="[
                  { value: 'openai', label: 'OpenAI 兼容' },
                  { value: 'anthropic', label: 'Anthropic Messages' }
                ]" /></label
          ></div>
          <label
            >服务 URL<input
              v-model="form.base_url"
              aria-label="服务 URL"
              type="url"
              required
              maxlength="2048"
              placeholder="http://127.0.0.1:8080/v1"
          /></label>
          <label
            >API 密钥<input
              v-model="apiKey"
              aria-label="API 密钥"
              type="password"
              autocomplete="new-password"
              :disabled="clearKey"
              maxlength="8192"
              :placeholder="
                selected?.has_api_key ? '已保存，留空保持原密钥' : '本地无鉴权服务可留空'
              " /></label
          ><label v-if="selected?.has_api_key" class="check-label"
            ><input type="checkbox" role="switch" v-model="clearKey" />清除已保存密钥</label
          >
          <div class="model-detect"
            ><label
              >模型名称<input
                v-model="form.model"
                aria-label="模型名称"
                required
                maxlength="256"
                placeholder="输入模型 ID，或检测后选择" /></label
            ><button type="button" :disabled="detecting || !form.base_url" @click="detect"
              ><Search :size="15" />{{ detecting ? '检测中…' : '检测可用模型' }}</button
            ></div
          ><div v-if="detected.length" class="detected-models"
            ><SelectField
              :model-value="form.model"
              label="检测到的模型"
              :options="
                detected.map((m) => ({ value: m.id, label: m.name ? `${m.id} · ${m.name}` : m.id }))
              "
              @update:model-value="form.model = String($event)" /></div
          ><p v-if="detectNote" class="model-detect-note" role="status"
            ><Check :size="14" />{{ detectNote }}</p
          ><div v-if="detectError" class="notice error" role="alert">{{ detectError }}</div
          ><p class="help"
            >检测仅获取服务返回的模型列表，请选择支持图片输入的模型。URL 填 API 基础地址，不包含
            /chat/completions 或 /messages。</p
          >
          <div class="vision-probe"
            ><div class="vision-probe-controls"
              ><span class="probe-timeout"
                ><span class="probe-timeout-text">检测超时</span
                ><SelectField
                  v-model="probeTimeout"
                  label="检测超时"
                  :options="[
                    { value: 5, label: '5s' },
                    { value: 10, label: '10s' },
                    { value: 15, label: '15s' }
                  ]" /></span
              ><button
                type="button"
                :disabled="probing || !form.base_url || !form.model"
                @click="testVision"
                ><ScanEye :size="16" />{{ probing ? '正在测试图片识别…' : '测试图片识别' }}</button
              ></div
            ><p class="help"
              >发送一张内置随机色块图，调用一次模型，可能消耗额度。不使用你的媒体文件。</p
            ><p v-if="form.provider === 'zhipu-coding'" class="help"
              >Coding Plan 的图片能力取决于套餐与模型；本测试验证直接图片请求，不调用视觉 MCP。</p
            ><div v-if="probeError" class="notice error" role="alert">{{ probeError }}</div
            ><div v-if="probeResult" class="probe-result" :class="probeResult.status" role="status"
              ><strong>{{ probeResult.message }}</strong
              ><p
                >耗时 {{ probeResult.elapsed_ms }} ms
                <span v-if="probeResult.code">· {{ probeResult.code }}</span
                ><span v-if="probeResult.http_status">
                  · HTTP {{ probeResult.http_status }}</span
                ></p
              ><p>预期：{{ probeResult.expected }}</p
              ><p v-if="probeResult.answer">回答：{{ probeResult.answer }}</p></div
            ></div
          >
          <div class="form-grid model-parameters"
            ><label
              >并发任务数<input
                v-model.number="form.workers"
                aria-label="并发任务数"
                type="number"
                min="1"
                max="32"
                required
              /><small
                >同时处理的视频数量，保存后也应用于该配置的排队和运行中批次；降低时等待在途任务完成。</small
              ></label
            ><label
              >请求超时<input
                v-model="form.timeout"
                aria-label="请求超时"
                required
                placeholder="180s"
              /><small>单次模型请求的最长等待时间。</small></label
            ></div
          >
          <div class="form-grid model-parameters"
            ><label
              >思考模式<SelectField
                v-model="form.thinking"
                label="思考模式"
                :options="[
                  { value: 'default', label: '跟随模型默认' },
                  { value: 'disabled', label: '关闭思考' },
                  { value: 'enabled', label: '开启思考' }
                ]"
            /></label>
            <label v-if="form.provider === 'custom' && form.thinking !== 'default'"
              >思考参数类型<SelectField
                v-model="form.thinking_adapter"
                label="思考参数类型"
                @change="changeThinkingAdapter"
                :options="[{ value: 'auto', label: '请选择服务支持的参数' }, ...thinkingAdapters]"
            /></label>
          </div>
          <div class="form-grid model-parameters"
            ><label
              v-if="
                form.thinking === 'enabled' &&
                (['openai', 'ollama'].includes(form.provider) ||
                  (form.provider === 'claude' && !manualClaude) ||
                  ['effort', 'adaptive'].includes(form.thinking_adapter))
              "
              >思考强度<SelectField
                v-model="form.thinking_effort"
                label="思考强度"
                :options="[
                  { value: 'low', label: '低' },
                  { value: 'medium', label: '中' },
                  { value: 'high', label: '高' }
                ]"
            /></label>
            <label v-if="showBudget"
              >{{ isLlama ? '思考预算' : '固定思考预算'
              }}<input
                v-model.number="form.thinking_budget"
                :aria-label="isLlama ? '思考预算' : '固定思考预算'"
                type="number"
                :min="isLlama ? 1 : 1024"
                max="32767"
                required
              /><small
                >{{
                  isLlama
                    ? '建议从 512 开始；达到预算后由服务结束思考，再生成标题。'
                    : 'Claude 固定预算至少 1024。'
                }}须小于最大输出 token。</small
              ></label
            >
            <label
              >最大输出 token<input
                v-model.number="form.max_tokens"
                aria-label="最大输出 token"
                type="number"
                min="1"
                max="32768"
                required
              /><small
                >包含思考和最终标题。{{ isLlama ? '建议先设为 2048，为标题保留空间。' : '' }}</small
              ></label
            >
            <label
              >温度<input
                v-model.number="form.temperature"
                aria-label="温度"
                type="number"
                min="0"
                :max="form.protocol === 'anthropic' ? 1 : 2"
                step="0.1"
                :disabled="form.omit_temperature || form.thinking === 'enabled'"
                required /></label
          ></div>
          <label class="check-label"
            ><input
              v-model="form.omit_temperature"
              type="checkbox"
              role="switch"
            />使用模型默认温度（不发送 temperature）</label
          >
          <p class="help"
            >跟随默认不发送思考参数。开启思考时不发送温度，思考仍计入输出额度。部分型号无法关闭思考；自定义服务必须确认参数支持。</p
          >
          <p v-if="isLlama && form.thinking !== 'default'" class="help"
            >需要支持思考预算的 llama.cpp
            和模型聊天模板。思考内容单独返回，仅用最终回答生成标题。</p
          >
          <label
            >命名提示词<SelectField
              v-model="form.prompt_id"
              label="命名提示词"
              :options="prompts.map((p) => ({ value: p.id, label: p.title }))" /></label
          ><details v-if="selectedPrompt" class="prompt-preview"
            ><summary>查看所选提示词</summary><p>{{ selectedPrompt.body }}</p></details
          ><p class="help"
            >提示词在系统设置的「Prompt配置」页维护；请求时会自动在所选提示词末尾附加固定提示词，作为统一输出约束。</p
          ><div v-if="dialogError" role="alert" class="notice error">{{ dialogError }}</div
          ><div class="modal-actions"
            ><button type="button" :disabled="saving" @click="close">取消</button
            ><button class="primary" :disabled="saving">{{
              saving ? '正在保存…' : '保存配置'
            }}</button></div
          ></form
        ></Modal
      ></ModalTransition
    >
    <ModalTransition
      ><Modal
        v-if="deleteTarget"
        title="删除模型配置"
        @close="!actionBusy && (deleteTarget = undefined)"
        ><p>确定删除“{{ deleteTarget.title }}”？</p
        ><p class="help"
          >删除后无法恢复。已有任务和历史记录会保留。全局默认配置或被运行计划使用的配置，需先更换关联。</p
        ><div v-if="deleteError" role="alert" class="notice error">{{ deleteError }}</div
        ><div class="modal-actions"
          ><button :disabled="actionBusy" @click="deleteTarget = undefined">取消</button
          ><button class="cancel-run" :disabled="actionBusy" @click="remove"
            ><Trash2 :size="16" />{{ actionBusy ? '正在删除…' : '确认删除' }}</button
          ></div
        ></Modal
      ></ModalTransition
    >
  </section>
</template>
