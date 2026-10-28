<script setup lang="ts">
  import { vFormErrors, fieldErrors } from '../../app/form-errors';
  import { errorText } from '../../app/errors';
  import { useToast } from '../../app/use-toast';
  import { computed, onMounted, ref, watch } from 'vue';
  import { Save, Images, Music2, Link2, Check, ArrowRight } from '@lucide/vue';
  import { useApi } from '../../app/use-api';
  const { api, formError, invalidFields } = useApi();
  const toast = useToast();
  type Rules = { padding: number; lowercase: boolean; prefix: string; compact: boolean };
  type Settings = { image: Rules; audio: Rules; shared: boolean };
  const form = ref<Settings>(),
    saved = ref(''),
    loading = ref(true),
    saving = ref(false),
    error = ref('');
  const dirty = computed(() => !!form.value && JSON.stringify(form.value) !== saved.value);
  const media = ref<'image' | 'audio'>('image');
  const mediaName = computed(() => (media.value === 'image' ? '图片' : '音频'));
  const rules = computed(() => form.value?.[media.value]);
  const examples = computed(() => {
    const current = rules.value;
    if (!current) return [];
    const width = Math.min(9, Math.max(1, current.padding || 1));
    const extension = media.value === 'image' ? 'JPG' : 'MP3';
    return [1, 2, 3].map(
      (n) =>
        `${current.prefix}${String(n).padStart(width, '0')}.${current.lowercase ? extension.toLowerCase() : extension}`
    );
  });
  watch(formError, (error) => {
    const path = fieldErrors(error).keys().next().value;
    if (path?.startsWith('image.')) media.value = 'image';
    if (path?.startsWith('audio.')) media.value = 'audio';
  });
  async function load() {
    loading.value = true;
    error.value = '';
    try {
      const state = await api<{ settings: Settings }>('/numbering/settings');
      form.value = {
        image: { ...state.settings.image },
        audio: { ...state.settings.audio },
        shared: state.settings.shared
      };
      saved.value = JSON.stringify(form.value);
    } catch (e) {
      error.value = errorText(e);
    } finally {
      loading.value = false;
    }
  }
  async function save() {
    if (!form.value || saving.value) return;
    for (const kind of ['image', 'audio'] as const) {
      const padding = form.value[kind].padding;
      if (!Number.isInteger(padding) || padding < 1 || padding > 9) {
        media.value = kind;
        error.value = errorText(
          invalidFields([{ path: `${kind}.padding`, reason: 'out_of_range' }])
        );
        return;
      }
    }
    saving.value = true;
    error.value = '';
    try {
      form.value = await api<Settings>('/numbering/settings', { method: 'PUT', body: form.value });
      saved.value = JSON.stringify(form.value);
      toast.show('编号规则已保存。之后的预览与编号使用新规则；已生成的批次和整批还原不受影响。');
    } catch (e) {
      error.value = errorText(e);
    } finally {
      saving.value = false;
    }
  }
  onMounted(load);
