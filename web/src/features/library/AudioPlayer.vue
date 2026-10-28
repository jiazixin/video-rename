<script setup lang="ts">
  import { formatPlaybackTime } from '../../lib/format/date';
  import { ref, onMounted, onBeforeUnmount, computed, nextTick } from 'vue';
  import { Play, Pause, Volume2, VolumeX, Music, SkipBack, SkipForward } from '@lucide/vue';
  import type { Entry } from '../../app/contracts';
  import type { MediaQueue } from './media-preview';
  import { formatSize } from '../../lib/format/bytes';
  const {
    entry,
    switching,
    notice = '',
    position = '',
    queue
  } = defineProps<{
    entry: Entry;
    switching?: boolean;
    notice?: string;
    position?: string;
    // eslint-disable-next-line vue/require-default-prop -- undefined means still loading, null means failed
    queue?: MediaQueue | null;
  }>();
  const emit = defineEmits<{ previous: []; next: []; select: [entry: Entry] }>();
  const list = ref<HTMLElement>();
  // A stable colour per file name stands in for cover art, which the server does not extract.
  const tone = computed(() =>
    Array.from(entry.name).reduce((sum, ch) => (sum * 31 + ch.charCodeAt(0)) % 6, 1)
  );
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
  onMounted(() => {
    document.addEventListener('keydown', keyboard);
    void nextTick(revealCurrent);
  });
  // Keep the playing track in view inside the list without scrolling the dialog itself.
  function revealCurrent() {
    const box = list.value,
      row = box?.querySelector<HTMLElement>('[aria-current="true"]');
    if (!box || !row) return;
    if (
      row.offsetTop < box.scrollTop ||
      row.offsetTop + row.offsetHeight > box.scrollTop + box.clientHeight
    )
      box.scrollTop = row.offsetTop - box.clientHeight / 2 + row.offsetHeight / 2;
  }
  defineExpose({ revealCurrent });
  onBeforeUnmount(() => {
    document.removeEventListener('keydown', keyboard);
    const a = audio.value;
    if (a) {
      a.pause();
      a.removeAttribute('src');
      a.load();
    }
  });
  const queueHint = computed(() => {
    if (queue === null) return '同目录列表读取失败，仍可用上一首 / 下一首切换。';
    return queue ? '当前筛选下没有其他音频。' : '正在读取同目录音频…';
  });
</script>
<template>
  <div class="audio-deck">
    <section class="now-playing" aria-label="正在播放">
      <div class="cover" :data-tone="tone" aria-hidden="true"
        ><Music :size="38" :class="{ pulsing: !paused }"
      /></div>
      <p class="track-position">{{ position ? `第 ${position} 首` : '同目录音频' }}</p>
      <audio
        ref="audio"
        aria-label="音频播放器"
        :src="`/api/entries/${entry.id}/audio`"
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
      <div class="seek">
        <input
          class="media-seek"
          type="range"
          aria-label="播放进度"
          :aria-valuetext="`${formatPlaybackTime(current)} / ${formatPlaybackTime(duration)}`"
          min="0"
          :max="duration || 1"
          step="0.1"
          :value="current"
          :disabled="!duration"
          :style="{ '--progress': `${progress}%` }"
          @input="seek"
        /><span class="media-time"
          ><span>{{ formatPlaybackTime(current) }}</span
          ><span>{{ formatPlaybackTime(duration) }}</span></span
        >
      </div>
      <div class="transport">
        <button aria-label="上一首" title="上一首" :disabled="switching" @click="emit('previous')"
          ><SkipBack :size="18"
        /></button>
        <button class="play" :aria-label="paused ? '播放音频' : '暂停音频'" @click="toggle"
          ><Play v-if="paused" :size="20" /><Pause v-else :size="20"
        /></button>
        <button aria-label="下一首" title="下一首" :disabled="switching" @click="emit('next')"
          ><SkipForward :size="18"
        /></button>
      </div>
      <div class="media-volume"
        ><button :aria-label="muted ? '取消静音' : '静音'" @click="mute"
          ><VolumeX v-if="muted || !volume" :size="17" /><Volume2 v-else :size="17" /></button
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
      <p v-if="error" class="deck-status error" role="alert">{{ error }}</p>
      <p v-else-if="notice" class="deck-status" role="status">{{ notice }}</p>
    </section>
    <section class="track-list" aria-label="同目录音频">
      <header
        ><strong>同目录音频</strong
        ><span v-if="queue"
          >{{ queue.items.length }}{{ queue.complete ? '' : '+' }} 首</span
        ></header
      >
      <ol v-if="queue?.items.length" ref="list"
        ><li v-for="(item, index) in queue.items" :key="item.id"
          ><button
            :aria-label="`播放 ${item.name}`"
            :aria-current="item.id === entry.id ? 'true' : undefined"
            :class="{ playing: item.id === entry.id && !paused }"
            @click="item.id !== entry.id && emit('select', item)"
            ><span class="n">{{ String(index + 1).padStart(2, '0') }}</span
            ><span class="t" :title="item.name">{{ item.name }}</span
            ><span v-if="item.id === entry.id" class="eq" aria-hidden="true"><i /><i /><i /></span
            ><span v-else class="d">{{ formatSize(item.size) }}</span></button
          ></li
        ></ol
      >
      <p v-else class="list-empty">{{ queueHint }}</p>
      <p class="list-hint">← → 快退 / 快进 10 秒 · 使用浏览器原生解码</p>
    </section>
  </div>
