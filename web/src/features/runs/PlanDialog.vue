<script setup lang="ts">
  import { ChevronRight } from '@lucide/vue';
  import ModalDialog from '../../components/ModalDialog.vue';
  import SelectField from '../../components/SelectField.vue';
  import { DirectoryPicker } from '../library';
  import { ModelPicker } from '../models';
  import { useToast } from '../../app/use-toast';
  import type { Plan } from '../../app/contracts';
  import { usePlanForm, type PlanMode } from './plan-form';
  import './runs.css';
  // Submits a manual run now, or creates or edits a schedule.
  const { mode, plan, ready } = defineProps<{
    mode: PlanMode;
    /** The schedule being edited; null for a new one. */
    plan: Plan | null;
    /** Whether a naming model is available for an immediate run. */
    ready: boolean;
  }>();
  const emit = defineEmits<{ close: []; saved: [] }>();
  const {
    taskType,
    modelConfigID,
    name,
    targets,
    recursive,
    kind,
    timezone,
    enabled,
    cron,
    minute,
    hour,
    days,
    preview,
    busy,
    error,
    expression,
    title,
    submitLabel,
    showPreview,
    submit
  } = usePlanForm({
    mode,
    source: plan,
    notify: useToast().show,
    onSaved: () => {
      emit('saved');
    }
  });
  const weekdays = ['日', '一', '二', '三', '四', '五', '六'];
</script>

<template>
  <ModalDialog :title="title" wide @close="!busy && emit('close')"
    ><form @submit.prevent="submit">
      <div class="plan-config-row"
        ><label
          >任务类型<SelectField
            v-model="taskType"
            label="计划任务类型"
            :options="[
              { value: 'rename', label: '命名' },
              { value: 'extract', label: '抽帧' }
            ]" /></label
        ><ModelPicker v-if="taskType !== 'extract'" v-model="modelConfigID"
      /></div>
      <template v-if="mode === 'plan'"
        ><label
          >计划名称<input
            v-model="name"
            required
            maxlength="80"
            placeholder="例如：每天整理旅行视频" /></label
        ><div class="form-grid"
          ><label
            >执行频率<SelectField
              v-model="kind"
              label="执行频率"
              :options="[
                { value: 'hourly', label: '按小时' },
                { value: 'daily', label: '按日' },
                { value: 'weekly', label: '按周' },
                { value: 'cron', label: 'Cron 表达式' }
              ]"
              @change="preview = []" /></label
          ><label>时区<input v-model="timezone" required placeholder="Asia/Shanghai" /></label></div
        ><div v-if="kind !== 'cron'" class="form-grid"
          ><label v-if="kind !== 'hourly'"
            >小时<input v-model="hour" type="number" min="0" max="23" required /></label
          ><label
            >分钟<input v-model="minute" type="number" min="0" max="59" required /></label></div
        ><div v-if="kind === 'weekly'" class="weekday-picker"
          ><label v-for="(d, i) in weekdays" :key="i"
            ><input v-model="days" type="checkbox" :value="i" />周{{ d }}</label
          ></div
        ><label v-if="kind === 'cron'"
          >Cron 表达式<input
            v-model="cron"
            aria-label="Cron 表达式"
            required
            placeholder="0 */6 * * *"
            class="mono"
          /><small class="help"
            >五段：分 时 日 月 周。日与周同时限制时，任一匹配即触发。</small
          ></label
        ><div class="preview-rule"
          ><code>{{ expression }}</code
          ><button type="button" @click="showPreview"
            >预览执行时间<ChevronRight :size="14" /></button></div
        ><ol v-if="preview.length" class="schedule-preview" aria-label="执行时间预览"
          ><li v-for="p in preview" :key="p">{{ p }}</li></ol
        ></template
      >
      <label class="field-heading">选择目标目录</label><DirectoryPicker v-model="targets" /><label
        class="check-label"
        ><input v-model="recursive" type="checkbox" role="switch" />包含所选目录的子目录</label
      ><label v-if="mode === 'plan'" class="check-label"
        ><input v-model="enabled" type="checkbox" role="switch" />保存后启用计划</label
      >
      <p class="help">{{
        taskType === 'extract'
          ? '为所选目录中的视频预先生成截图，包括已处理视频；有效缓存直接复用，供之后重命名使用。结果见“抽帧记录”。'
          : '已命名或手动标记已处理的视频会跳过；还原后的视频可再次处理。新文件会先进行稳定性检查。'
      }}</p
      ><div v-if="error" class="notice error" role="alert">{{ error }}</div
      ><div class="modal-actions"
        ><button type="button" :disabled="busy" @click="emit('close')">取消</button
        ><button
          class="primary"
          :disabled="
            busy ||
            !targets.length ||
            (taskType !== 'extract' && !modelConfigID) ||
            (taskType !== 'extract' && mode === 'manual' && !ready)
          "
          >{{ submitLabel }}</button
        ></div
      ></form
    ></ModalDialog
  >
</template>

<style scoped>
  .field-heading {
    margin-bottom: 8px !important;
  }
</style>