</script>
<template>
  <section class="numbering-settings">
    <div v-if="error" class="notice error" role="alert"
      >{{ error }}<button v-if="!form" @click="load">重新加载</button></div
    >
    <p v-if="loading" role="status">正在加载编号规则…</p>
    <form v-else-if="form" v-form-errors="formError" @submit.prevent="save">
      <fieldset :disabled="saving" class="numbering-fieldset">
        <div class="numbering-intro"
          ><div
            ><h2>让文件名井然有序</h2><p>分别设置图片与音频，在文件目录中使用“一键编号”。</p></div
          ><div class="media-picker" role="group" aria-label="编号媒体类型"
            ><button type="button" :aria-pressed="media === 'image'" @click="media = 'image'"
              ><Images :size="16" />图片</button
            ><button type="button" :aria-pressed="media === 'audio'" @click="media = 'audio'"
              ><Music2 :size="16" />音频</button
            ></div
          ></div
        >
        <section v-if="rules" class="numbering-editor" :aria-label="`${mediaName}编号规则`">
          <div class="numbering-editor-title"
            ><span class="media-icon"
              ><Images v-if="media === 'image'" :size="20" /><Music2 v-else :size="20" /></span
            ><h2>{{ mediaName }}编号规则</h2><span class="independent-note">独立配置</span></div
          >
          <div class="numbering-format"
            ><div class="format-fields"
              ><h3>命名格式</h3
              ><div class="format-inputs"
                ><label
                  >编号前缀<input
                    v-model="rules.prefix"
                    :name="`${media}.prefix`"
                    :aria-label="`${mediaName}编号前缀`"
                    type="text"
                    maxlength="32"
                    :placeholder="media === 'image' ? '如 IMG_' : '如 AUD_'"
                  /><small>可选，留空只使用数字</small></label
                ><label
                  >编号最小位数<input
                    v-model.number="rules.padding"
                    :name="`${media}.padding`"
                    :aria-label="`${mediaName}编号最小位数`"
                    type="number"
                    min="1"
                    max="9"
                    step="1"
                    required
                  /><small>1–9 位，不足时在前面补零</small></label
                ></div
              ></div
            >
            <aside class="filename-preview" aria-label="文件名格式示例"
              ><div class="preview-caption"
                ><span>文件名示例</span><span class="live-label"><i />实时预览</span></div
              ><div class="example-names" aria-live="polite"
                ><code v-for="name in examples" :key="name">{{ name }}</code></div
              ><p>仅展示格式；实际号码以编号预览为准。</p></aside
            ></div
          >
          <div class="numbering-method"
            ><h3>编号方式</h3
            ><div class="method-options"
              ><label class="method-option" :class="{ selected: !rules.compact }"
                ><input
                  v-model="rules.compact"
                  type="radio"
                  :name="`numbering-method-${media}`"
                  :data-field="`${media}.compact`"
                  :value="false"
                /><span
                  ><strong>保留已有编号</strong><small>保留现有号码，新文件从最大编号后继续。</small
                  ><span class="method-example"
                    >001 · 003 <ArrowRight :size="13" /> 004 · 005</span
                  ></span
                ></label
              ><label class="method-option" :class="{ selected: rules.compact }"
                ><input
                  v-model="rules.compact"
                  type="radio"
                  :name="`numbering-method-${media}`"
                  :data-field="`${media}.compact`"
                  :value="true"
                /><span
                  ><strong>连续重新编号</strong><small>从 1 开始紧凑排列，自动跳过保留号码。</small
                  ><span class="method-example"
                    >001 · 003 <ArrowRight :size="13" /> 001 · 002</span
                  ></span
                ></label
              ></div
            ></div
          >
          <label class="numbering-toggle"
            ><span
              ><strong>扩展名转小写</strong
              ><small
                >开启后将扩展名转为小写；关闭后保留原大小写。JPEG 始终统一为 .jpg。</small
              ></span
            ><input
              v-model="rules.lowercase"
              :name="`${media}.lowercase`"
              type="checkbox"
              role="switch"
              :aria-label="`${mediaName}扩展名转小写`"
          /></label>
          <details class="numbering-explanation"
            ><summary>规则说明</summary
            ><ul
              ><li>填写前缀后，仅“前缀 + 数字”视为已有编号；留空则匹配纯数字名。</li
              ><li>已有编号也会按最小位数补零；号码超过设定位数时保留完整数字。</li
              ><li>连续重新编号时，未参与本次编号的文件仍保留号码，新号自动跳过。</li></ul
            ></details
          >
        </section>
        <section class="numbering-shared"
          ><div class="shared-heading"
            ><Link2 :size="17" /><h3>公共设置</h3><span>同时适用于图片与音频</span></div
          ><label class="numbering-toggle"
            ><span
              ><strong>图片与音频共用序号</strong
              ><small>同一目录中互相保留已占用号码，仅改名当前选择的文件类型。</small></span
            ><input
              v-model="form.shared"
              name="shared"
              type="checkbox"
              role="switch"
              aria-label="图片与音频共用序号" /></label
          ><p v-if="form.shared" class="shared-note"
            >保留已有编号时，从两种类型的最大编号后续排；连续重新编号时，跳过另一类文件的已占用号码。</p
          ></section
        >
      </fieldset>
      <footer class="numbering-footer"
        ><div
          ><span class="save-state" :class="{ dirty }" role="status"
            ><i v-if="dirty" /><Check v-else :size="14" />{{
              dirty ? '有未保存的修改' : '所有修改已保存'
            }}</span
          ><p>统一保存两种类型的规则，仅用于之后的编号预览。</p></div
        ><button class="primary" :disabled="saving || !dirty"
          ><Save :size="16" />{{ saving ? '正在保存…' : '保存编号规则' }}</button
        ></footer
      >
    </form>
  </section>
