<script setup lang="ts">
  import { computed, onMounted, ref, watch } from 'vue';
  import { RefreshCw, Save } from 'lucide-vue-next';
  import type { LibraryScanSettings } from '../../app/api';
  import DirectoryPicker from './DirectoryPicker.vue';
  import ScanStatus from './ScanStatus.vue';
  import { useLibraryScan } from './library-scan';
  const scan = useLibraryScan();
  const { state, error, actionError, submitting, scanning } = scan;
  const form = ref<LibraryScanSettings>(),
    saved = ref(''),
    message = ref('');
  const dirty = computed(() => !!form.value && JSON.stringify(form.value) !== saved.value);
  watch(state, (value) => {
    if (!form.value && value) {
      form.value = { ...value.settings, targets: [...value.settings.targets] };
      saved.value = JSON.stringify(form.value);
    }
  });
  watch(
    form,
    () => {
      message.value = '';
    },
    { deep: true, flush: 'sync' }
  );
  async function save() {
    if (!form.value) return;
    const result = await scan.save(form.value);
    if (!result) return;
    form.value = { ...result, targets: [...result.targets] };
    saved.value = JSON.stringify(form.value);
    message.value = '扫描设置已保存。自动扫描在后台运行，关闭网页仍会继续。';
  }
  onMounted(scan.startPolling);
</script>
<template>
  <section class="panel library-scan-settings">
    <header
      ><span class="eyebrow">05 · 媒体索引</span><h2>后台目录扫描</h2
      ><p class="help"
        >定期发现各目录内新增或变化的文件，只同步文件列表，不触发命名和抽帧。</p
      ></header
    >
    <div v-if="error || actionError" class="notice error" role="alert"
      >{{ actionError || error
      }}<button v-if="error" type="button" @click="scan.refresh">重新读取状态</button></div
    >
    <p v-if="!form && !error" class="help">正在读取扫描设置…</p>
    <form v-if="form" @submit.prevent="save">
      <fieldset :disabled="submitting" class="scan-settings-fields">
        <div class="scan-settings-options">
          <label class="check-label"
            ><input v-model="form.enabled" type="checkbox" />开启定时扫描</label
          >
          <label
            >扫描间隔（分钟）<input
              v-model.number="form.interval_minutes"
              aria-label="扫描间隔（分钟）"
              type="number"
              min="5"
              max="1440"
              step="1"
              required
            /><small class="help"
              >默认 60 分钟（1 小时），可设 5–1440
              分钟。上一轮完成后再等待此间隔，不会重叠扫描。</small
            ></label
          >
        </div>
        <div class="scan-settings-scope"
          ><h3>扫描范围</h3
          ><p class="help"
            >所选目录均包含全部子目录；选择媒体库可扫描全部文件夹。需选择 1–100
            个目录，重叠范围自动合并。</p
          ><DirectoryPicker v-model="form.targets" /><p
            v-if="!form.targets.length || form.targets.length > 100"
            class="notice error"
            >请选择 1–100 个扫描目录。</p
          ></div
        >
      </fieldset>
      <footer class="scan-settings-actions"
        ><button
          class="primary"
          :disabled="submitting || !dirty || !form.targets.length || form.targets.length > 100"
          ><Save :size="16" />{{ submitting ? '正在提交…' : '保存扫描设置' }}</button
        ><button
          type="button"
          :disabled="submitting || scanning || dirty || !state"
          @click="scan.start()"
          ><RefreshCw :size="16" />立即扫描全部范围</button
        ><span v-if="dirty" class="help">保存后可扫描所选范围。</span></footer
      >
      <p v-if="message" class="notice success" role="status">{{ message }}</p>
    </form>
    <ScanStatus
      v-if="state"
      :status="state.status"
      :submitting="submitting"
      @cancel="scan.cancel"
    />
  </section>
</template>
<style scoped>
  .library-scan-settings {
    margin-top: 24px;
    padding: 26px;
  }
  .library-scan-settings h2 {
    margin: 8px 0;
  }
  .scan-settings-fields {
    border: 0;
    margin: 20px 0;
    padding: 0;
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    gap: 36px;
  }
  .scan-settings-options {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 20px;
  }
  .scan-settings-options > label:not(.check-label) {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .scan-settings-options input[type='number'] {
    max-width: 240px;
  }
  .scan-settings-options small {
    font-weight: 400;
  }
  .scan-settings-scope {
    min-width: 0;
  }
  .scan-settings-scope h3 {
    margin: 0;
    font-size: 13px;
  }
  .scan-settings-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 12px;
    align-items: center;
    margin-bottom: 20px;
  }
  @media (max-width: 760px) {
    .library-scan-settings {
      padding: 20px;
    }
    .scan-settings-fields {
      grid-template-columns: minmax(0, 1fr);
      gap: 20px;
    }
    .scan-settings-actions > button {
      flex: 1 1 auto;
    }
  }
</style>
