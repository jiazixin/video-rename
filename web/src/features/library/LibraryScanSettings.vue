<script setup lang="ts">
  import ToastNotice from '../../components/ToastNotice.vue';
  import { computed, onMounted, ref, watch } from 'vue';
  import { RefreshCw, Save } from '@lucide/vue';
  import { type LibraryScanSettings } from '../../app/contracts';
  import ScanDirectorySelection from './ScanDirectorySelection.vue';
  import ScanStatus from './ScanStatus.vue';
  import { useLibraryScan } from './library-scan';
  const scan = useLibraryScan();
  const { state, error, actionError, submitting, scanning } = scan;
  const toast = ref<InstanceType<typeof ToastNotice>>();
  const form = ref<LibraryScanSettings>(),
    saved = ref('');
  const dirty = computed(() => !!form.value && JSON.stringify(form.value) !== saved.value);
  watch(state, (value) => {
    if (!form.value && value) {
      form.value = {
        ...value.settings,
        targets: [...value.settings.targets],
        excluded_targets: [...value.settings.excluded_targets]
      };
      saved.value = JSON.stringify(form.value);
    }
  });
  async function save() {
    if (!form.value) return;
    const result = await scan.save(form.value);
    if (!result) return;
    form.value = {
      ...result,
      targets: [...result.targets],
      excluded_targets: [...result.excluded_targets]
    };
    saved.value = JSON.stringify(form.value);
    toast.value?.show('扫描设置已保存。自动扫描在后台运行，关闭网页仍会继续。');
  }
  onMounted(scan.startPolling);
</script>
<template>
  <section class="panel library-scan-settings">
    <ToastNotice ref="toast" />
    <header
      ><span class="eyebrow">媒体索引</span><h2>后台目录扫描</h2
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
            ><input
              v-model="form.enabled"
              type="checkbox"
              aria-label="开启定时扫描"
            />开启定时扫描</label
          >
          <label class="scan-interval"
            >扫描间隔<span class="scan-interval-input"
              ><input
                v-model.number="form.interval_minutes"
                aria-label="扫描间隔（分钟）"
                type="number"
                min="5"
                max="1440"
                step="1"
                required
              /><span>分钟</span></span
            ></label
          >
          <p class="help scan-interval-help"
            >上一轮完成后再等待此间隔，不会重叠扫描。<span
              >默认 60 分钟，可设 5–1440 分钟。</span
            ></p
          >
        </div>
        <div class="scan-settings-scope">
          <ScanDirectorySelection
            v-model="form.targets"
            :paths="state?.directory_paths || {}"
            title="扫描范围"
            description="包含全部子目录；选择媒体库可扫描全部文件夹。可选 1–100 个目录，重叠范围自动合并。"
            empty="尚未选择扫描目录，请点击加号添加。"
          />
          <p v-if="!form.targets.length || form.targets.length > 100" class="notice error"
            >请选择 1–100 个扫描目录。</p
          >
          <ScanDirectorySelection
            v-model="form.excluded_targets"
            :paths="state?.directory_paths || {}"
            title="排除目录"
            description="手动扫描和后台扫描均跳过这些目录及其子目录，排除优先于扫描范围。最多 100 个，可随时移除。"
            empty="未设置排除目录，将扫描所选范围内的全部目录。"
          />
          <p class="help"
            >排除不会删除已有索引，也不限制浏览、命名或抽帧。运行中的扫描沿用启动时的设置，新设置从下一轮生效。</p
          >
        </div>
      </fieldset>
      <footer class="scan-settings-actions"
        ><button
          class="primary"
          :disabled="
            submitting ||
            !dirty ||
            !form.targets.length ||
            form.targets.length > 100 ||
            form.excluded_targets.length > 100
          "
          ><Save :size="16" />{{ submitting ? '正在提交…' : '保存扫描设置' }}</button
        ><button
          type="button"
          :disabled="submitting || scanning || dirty || !state"
          @click="scan.start()"
          ><RefreshCw :size="16" />立即扫描全部范围</button
        ><span v-if="dirty" class="help">保存后可扫描所选范围。</span></footer
      >
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
    padding: 26px;
  }
  .library-scan-settings h2 {
    margin: 8px 0;
  }
  .scan-settings-fields {
    border: 0;
    margin: 20px 0;
    padding: 0;
    min-width: 0;
  }
  .scan-settings-options {
    display: grid;
    grid-template-columns: auto auto minmax(0, 1fr);
    align-items: center;
    gap: 18px 28px;
    padding: 14px 16px;
    margin-bottom: 22px;
    border: 1px solid #e5eae7;
    border-radius: 8px;
    background: #f8faf9;
  }
  .scan-settings-options label {
    margin: 0;
  }
  .scan-settings-options .check-label {
    margin: 0 !important;
    white-space: nowrap;
  }
  .scan-interval,
  .scan-interval-input {
    display: flex;
    align-items: center;
    flex-direction: row;
    gap: 8px;
    white-space: nowrap;
  }
  .scan-interval-input {
    color: var(--ash);
    font-size: 12px;
    font-weight: 400;
  }
  .scan-interval-input input {
    width: 84px;
    min-height: 34px;
    padding: 6px 10px;
    text-align: center;
    font-variant-numeric: tabular-nums;
  }
  .scan-interval-help {
    margin: 0;
    padding-left: 22px;
    border-left: 1px solid var(--line);
    font-size: 11px;
    line-height: 1.7;
  }
  .scan-interval-help span {
    display: block;
  }
  .scan-settings-scope {
    min-width: 0;
  }
  .scan-settings-scope > .help {
    margin: 6px 0 12px;
  }
  .scan-settings-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 12px;
    align-items: center;
    margin-bottom: 20px;
  }
  @media (max-width: 960px) {
    .scan-settings-options {
      grid-template-columns: auto minmax(0, 1fr);
      gap: 12px 24px;
    }
    .scan-interval {
      justify-content: flex-end;
    }
    .scan-interval-help {
      grid-column: 1 / -1;
      padding: 10px 0 0;
      border-left: 0;
      border-top: 1px solid var(--line);
    }
    .scan-interval-help span {
      display: inline;
      margin-left: 6px;
    }
  }
  @media (max-width: 600px) {
    .library-scan-settings {
      padding: 16px;
    }
    .scan-settings-options {
      grid-template-columns: minmax(0, 1fr);
      gap: 14px;
      padding: 14px;
    }
    .scan-interval {
      justify-content: space-between;
    }
    .scan-interval-help span {
      display: block;
      margin-left: 0;
    }
    .scan-settings-actions {
      gap: 8px;
    }
    .scan-settings-actions > button {
      flex: 1 1 auto;
    }
  }
</style>