</template>
<style scoped>
  .numbering-settings {
    min-width: 0;
  }
  .numbering-fieldset {
    border: 0;
    padding: 0;
    margin: 0;
    min-width: 0;
    display: grid;
    gap: var(--space-24);
  }
  .numbering-intro {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-20);
  }
  .numbering-intro h2 {
    font-size: var(--font-size-20);
    font-weight: var(--font-weight-600);
    letter-spacing: var(--tracking-negative-0-4);
  }
  .numbering-intro p,
  .numbering-footer p {
    font-size: var(--font-size-12);
    color: var(--steel);
    line-height: var(--line-height-1-7);
    margin: var(--space-7) 0 0;
  }
  .media-picker {
    display: flex;
    padding: var(--space-4);
    gap: var(--space-4);
    background: var(--palette-f1f4f2);
    border: 1px solid var(--palette-e4e9e6);
    border-radius: var(--radius-10);
    flex-shrink: 0;
  }
  .media-picker button {
    border: 0;
    background: transparent;
    box-shadow: none;
    min-width: 92px;
    color: var(--steel);
  }
  .media-picker button[aria-pressed='true'] {
    background: var(--palette-ffffff);
    color: var(--palette-28654d);
    box-shadow: var(--shadow-21);
  }
  .numbering-editor {
    border: 1px solid var(--line);
    border-radius: var(--radius-12);
    background: var(--palette-ffffff);
    overflow: hidden;
  }
  .numbering-editor-title {
    display: flex;
    align-items: center;
    gap: var(--space-12);
    padding: var(--space-20) var(--space-24);
    border-bottom: 1px solid var(--palette-edf0ee);
  }
  .numbering-editor-title h2 {
    font-size: var(--font-size-15);
    font-weight: var(--font-weight-600);
  }
  .media-icon {
    display: grid;
    place-items: center;
    width: 36px;
    height: 36px;
    border-radius: var(--radius-9);
    background: var(--palette-edf5f0);
    color: var(--palette-337756);
  }
  .independent-note {
    font-size: var(--font-size-11);
    color: var(--steel);
    margin-left: auto;
  }
  .numbering-format {
    display: grid;
    grid-template-columns: 1.1fr 1fr;
    gap: var(--space-32);
    padding: var(--space-24);
  }
  .numbering-settings h3 {
    font-size: var(--font-size-13);
    font-weight: var(--font-weight-600);
    margin: 0 0 var(--space-16);
  }
  .format-inputs {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--space-16);
  }
  .format-inputs label {
    display: flex;
    flex-direction: column;
    gap: var(--space-8);
    font-size: var(--font-size-12);
    margin: 0;
    min-width: 0;
  }
  .format-inputs input {
    width: 100%;
    min-width: 0;
  }
  .format-inputs small {
    font-size: var(--font-size-11);
    line-height: var(--line-height-1-6);
    color: var(--steel);
    font-weight: var(--font-weight-400);
  }
  .filename-preview {
    min-width: 0;
    background: var(--palette-f5f8f6);
    border: 1px solid var(--palette-e5ece7);
    border-radius: var(--radius-9);
    padding: var(--space-16) var(--space-18);
    align-self: stretch;
  }
  .preview-caption {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: var(--space-10);
    font-size: var(--font-size-11);
    color: var(--palette-52665a);
  }
  .live-label {
    display: flex;
    align-items: center;
    gap: var(--space-6);
    font-size: var(--font-size-10);
  }
  .live-label i {
    width: 5px;
    height: 5px;
    border-radius: var(--radius-round);
    background: var(--palette-47916c);
  }
  .example-names {
    display: flex;
    flex-direction: column;
    gap: var(--space-6);
    margin: var(--space-14) 0;
  }
  .example-names code {
    color: var(--palette-285c43);
    font-size: var(--font-size-13);
    overflow-wrap: anywhere;
    white-space: pre-wrap;
  }
  .example-names code:not(:first-child) {
    color: var(--palette-637368);
    font-size: var(--font-size-12);
  }
  .filename-preview p {
    font-size: var(--font-size-10);
    color: var(--palette-647369);
    line-height: var(--line-height-1-6);
    margin: 0;
  }
  .numbering-method {
    padding: 0 var(--space-24) var(--space-24);
  }
  .method-options {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--space-12);
  }
  .method-option {
    display: flex;
    flex-direction: row;
    align-items: flex-start;
    gap: var(--space-10);
    padding: var(--space-16);
    border: 1px solid var(--line);
    border-radius: var(--radius-9);
    cursor: pointer;
    margin: 0;
  }
  .method-option.selected {
    border-color: var(--palette-81aa93);
    background: var(--palette-f6faf7);
  }
  .method-option input {
    width: 15px;
    height: 15px;
    margin: var(--space-2) 0 0;
    flex-shrink: 0;
  }
  .method-option > span {
    min-width: 0;
  }
  .method-option strong,
  .numbering-toggle strong {
    display: block;
    font-size: var(--font-size-12);
    font-weight: var(--font-weight-500);
    line-height: var(--line-height-1-6);
  }
  .method-option small,
  .numbering-toggle small {
    display: block;
    font-size: var(--font-size-11);
    font-weight: var(--font-weight-400);
    line-height: var(--line-height-1-7);
    color: var(--steel);
    margin-top: var(--space-4);
  }
  .method-example {
    display: flex;
    align-items: center;
    gap: var(--space-9);
    margin-top: var(--space-12);
    font-size: var(--font-size-11);
    font-weight: var(--font-weight-400);
    color: var(--palette-65746a);
    font-variant-numeric: tabular-nums;
  }
  .numbering-toggle {
    display: flex;
    flex-direction: row;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-24);
    margin: 0;
    padding: var(--space-20) var(--space-24);
    border-top: 1px solid var(--palette-edf0ee);
    cursor: pointer;
  }
  .numbering-toggle > span {
    min-width: 0;
  }
  .numbering-settings :focus-visible {
    outline: 2px solid var(--palette-337b56);
    outline-offset: 3px;
  }
  .numbering-explanation {
    border-top: 1px solid var(--palette-edf0ee);
    padding: var(--space-14) var(--space-24);
    font-size: var(--font-size-11);
    color: var(--steel);
  }
  .numbering-explanation summary {
    cursor: pointer;
    line-height: var(--line-height-1-8);
    width: fit-content;
  }
  .numbering-explanation ul {
    margin: var(--space-12) 0 0;
    padding-left: var(--space-18);
    line-height: var(--line-height-1-9);
  }
  .numbering-shared {
    border: 1px solid var(--line);
    border-radius: var(--radius-12);
    background: var(--palette-fafbfa);
  }
  .shared-heading {
    display: flex;
    align-items: center;
    gap: var(--space-9);
    padding: var(--space-18) var(--space-24) 0;
    color: var(--palette-58685e);
  }
  .shared-heading h3 {
    margin: 0;
    font-size: var(--font-size-12);
  }
  .shared-heading > span {
    margin-left: auto;
    font-size: var(--font-size-10);
    color: var(--steel);
  }
  .numbering-shared .numbering-toggle {
    border: 0;
  }
  .shared-note {
    margin: 0;
    padding: 0 var(--space-24) var(--space-18);
    font-size: var(--font-size-11);
    color: var(--steel);
    line-height: var(--line-height-1-7);
  }
  .numbering-footer {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: var(--space-20);
    margin-top: var(--space-24);
    padding: var(--space-20) 0;
    border-top: 1px solid var(--line);
  }
  .save-state {
    display: flex;
    align-items: center;
    gap: var(--space-7);
    color: var(--palette-54715f);
    font-size: var(--font-size-12);
  }
  .save-state.dirty {
    color: var(--palette-80621f);
  }
  .save-state i {
    width: 6px;
    height: 6px;
    background: currentColor;
    border-radius: var(--radius-round);
  }
  .numbering-footer button {
    flex-shrink: 0;
  }
  .numbering-settings fieldset:disabled {
    opacity: 0.65;
  }
  @media (max-width: 900px) {
    .numbering-format {
      grid-template-columns: 1fr;
      gap: var(--space-20);
    }
    .example-names {
      flex-direction: row;
      flex-wrap: wrap;
      gap: var(--space-8) var(--space-18);
    }
    .example-names code:not(:first-child) {
      font-size: var(--font-size-13);
    }
  }
  @media (max-width: 600px) {
    .numbering-intro {
      align-items: flex-start;
      flex-direction: column;
      gap: var(--space-16);
    }
    .media-picker {
      width: 100%;
    }
    .media-picker button {
      flex: 1;
    }
    .numbering-fieldset {
      gap: var(--space-18);
    }
    .numbering-editor-title,
    .numbering-format,
    .numbering-toggle {
      padding: var(--space-18);
    }
    .numbering-method {
      padding: 0 var(--space-18) var(--space-18);
    }
    .method-options {
      grid-template-columns: 1fr;
    }
    .format-inputs {
      gap: var(--space-12);
    }
    .numbering-explanation {
      padding: var(--space-14) var(--space-18);
    }
    .shared-heading {
      padding: var(--space-18) var(--space-18) 0;
      flex-wrap: wrap;
    }
    .shared-heading > span {
      margin-left: 0;
    }
    .shared-note {
      padding: 0 var(--space-18) var(--space-18);
    }
    .numbering-footer {
      align-items: stretch;
      flex-direction: column;
      gap: var(--space-16);
    }
    .numbering-footer button {
      width: 100%;
    }
  }
</style>