</template>
<style scoped>
  /* Two panes: the track that is playing on the left, the folder it came from on the right. */
  .audio-deck {
    display: grid;
    grid-template-columns: minmax(0, 300px) minmax(0, 1fr);
    min-height: 0;
    height: min(460px, 70vh);
    border: 1px solid var(--line);
    border-radius: 12px;
    overflow: hidden;
  }
  .now-playing {
    display: flex;
    flex-direction: column;
    align-items: stretch;
    justify-content: center;
    gap: 12px;
    padding: 22px 20px;
    background: var(--tone-green-bg);
    min-width: 0;
  }
  .cover {
    align-self: center;
    width: 132px;
    aspect-ratio: 1;
    display: grid;
    place-items: center;
    border-radius: 16px;
    color: var(--palette-rgba-255-255-255-0_92);
    background: linear-gradient(135deg, var(--audio-cover-0-start), var(--audio-cover-0-end));
    box-shadow: 0 12px 26px var(--palette-rgba-20-40-30-0_18);
  }
  .cover[data-tone='1'] {
    background: linear-gradient(135deg, var(--audio-cover-1-start), var(--audio-cover-1-end));
  }
  .cover[data-tone='2'] {
    background: linear-gradient(135deg, var(--audio-cover-2-start), var(--audio-cover-2-end));
  }
  .cover[data-tone='3'] {
    background: linear-gradient(135deg, var(--audio-cover-3-start), var(--audio-cover-3-end));
  }
  .cover[data-tone='4'] {
    background: linear-gradient(135deg, var(--audio-cover-4-start), var(--audio-cover-4-end));
  }
  .cover[data-tone='5'] {
    background: linear-gradient(135deg, var(--audio-cover-5-start), var(--audio-cover-5-end));
  }
  .cover .pulsing {
    animation: cover-pulse 1.6s linear infinite;
  }
  @keyframes cover-pulse {
    50% {
      transform: scale(1.08);
    }
  }
  .track-position {
    margin: 0;
    text-align: center;
    font-size: 12px;
    color: var(--steel);
    font-variant-numeric: tabular-nums;
  }
  .seek {
    display: grid;
    gap: 4px;
  }
  .media-time {
    display: flex;
    justify-content: space-between;
    font-size: 11px;
    color: var(--ash);
    font-variant-numeric: tabular-nums;
  }
  .transport {
    display: flex;
    justify-content: center;
    align-items: center;
    gap: 14px;
  }
  .transport button,
  .media-volume button {
    border: 0;
    background: transparent;
    color: var(--steel);
    padding: 8px;
    border-radius: 50%;
  }
  .transport button:hover:not(:disabled),
  .media-volume button:hover:not(:disabled) {
    background: var(--palette-rgba-0-0-0-0_06);
    border: 0;
  }
  .transport .play,
  .transport .play:hover:not(:disabled) {
    width: 46px;
    height: 46px;
    padding: 0;
    background: var(--tone-green-ink);
    color: var(--palette-ffffff);
  }
  .transport .play:hover:not(:disabled) {
    filter: brightness(1.08);
  }
  .media-volume {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
  }
  .media-volume input {
    width: 110px;
  }
  .audio-deck input[type='range'] {
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
      var(--tone-green-ink) var(--progress),
      var(--tone-green-line) var(--progress)
    );
    cursor: pointer;
    box-shadow: none;
  }
  .media-seek {
    width: 100%;
  }
  .audio-deck input::-webkit-slider-thumb {
    appearance: none;
    width: 13px;
    height: 13px;
    border-radius: 50%;
    background: var(--palette-ffffff);
    box-shadow: 0 0 0 3px var(--tone-green-ink);
  }
  .audio-deck input::-moz-range-thumb {
    width: 10px;
    height: 10px;
    border: 0;
    border-radius: 50%;
    background: var(--palette-ffffff);
    box-shadow: 0 0 0 3px var(--tone-green-ink);
  }
  .deck-status {
    margin: 0;
    font-size: 12px;
    line-height: 1.6;
    text-align: center;
    color: var(--steel);
  }
  .deck-status.error {
    color: var(--tone-red-ink);
  }
  .track-list {
    display: flex;
    flex-direction: column;
    min-height: 0;
    min-width: 0;
  }
  .track-list header {
    display: flex;
    align-items: baseline;
    gap: 8px;
    padding: 14px 16px 10px;
    border-bottom: 1px solid var(--line);
    font-size: 13px;
  }
  .track-list header span {
    font-size: 12px;
    color: var(--ash);
  }
  ol {
    flex: 1;
    min-height: 0;
    overflow: auto;
    list-style: none;
    margin: 0;
    padding: 6px;
    position: relative;
  }
  ol button {
    width: 100%;
    display: grid;
    grid-template-columns: 24px minmax(0, 1fr) auto;
    gap: 10px;
    align-items: center;
    padding: 8px 10px;
    border: 0;
    border-radius: 8px;
    background: transparent;
    text-align: left;
    font-size: 13px;
    font-weight: 400;
    color: var(--ink);
  }
  ol button:hover:not(:disabled) {
    background: var(--control-hover);
    border: 0;
  }
  ol button[aria-current='true'],
  ol button[aria-current='true']:hover:not(:disabled) {
    background: var(--tone-green-bg);
    color: var(--tone-green-ink);
    font-weight: 600;
  }
  .n,
  .d {
    font-size: 11px;
    color: var(--ash);
    font-variant-numeric: tabular-nums;
  }
  .t {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .eq {
    display: inline-flex;
    align-items: flex-end;
    gap: 2px;
    height: 12px;
  }
  .eq i {
    width: 2px;
    height: 40%;
    border-radius: 1px;
    background: currentColor;
  }
  .playing .eq i {
    animation: eq 0.9s linear infinite;
  }
  .playing .eq i:nth-child(2) {
    animation-delay: -0.3s;
  }
  .playing .eq i:nth-child(3) {
    animation-delay: -0.6s;
  }
  @keyframes eq {
    0%,
    100% {
      height: 30%;
    }
    50% {
      height: 100%;
    }
  }
  .list-empty {
    flex: 1;
    margin: 0;
    padding: 24px 16px;
    font-size: 12px;
    color: var(--ash);
    text-align: center;
  }
  .list-hint {
    margin: 0;
    padding: 8px 16px 10px;
    border-top: 1px solid var(--line);
    font-size: 11px;
    color: var(--ash);
  }
  @media (max-width: 700px) {
    .audio-deck {
      grid-template-columns: minmax(0, 1fr);
      height: auto;
    }
    .track-list {
      max-height: 240px;
      border-top: 1px solid var(--line);
    }
    .cover {
      width: 96px;
    }
    .list-hint {
      display: none;
    }
  }
</style>
