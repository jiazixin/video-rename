export const statusNames: Record<string, string> = {
  queued: '排队中',
  scanning: '扫描与稳定性检查',
  running: '运行中',
  paused: '已暂停',
  cancelling: '正在取消',
  completed: '已完成',
  partial_failure: '部分失败',
  failed: '失败',
  cancelled: '已取消',
  interrupted: '已中断',
  done: '已完成',
  skipped: '已跳过'
}
export const actionNames: Record<string, string> = {
  manual: '手动命名',
  automatic: '自动命名',
  restore: '还原',
  recovery: '恢复核对',
  scan: '目录扫描',
  run: '运行',
  schedule: '计划触发',
  scheduler: '调度器'
}
