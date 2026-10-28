<script setup lang="ts">
  import './models.css';
  import { errorText } from '../../app/errors';
  import { computed, onMounted, ref } from 'vue';
  import { apiItems } from '../../app/api';
  import { type ModelProfile } from '../../app/contracts';
  import SelectField from '../../components/SelectField.vue';
  const model = defineModel<number>({ required: true });
  const rows = ref<ModelProfile[]>([]),
    error = ref(''),
    loading = ref(true);
  const selected = computed(() => rows.value.find((p) => p.id === model.value));
  async function load() {
    loading.value = true;
    try {
      rows.value = (await apiItems<ModelProfile>('/model-configs')).filter((p) => p.ready);
      error.value = '';
      const fallback = rows.value.find((p) => p.is_default) ?? rows.value[0];
      if (!model.value && fallback) model.value = fallback.id;
    } catch (e) {
      error.value = errorText(e);
    } finally {
      loading.value = false;
    }
  }
  onMounted(load);
</script>
<template>
  <div class="prompt-picker"
    ><label
      >模型配置<SelectField
        :model-value="model"
        :options="rows.map((p) => ({ value: p.id, label: p.title }))"
        label="模型配置"
        @update:model-value="model = Number($event)" /></label
    ><div v-if="error" role="alert" class="notice error"
      >{{ error }}<button type="button" @click="load">重试</button></div
    ><p v-else-if="loading" class="help">正在读取模型配置…</p
    ><p v-else-if="!selected" class="help">请先在“模型配置”页面完善配置，然后在这里选择。</p
    ><details v-else class="prompt-preview"
      ><summary>查看命名提示词</summary><p>{{ selected.body }}</p></details
    ></div
  >
</template>
