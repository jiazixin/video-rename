<script setup lang="ts">
  import { ref, onMounted, onBeforeUnmount, computed } from 'vue';
  import {
    Play,
    Pause,
    Volume2,
    VolumeX,
    Maximize,
    Minimize,
    ChevronLeft,
    ChevronRight
  } from '@lucide/vue';
  defineProps<{ src: string; switching?: boolean; notice?: string }>();
  const emit = defineEmits<{ previous: []; next: [] }>();
  const video = ref<HTMLVideoElement>(),
    shell = ref<HTMLElement>();
  const paused = ref(true),
    current = ref(0),
    duration = ref(0),
    volume = ref(1),
    muted = ref(false),
    error = ref(''),
    fullscreen = ref(false),
    speed = ref(1);
  const speeds = [1, 1.25, 1.5, 2, 0.5, 0.75];
  // Cycles through the common rates; the next click after 2× drops to half speed.
  function nextSpeed() {
    speed.value = speeds[(speeds.indexOf(speed.value) + 1) % speeds.length]!;
    if (video.value) video.value.playbackRate = speed.value;
  }
  const progress = computed(() => (duration.value ? (current.value / duration.value) * 100 : 0));
  function sync() {
    const v = video.value;
    if (!v) return;
    paused.value = v.paused;
    current.value = v.currentTime;
    duration.value = Number.isFinite(v.duration) ? v.duration : 0;
    volume.value = v.volume;
    muted.value = v.muted;
    speed.value = v.playbackRate;
  }
  async function toggle() {
    const v = video.value;
    if (!v) return;
    if (!v.paused) v.pause();
    else
      try {
        await v.play();
        error.value = '';
      } catch {
        error.value = '播放未能开始，请重试或检查视频编码。';
      }
  }
  function seek(e: Event) {
    if (video.value && duration.value) {
      video.value.currentTime = Math.min(
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
    const v = video.value;
    if (!v || !duration.value) return;
    e.preventDefault();
    v.currentTime = Math.max(
      0,
      Math.min(duration.value, v.currentTime + (e.key === 'ArrowRight' ? 10 : -10))
    );
    sync();
  }
  function changeVolume(e: Event) {
    if (video.value) {
      video.value.volume = Number((e.target as HTMLInputElement).value);
      video.value.muted = false;
      sync();
    }
  }
  function mute() {
    if (video.value) {
      video.value.muted = !video.value.muted;
      sync();
    }
  }
  function time(n: number) {
    const s = Math.floor(n || 0);
    return `${Math.floor(s / 3600) ? `${Math.floor(s / 3600)}:` : ''}${String(Math.floor(s / 60) % 60).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`;
  }
  async function expand() {
    try {
      if (document.fullscreenElement === shell.value) await document.exitFullscreen();
      else await shell.value?.requestFullscreen();
    } catch {
      error.value = '当前浏览器无法进入全屏。';
    }
  }
  function fullChanged() {
    fullscreen.value = document.fullscreenElement === shell.value;
  }
  onMounted(() => {
    document.addEventListener('fullscreenchange', fullChanged);
    document.addEventListener('keydown', keyboard);
  });
  onBeforeUnmount(() => {
    document.removeEventListener('fullscreenchange', fullChanged);
    document.removeEventListener('keydown', keyboard);
    const v = video.value;
    if (v) {
      v.pause();
      v.removeAttribute('src');
      v.load();
    }
  });
</script>
<template>
  <div ref="shell" class="video-theater">
    <div class="video-stage">
      <video
        ref="video"
        class="video-player"
        :src="src"
        autoplay
        playsinline
        preload="metadata"
        @click="toggle"
        @loadedmetadata="sync"
        @durationchange="sync"
        @timeupdate="sync"
        @play="sync"
        @pause="sync"
        @ended="sync"
        @volumechange="sync"
        @ratechange="sync"
        @error="error = '无法播放此视频，文件可能不可用或浏览器不支持其编码。'"
      />
      <button
        v-if="paused && !error"
        class="stage-play"
        tabindex="-1"
        aria-hidden="true"
        @click="toggle"
        ><Play :size="28"
      /></button>
      <button
        class="episode previous"
        aria-label="上一集"
        title="上一集"
        :disabled="switching"
        @click="emit('previous')"
        ><ChevronLeft :size="30"
      /></button>
      <button
        class="episode next"
        aria-label="下一集"
        title="下一集"
        :disabled="switching"
        @click="emit('next')"
        ><ChevronRight :size="30"
      /></button>
    </div>
    <div class="video-bar">
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
      <div class="bar-row">
        <button :aria-label="paused ? '播放视频' : '暂停视频'" @click="toggle"
          ><Play v-if="paused" :size="19" /><Pause v-else :size="19"
        /></button>
        <span class="media-time"
          >{{ time(current) }} <span>/ {{ time(duration) }}</span></span
        >
        <p class="bar-status" role="status">
          <span v-if="error" class="bar-error">{{ error }}</span
          ><span v-else-if="notice">{{ notice }}</span
          ><span v-else class="bar-hint">← → 快退 / 快进 10 秒 · 两侧切换上一集 / 下一集</span>
        </p>
        <button class="speed" :aria-label="`播放速度 ${speed}×`" title="播放速度" @click="nextSpeed"
          >{{ speed }}×</button
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
        <button :aria-label="fullscreen ? '退出全屏' : '全屏播放'" @click="expand"
          ><Minimize v-if="fullscreen" :size="18" /><Maximize v-else :size="18"
        /></button>
      </div>
    </div>
  </div>
</template>
<style scoped>
  /* Theater layout: the picture takes every spare pixel; the control bar stays fixed below it. */
  .video-theater {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    min-width: 0;
    border-radius: 10px;
    overflow: hidden;
    background: var(--stage);
    color: var(--stage-ink);
  }
  .video-stage {
    position: relative;
    flex: 1;
    min-height: 0;
    display: grid;
    background: #000;
  }
  .video-player {
    width: 100%;
    height: 100%;
    min-height: 0;
    object-fit: contain;
    background: #000;
    cursor: pointer;
  }
  .stage-play {
    position: absolute;
    left: 50%;
    top: 50%;
    width: 64px;
    height: 64px;
    padding: 0 0 0 4px;
    border: 0;
    border-radius: 50%;
    transform: translate(-50%, -50%);
    background: rgba(255, 255, 255, 0.92);
    color: #0c1110;
  }
  .stage-play:hover:not(:disabled) {
    background: #fff;
    border: 0;
  }
  /* Previous/next: full-height strips at the edges, dim until the pointer gets close. */
  .episode {
    position: absolute;
    top: 0;
    bottom: 0;
    width: clamp(48px, 7%, 96px);
    padding: 0;
    border: 0;
    border-radius: 0;
    color: var(--stage-ink);
    background: transparent;
    opacity: 0.55;
    transition:
      opacity var(--motion-hover) linear,
      background var(--motion-hover) linear;
  }
  .episode.previous {
    left: 0;
  }
  .episode.next {
    right: 0;
  }
  .episode:hover:not(:disabled),
  .episode:focus-visible {
    opacity: 1;
    border: 0;
  }
  .episode.previous:hover:not(:disabled) {
    background: linear-gradient(to right, rgba(0, 0, 0, 0.45), transparent);
  }
  .episode.next:hover:not(:disabled) {
    background: linear-gradient(to left, rgba(0, 0, 0, 0.45), transparent);
  }
  .episode:disabled {
    opacity: 0.25;
  }
  .video-bar {
    flex-shrink: 0;
    padding: 12px 14px 10px;
    background: var(--stage-bar);
    border-top: 1px solid var(--stage-line);
  }
  .bar-row {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-top: 8px;
  }
  .bar-row button {
    border: 0;
    background: transparent;
    color: var(--stage-ink);
    padding: 7px;
    border-radius: 8px;
  }
  .bar-row button:hover:not(:disabled) {
    background: var(--stage-hover);
    border: 0;
  }
  .media-time {
    font-size: 12px;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }
  .media-time span {
    color: var(--stage-dim);
  }
  .bar-status {
    flex: 1;
    min-width: 0;
    margin: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 12px;
    line-height: 1.5;
    color: var(--stage-ink);
  }
  .bar-hint {
    color: var(--stage-dim);
  }
  .bar-error {
    color: #ffc9c9;
  }
  .bar-row .speed {
    min-width: 46px;
    padding: 4px 8px;
    border: 1px solid var(--stage-line);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
  }
  .bar-row .speed:hover:not(:disabled) {
    border: 1px solid var(--stage-line);
  }
  .media-volume {
    display: flex;
    align-items: center;
    gap: 5px;
  }
  .video-theater input[type='range'] {
    display: block;
    appearance: none;
    -webkit-appearance: none;
    border: 0;
    border-radius: 10px;
    height: 5px;
    min-height: 0;
    padding: 0;
    background: linear-gradient(
      to right,
      var(--stage-accent) var(--progress),
      rgba(255, 255, 255, 0.2) var(--progress)
    );
    cursor: pointer;
    box-shadow: none;
    transition: height var(--motion-hover) linear;
  }
  .video-theater input[type='range']:hover {
    height: 7px;
  }
  .media-seek {
    width: 100%;
    margin: 4px 0;
  }
  .media-volume input {
    width: 76px;
  }
  .video-theater input::-webkit-slider-thumb {
    appearance: none;
    width: 13px;
    height: 13px;
    border-radius: 50%;
    background: #fff;
    box-shadow: 0 0 0 3px var(--stage-accent);
  }
  .video-theater input::-moz-range-thumb {
    width: 10px;
    height: 10px;
    border: 0;
    border-radius: 50%;
    background: #fff;
    box-shadow: 0 0 0 3px var(--stage-accent);
  }
  .video-theater:fullscreen {
    border-radius: 0;
  }
  @media (max-width: 700px) {
    .bar-hint {
      display: none;
    }
  }
  @media (max-width: 500px) {
    .video-bar {
      padding: 10px;
    }
    .bar-row {
      gap: 4px;
    }
    .media-volume input {
      width: 48px;
    }
    .media-time {
      font-size: 11px;
    }
  }
  @media (pointer: coarse) {
    .episode {
      opacity: 0.85;
    }
  }
</style>
