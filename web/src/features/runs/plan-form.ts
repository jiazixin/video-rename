import { computed, ref } from 'vue'
import { api, apiItems, newRequestKey } from '../../app/api'
import { errorText } from '../../app/errors'
import type { Plan } from '../../app/contracts'

export type PlanMode = 'manual' | 'plan'

type PlanFormOptions = {
  /** manual submits one run now; plan creates or edits a schedule. */
  mode: PlanMode
  /** The schedule being edited; null for a new one. */
  source: Plan | null
  notify: (message: string) => void
  /** Called once the run is queued or the schedule is saved. */
  onSaved: () => void
}

// The form of a manual run or a schedule, filled from source when editing.
export function usePlanForm({ mode, source, notify, onSaved }: PlanFormOptions) {
  const parts = source?.expression.split(' ')
  const taskType = ref(source?.task || 'rename'),
    modelConfigID = ref(source?.model_config_id || 0),
    name = ref(source?.name || ''),
    targets = ref<number[]>(source ? [...source.targets] : []),
    recursive = ref(source?.recursive ?? true),
    kind = ref(source?.kind || 'daily'),
    timezone = ref(source?.timezone || 'Asia/Shanghai'),
    enabled = ref(source?.enabled ?? true),
    cron = ref(source?.expression || '0 */6 * * *'),
    minute = ref(parts ? Number(parts[0]) || 0 : 30),
    hour = ref(parts ? Number(parts[1]) || 0 : 2),
    days = ref(parts?.[4] && source?.kind === 'weekly' ? parts[4].split(',').map(Number) : [1]),
    preview = ref<string[]>([]),
    busy = ref(false),
    error = ref('')
  const expression = computed(() => {
    if (kind.value === 'hourly') return `${minute.value} * * * *`
    if (kind.value === 'daily') return `${minute.value} ${hour.value} * * *`
    if (kind.value === 'weekly') return `${minute.value} ${hour.value} * * ${days.value.join(',')}`
    return cron.value
  })
  const title = computed(() => {
    if (mode === 'manual') return '新建手动计划'
    return source ? '编辑自动计划' : '新建自动计划'
  })
  const submitLabel = computed(() => {
    if (busy.value) return '正在提交…'
    return mode === 'manual' ? '立即运行' : '保存计划'
  })
  async function showPreview() {
    error.value = ''
    try {
      preview.value = await apiItems<string>('/schedules/preview', {
        method: 'POST',
        body: { expression: expression.value, timezone: timezone.value }
      })
    } catch (e) {
      error.value = errorText(e)
    }
  }
  async function submit() {
    busy.value = true
    error.value = ''
    try {
      const extracting = taskType.value === 'extract'
      if (mode === 'manual') {
        const r = await api<{ id: number }>('/runs', {
          method: 'POST',
          body: {
            task: taskType.value,
            model_config_id: extracting ? 0 : modelConfigID.value,
            targets: targets.value,
            recursive: recursive.value,
            key: newRequestKey()
          }
        })
        notify(`${extracting ? '抽帧' : '重命名'}批次 #${r.id} 已加入队列，可在批次记录查看进度。`)
      } else {
        await api<unknown>(source ? `/schedules/${source.id}` : '/schedules', {
          method: source ? 'PATCH' : 'POST',
          body: {
            task: taskType.value,
            name: name.value,
            model_config_id: extracting ? 0 : modelConfigID.value,
            targets: targets.value,
            recursive: recursive.value,
            kind: kind.value,
            expression: expression.value,
            timezone: timezone.value,
            enabled: enabled.value,
            version: source?.version || 0
          }
        })
        notify('运行计划已保存')
      }
      onSaved()
    } catch (e) {
      error.value = errorText(e)
    } finally {
      busy.value = false
    }
  }
  return {
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
  }
}
