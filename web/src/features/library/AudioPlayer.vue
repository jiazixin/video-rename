<script setup lang="ts">
  import { ref, onMounted, onBeforeUnmount, computed } from 'vue';
  import { Play, Pause, Volume2, VolumeX, Disc3, SkipBack, SkipForward } from '@lucide/vue';
  defineProps<{ src: string; switching?: boolean }>();
  const emit = defineEmits<{ previous: []; next: [] }>();
  const audio = ref<HTMLAudioElement>(),
    paused = ref(true),
    current = ref(0),
    duration = ref(0),
    volume = ref(1),
    muted = ref(false),
    error = ref('');
  const progress = computed(() => (duration.value ? (current.value / duration.value) * 100 : 0));
  function sync() {
    const a = audio.value;
    if (!a) return;
    paused.value = a.paused;
    current.value = a.currentTime;
    duration.value = Number.isFinite(a.duration) ? a.duration : 0;
    volume.value = a.volume;
    muted.value = a.muted;
  }
  async function toggle() {
    const a = audio.value;
    if (!a) return;
    if (!a.paused) a.pause();
    else
      try {
        await a.play();
        error.value = '';
      } catch {
        error.value = '播放未能开始，请重试或检查音频编码。';
      }
  }
  function seek(e: Event) {
    if (audio.value && duration.value) {
      audio.value.currentTime = Math.min(
        duration.value,
        Math.max(0, Number((e.target as HTMLInputElement).value))
      );
      sync();
    }
  }
  function keyboard(e: KeyboardEvent) {
    if (e.altKey || e.ctrlKey || e.metaKey || !['ArrowLeft', 'ArrowRight'].includes(e.key)) return;
    const target = e.target as HTMLElement | null;
    if (target?.isContentEditable || target?.closest('textarea,select,input:not([type="range"])'))
      return;
    const a = audio.value;
    if (!a || !duration.value) return;
    e.preventDefault();
    a.currentTime = Math.max(
      0,
      Math.min(duration.value, a.currentTime + (e.key === 'ArrowRight' ? 10 : -10))
    );
    sync();
  }
  function changeVolume(e: Event) {
    if (audio.value) {
      audio.value.volume = Number((e.target as HTMLInputElement).value);
      audio.value.muted = false;
      sync();
    }
  }
  function mute() {
    if (audio.value) {
      audio.value.muted = !audio.value.muted;
      sync();
    }
  }
  function time(n: number) {
    const s = Math.floor(n || 0);
    return `${Math.floor(s / 3600) ? `${Math.floor(s / 3600)}:` : ''}${String(Math.floor(s / 60) % 60).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`;
  }
  onMounted(() => document.addEventListener('keydown', keyboard));
  onBeforeUnmount(() => {
    document.removeEventListener('keydown', keyboard);
    const a = audio.value;
    if (a) {
      a.pause();
      a.removeAttribute('src');
      a.load();
    }
  });
</script>
<template>
  <div class="audio-player">
    <div class="audio-stage"><Disc3 :size="46" :class="{ spinning: !paused }" /></div>
    <audio
      ref="audio"
      :src="src"
      autoplay
      preload="metadata"
      @loadedmetadata="sync"
      @durationchange="sync"
      @timeupdate="sync"
      @play="sync"
      @pause="sync"
      @ended="sync"
      @volumechange="sync"
      @error="error = '无法播放此音频，文件可能不可用或浏览器不支持其编码。'"
    />
    <div class="media-controls">
      <input
        class="media-seek"
        type="range"
        aria-label="播放进度"
        :aria-valuetext="`${time(current)} / ${time(duration)}`"
        min="0"
        :max="duration || 1"
        step="0.1"
        :value="current"
        :disabled="!duration"
        :style="{ '--progress': `${progress}%` }"
        @input="seek"
      />
      <div class="media-buttons">
        <button aria-label="上一首" title="上一首" :disabled="switching" @click="emit('previous')"
          ><SkipBack :size="17"
        /></button>
        <button :aria-label="paused ? '播放音频' : '暂停音频'" @click="toggle"
          ><Play v-if="paused" :size="19" /><Pause v-else :size="19"
        /></button>
        <button aria-label="下一首" title="下一首" :disabled="switching" @click="emit('next')"
          ><SkipForward :size="17"
        /></button>
        <span class="media-time"
          >{{ time(current) }} <span>/ {{ time(duration) }}</span></span
        >
        <div class="media-volume"
          ><button :aria-label="muted ? '取消静音' : '静音'" @click="mute"
            ><VolumeX v-if="muted || !volume" :size="18" /><Volume2 v-else :size="18" /></button
          ><input
            type="range"
            aria-label="音量"
            min="0"
            max="1"
            step="0.05"
            :value="muted ? 0 : volume"
            :style="{ '--progress': `${(muted ? 0 : volume) * 100}%` }"
            @input="changeVolume"
        /></div>
      </div>
    </div>
    <p v-if="error" class="media-error" role="alert">{{ error }}</p>
  </div>
</template>
<style scoped>
  .audio-player {
    background: #121817;
    border: 1px solid #26332e;
    border-radius: 10px;
    overflow: hidden;
    color: #eef5f1;
    display: flex;
    flex-direction: column;
    min-width: 0;
  }
  .audio-stage {
    display: grid;
    place-items: center;
    padding: 30px 0 24px;
    background: #090d0b;
  }
  .audio-stage svg {
    color: #76c9a5;
  }
  .audio-stage svg.spinning {
    animation: audio-spin 3.2s linear infinite;
  }
  @keyframes audio-spin {
    to {
      transform: rotate(360deg);
    }
  }
  .audio-player audio {
    display: none;
  }
  .media-controls {
    padding: 12px 16px 10px;
    background: #18211d;
  }
  .media-buttons {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-top: 8px;
  }
  .media-buttons button {
    border: 0;
    background: transparent;
    color: #eef5f1;
    padding: 7px;
    border-radius: 5px;
  }
  .media-buttons button:hover:not(:disabled) {
    background: #2c3c33;
  }
  .media-buttons button:disabled {
    opacity: 0.45;
    cursor: default;
  }
  .media-time {
    font-size: 12px;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }
  .media-time span {
    color: #9baea3;
  }
  .media-volume {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 5px;
  }
  .audio-player input[type='range'] {
    display: block;
    appearance: none;
    -webkit-appearance: none;
    border: 0;
    border-radius: 10px;
    height: 6px;
    min-height: 0;
    padding: 0;
    background: linear-gradient(to right, #76c9a5 var(--progress), #3c4d43 var(--progress));
    cursor: pointer;
    box-shadow: none;
  }
  .media-seek {
    width: 100%;
    margin: 6px 0;
  }
  .media-volume input {
    width: 72px;
  }
  .audio-player input::-webkit-slider-thumb {
    appearance: none;
    width: 13px;
    height: 13px;
    border-radius: 50%;
    background: #d9f5e7;
    border: 2px solid #76c9a5;
  }
  .audio-player input::-moz-range-thumb {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    background: #d9f5e7;
    border: 2px solid #76c9a5;
  }
  .media-error {
    padding: 10px 16px;
    color: #ffc9c9;
    font-size: 12px;
  }
  @media (max-width: 500px) {
    .media-controls {
      padding: 10px;
    }
    .media-buttons {
      gap: 6px;
    }
    .media-volume input {
      width: 48px;
    }
    .media-time {
      font-size: 10px;
    }
  }
</style>
