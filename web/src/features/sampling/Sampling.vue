<script setup lang="ts">
  import { computed, onMounted, ref, watch } from 'vue';
  import { Plus, Save, Trash2 } from 'lucide-vue-next';
  import { api } from '../../app/api';
  import SelectField from '../../components/SelectField.vue';
  import './sampling.css';
  type Settings = {
    sampling: {
      strategy: string;
      max_frames: number;
      skip_start_ratio: number;
      skip_end_ratio: number;
      duration_rules: { max_duration: string | null; frames: number }[];
      size_rules: { max_mib: number | null; frames: number }[];
    };
    images: { max_dimension: number; jpeg_quality: number };
  };
  const form = ref<Settings>(),
    loading = ref(true),
    saving = ref(false),
    error = ref(''),
    message = ref(''),
    saved = ref('');
  const startPercent = computed({
    get: () => Number(((form.value?.sampling.skip_start_ratio || 0) * 100).toFixed(8)),
    set: (v: number) => {
      if (form.value) form.value.sampling.skip_start_ratio = v / 100;
    }
  });
  const endPercent = computed({
    get: () => Number(((form.value?.sampling.skip_end_ratio || 0) * 100).toFixed(8)),
    set: (v: number) => {
      if (form.value) form.value.sampling.skip_end_ratio = v / 100;
    }
  });
  const dirty = computed(() => !!form.value && JSON.stringify(form.value) !== saved.value);
  watch(
    form,
    () => {
      message.value = '';
    },
    { deep: true }
  );
  async function load() {
    loading.value = true;
    error.value = '';
    try {
      form.value = await api<Settings>('/sampling');
      saved.value = JSON.stringify(form.value);
    } catch (e) {
      error.value = (e as Error).message;
    } finally {
      loading.value = false;
    }
  }
  function addDuration() {
    const rules = form.value!.sampling.duration_rules;
    rules.splice(rules.length - 1, 0, { max_duration: '', frames: 12 });
  }
  function addSize() {
    const rules = form.value!.sampling.size_rules;
    rules.splice(rules.length - 1, 0, { max_mib: (rules.at(-2)?.max_mib || 0) + 1024, frames: 12 });
  }
  async function save() {
    if (!form.value || saving.value) return;
    error.value = '';
    message.value = '';
    if (startPercent.value + endPercent.value >= 100) {
      error.value = '跳过开头与末尾的比例之和必须小于 100%。';
      return;
    }
    saving.value = true;
    try {
      const result = await api<Settings>('/sampling', 'PUT', form.value);
      form.value = result;
      saved.value = JSON.stringify(result);
      await Promise.resolve();
      message.value = '设置已保存，将用于新建任务。';
    } catch (e) {
      error.value = (e as Error).message;
    } finally {
      saving.value = false;
    }
  }
  onMounted(load);
