<script setup lang="ts">
import { onDeactivated, onUnmounted, ref, watch } from 'vue'
import { CircleCheck, X } from 'lucide-vue-next'

const message = defineModel<string>({ required: true })
const duration = 5000
const revision = ref(0)
let timer: ReturnType<typeof setTimeout> | undefined
function clearTimer() { clearTimeout(timer); timer = undefined }
function close() { clearTimer(); message.value = '' }
watch(message, value => {
  clearTimer()
  if (value) {
    revision.value++
    timer = setTimeout(close, duration)
  }
}, { immediate: true })
onDeactivated(close)
onUnmounted(clearTimer)
</script>

<template>
  <Teleport to="body">
    <Transition name="toast">
      <div v-if="message" class="toast-notice" role="status" aria-live="polite" aria-atomic="true">
        <CircleCheck class="toast-icon" :size="20" aria-hidden="true" />
        <span class="toast-message">{{ message }}</span>
        <button type="button" class="toast-close" aria-label="关闭提示" @click="close"><X :size="16" aria-hidden="true" /></button>
        <div :key="revision" class="toast-countdown" :style="{ animationDuration: `${duration}ms` }" aria-hidden="true" />
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.toast-notice{position:fixed;top:24px;right:24px;z-index:300;display:flex;align-items:flex-start;gap:12px;width:380px;max-width:calc(100vw - 32px);box-sizing:border-box;padding:18px 16px 21px;border:1px solid #cee5db;border-radius:12px;background:#f0f8f5;color:#287257;box-shadow:0 8px 30px #182c201a;overflow:hidden}
.toast-icon{flex-shrink:0;margin-top:1px}
.toast-message{flex:1;min-width:0;line-height:1.6;font-size:14px;overflow-wrap:anywhere;white-space:pre-line}
.toast-close{flex-shrink:0;min-height:0;padding:3px;border:0;background:transparent;color:inherit;border-radius:4px}
.toast-close:hover{background:#dceee5}
.toast-close:focus-visible{outline:2px solid #287257;outline-offset:2px}
.toast-countdown{position:absolute;bottom:0;left:0;right:0;height:3px;background:#399874;transform-origin:left;animation:toast-countdown linear forwards}
@keyframes toast-countdown{from{transform:scaleX(1)}to{transform:scaleX(0)}}
.toast-enter-active,.toast-leave-active{transition:opacity .18s ease,transform .18s ease}
.toast-enter-from,.toast-leave-to{opacity:0;transform:translateX(16px)}
@media(max-width:600px){.toast-notice{top:16px;right:16px}}
@media(prefers-reduced-motion:reduce){.toast-enter-active,.toast-leave-active{transition:none}}
</style>