</script>
<template>
  <section class="sampling-page">
    <div v-if="error" class="notice error" role="alert"
      >{{ error }}<button v-if="!form" @click="load">重新加载</button></div
    >
    <p v-if="loading" role="status">正在加载设置…</p>
    <form v-else-if="form" @submit.prevent="save">
      <fieldset :disabled="saving" class="sampling-fieldset">
        <div class="sampling-grid">
          <section class="panel sampling-card"
            ><header
              ><span class="eyebrow">01 · 采样</span><h2>抽帧方式</h2
              ><p class="help">画面在选定范围内均匀抽取，实际帧数也受视频长度限制。</p></header
            >
            <label
              >抽帧策略<SelectField
                v-model="form.sampling.strategy"
                label="抽帧策略"
                :options="[
                  { value: 'duration', label: '按视频时长' },
                  { value: 'size', label: '按文件大小' },
                  { value: 'hybrid', label: '混合 · 时长为主，大小辅助' }
                ]"
            /></label>
            <p class="help">{{
              form.sampling.strategy === 'hybrid'
                ? '按时长确定帧数，文件较大时最多增加 2 帧，仍受最大帧数限制。'
                : form.sampling.strategy === 'duration'
                  ? '按视频时长匹配下方档位。'
                  : '按文件大小匹配下方档位。'
            }}</p>
            <label
              >每个视频最大帧数<input
                v-model.number="form.sampling.max_frames"
                aria-label="每个视频最大帧数"
                type="number"
                min="1"
                max="32"
                step="1"
                required
            /></label>
            <div class="sampling-pair"
              ><label
                >跳过开头（%）<input
                  v-model.number="startPercent"
                  aria-label="跳过开头（%）"
                  type="number"
                  min="0"
                  max="99.99"
                  step="0.01"
                  required /></label
              ><label
                >跳过末尾（%）<input
                  v-model.number="endPercent"
                  aria-label="跳过末尾（%）"
                  type="number"
                  min="0"
                  max="99.99"
                  step="0.01"
                  required /></label
            ></div>
            <small class="help">填 0 表示不跳过；首尾比例之和须小于 100%。</small>
          </section>
          <section class="panel sampling-card"
            ><header
              ><span class="eyebrow">02 · 图片</span><h2>截图尺寸与质量</h2
              ><p class="help">更多帧和更大的图片通常增加请求体积与模型消耗。</p></header
            >
            <label
              >截图最长边（像素）<input
                v-model.number="form.images.max_dimension"
                aria-label="截图最长边（像素）"
                type="number"
                min="128"
                max="2048"
                step="1"
                required /></label
            ><p class="help">128–2048 像素，保持原始比例，不放大小图。</p>
            <label
              >JPEG 质量档位<input
                v-model.number="form.images.jpeg_quality"
                aria-label="JPEG 质量档位"
                type="number"
                min="2"
                max="15"
                step="1"
                required /></label
            ><p class="help">2–15，数字越小通常越清晰，图片体积也越大。</p>
          </section>
        </div>
        <section v-if="form.sampling.strategy !== 'size'" class="panel sampling-card sampling-rules"
          ><header
            ><div><span class="eyebrow">时长档位</span><h2>按视频时长取帧</h2></div
            ><button type="button" @click="addDuration"
              ><Plus :size="15" />添加时长档位</button
            ></header
          ><p class="help"
            >从上到下取首个命中项，上限须递增。可填 30s、2m、1h；最后一档覆盖其余时长。</p
          >
          <div class="sampling-rule-head"><span>时长上限（含）</span><span>帧数</span><span /></div>
          <div v-for="(rule, i) in form.sampling.duration_rules" :key="i" class="sampling-rule-row"
            ><input
              v-if="rule.max_duration !== null"
              v-model="rule.max_duration"
              :aria-label="`时长上限 ${i + 1}`"
              placeholder="例如 10m"
              required /><span v-else class="sampling-fallback">超过以上所有档位</span
            ><input
              v-model.number="rule.frames"
              :aria-label="`时长档位帧数 ${i + 1}`"
              type="number"
              min="1"
              max="32"
              step="1"
              required /><button
              v-if="rule.max_duration !== null"
              type="button"
              class="icon-btn"
              :aria-label="`删除时长档位 ${i + 1}`"
              @click="form.sampling.duration_rules.splice(i, 1)"
              ><Trash2 :size="16" /></button
            ><span v-else
          /></div>
        </section>
        <section
          v-if="form.sampling.strategy !== 'duration'"
          class="panel sampling-card sampling-rules"
          ><header
            ><div><span class="eyebrow">大小档位</span><h2>按文件大小取帧</h2></div
            ><button type="button" @click="addSize"><Plus :size="15" />添加大小档位</button></header
          ><p class="help">上限须递增，MiB = 1024 × 1024 字节。最后一档覆盖其余大小。</p>
          <div class="sampling-rule-head"
            ><span>大小上限（MiB，含）</span><span>帧数</span><span
          /></div>
          <div v-for="(rule, i) in form.sampling.size_rules" :key="i" class="sampling-rule-row"
            ><input
              v-if="rule.max_mib !== null"
              v-model.number="rule.max_mib"
              :aria-label="`大小上限 ${i + 1}`"
              type="number"
              min="1"
              max="8796093022207"
              step="1"
              required /><span v-else class="sampling-fallback">超过以上所有档位</span
            ><input
              v-model.number="rule.frames"
              :aria-label="`大小档位帧数 ${i + 1}`"
              type="number"
              min="1"
              max="32"
              step="1"
              required /><button
              v-if="rule.max_mib !== null"
              type="button"
              class="icon-btn"
              :aria-label="`删除大小档位 ${i + 1}`"
              @click="form.sampling.size_rules.splice(i, 1)"
              ><Trash2 :size="16" /></button
            ><span v-else
          /></div>
        </section>
      </fieldset>
      <div v-if="message" class="notice success" role="status">{{ message }}</div>
      <footer class="sampling-footer"
        ><div
          ><strong>{{ dirty ? '有未保存的修改' : '设置已同步' }}</strong
          ><p class="help">保存后用于新建任务，已创建任务继续使用原参数。</p></div
        ><button class="primary" :disabled="saving || !dirty"
          ><Save :size="16" />{{ saving ? '正在保存…' : '保存抽帧设置' }}</button
        ></footer
      >
    </form>
  </section>
</template>
