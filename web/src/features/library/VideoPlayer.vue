<script setup lang="ts">
import {ref,onMounted,onBeforeUnmount,computed} from 'vue'
import {Play,Pause,Volume2,VolumeX,Maximize,Minimize,ChevronLeft,ChevronRight} from 'lucide-vue-next'
defineProps<{src:string;switching?:boolean}>()
const emit=defineEmits<{previous:[];next:[]}>()
const video=ref<HTMLVideoElement>(),shell=ref<HTMLElement>()
const paused=ref(true),current=ref(0),duration=ref(0),volume=ref(1),muted=ref(false),error=ref(''),fullscreen=ref(false)
const progress=computed(()=>duration.value?current.value/duration.value*100:0)
function sync(){const v=video.value;if(!v)return;paused.value=v.paused;current.value=v.currentTime;duration.value=Number.isFinite(v.duration)?v.duration:0;volume.value=v.volume;muted.value=v.muted}
async function toggle(){const v=video.value;if(!v)return;if(!v.paused)v.pause();else try{await v.play();error.value=''}catch{error.value='播放未能开始，请重试或检查视频编码。'}}
function seek(e:Event){if(video.value&&duration.value){video.value.currentTime=Math.min(duration.value,Math.max(0,Number((e.target as HTMLInputElement).value)));sync()}}
function keyboard(e:KeyboardEvent){
 if(e.altKey||e.ctrlKey||e.metaKey||!['ArrowLeft','ArrowRight'].includes(e.key))return
 const target=e.target as HTMLElement|null
 if(target?.isContentEditable||target?.closest('textarea,select,input:not([type="range"])'))return
 const v=video.value
 if(!v||!duration.value)return
 e.preventDefault()
 v.currentTime=Math.max(0,Math.min(duration.value,v.currentTime+(e.key==='ArrowRight'?10:-10)))
 sync()
}
function changeVolume(e:Event){if(video.value){video.value.volume=Number((e.target as HTMLInputElement).value);video.value.muted=false;sync()}}
function mute(){if(video.value){video.value.muted=!video.value.muted;sync()}}
function time(n:number){const s=Math.floor(n||0);return `${Math.floor(s/3600)?`${Math.floor(s/3600)}:`:''}${String(Math.floor(s/60)%60).padStart(2,'0')}:${String(s%60).padStart(2,'0')}`}
async function expand(){try{if(document.fullscreenElement===shell.value)await document.exitFullscreen();else await shell.value?.requestFullscreen()}catch{error.value='当前浏览器无法进入全屏。'}}
function fullChanged(){fullscreen.value=document.fullscreenElement===shell.value}
onMounted(()=>{document.addEventListener('fullscreenchange',fullChanged);document.addEventListener('keydown',keyboard)})
onBeforeUnmount(()=>{document.removeEventListener('fullscreenchange',fullChanged);document.removeEventListener('keydown',keyboard);const v=video.value;if(v){v.pause();v.removeAttribute('src');v.load()}})
</script>
<template>
<div ref="shell" class="media-player">
 <div class="video-stage"><button class="episode previous" aria-label="上一集" title="上一集" :disabled="switching" @click="emit('previous')"><ChevronLeft :size="28"/></button>
 <video ref="video" class="video-player" :src="src" autoplay playsinline preload="metadata" @click="toggle" @loadedmetadata="sync" @durationchange="sync" @timeupdate="sync" @play="sync" @pause="sync" @ended="sync" @volumechange="sync" @error="error='无法播放此视频，文件可能不可用或浏览器不支持其编码。'"/>
 <button class="episode next" aria-label="下一集" title="下一集" :disabled="switching" @click="emit('next')"><ChevronRight :size="28"/></button></div>
 <div class="media-controls">
  <input class="media-seek" type="range" aria-label="播放进度" :aria-valuetext="`${time(current)} / ${time(duration)}`" min="0" :max="duration||1" step="0.1" :value="current" :disabled="!duration" :style="{'--progress':`${progress}%`}" @input="seek"/>
  <div class="media-buttons">
   <button :aria-label="paused?'播放视频':'暂停视频'" @click="toggle"><Play v-if="paused" :size="19"/><Pause v-else :size="19"/></button>
   <span class="media-time">{{time(current)}} <span>/ {{time(duration)}}</span></span>
   <div class="media-volume"><button :aria-label="muted?'取消静音':'静音'" @click="mute"><VolumeX v-if="muted||!volume" :size="18"/><Volume2 v-else :size="18"/></button><input type="range" aria-label="音量" min="0" max="1" step="0.05" :value="muted?0:volume" :style="{'--progress':`${(muted?0:volume)*100}%`}" @input="changeVolume"/></div>
   <button :aria-label="fullscreen?'退出全屏':'全屏播放'" @click="expand"><Minimize v-if="fullscreen" :size="18"/><Maximize v-else :size="18"/></button>
  </div>
 </div>
 <p v-if="error" class="media-error" role="alert">{{error}}</p>
</div>
</template>
<style scoped>
.media-player{background:#121817;border:1px solid #26332e;border-radius:10px;overflow:hidden;color:#eef5f1;display:flex;flex-direction:column;min-width:0}.media-player .video-player{width:100%;height:72vh;max-height:calc(96vh - 160px);min-height:100px;border-radius:0;object-fit:contain;background:#090d0b;flex:1;min-width:0}.media-controls{padding:12px 16px 10px;flex-shrink:0;background:#18211d}.media-buttons{display:flex;align-items:center;gap:12px;margin-top:8px}.media-buttons button{border:0;background:transparent;color:#eef5f1;padding:7px;border-radius:5px}.media-buttons button:hover{background:#2c3c33}.media-time{font-size:12px;font-variant-numeric:tabular-nums;white-space:nowrap}.media-time span{color:#9baea3}.media-volume{margin-left:auto;display:flex;align-items:center;gap:5px}.media-player input[type=range]{display:block;appearance:none;-webkit-appearance:none;border:0;border-radius:10px;height:6px;min-height:0;padding:0;background:linear-gradient(to right,#76c9a5 var(--progress),#3c4d43 var(--progress));cursor:pointer;box-shadow:none}.media-seek{width:100%;margin:6px 0}.media-volume input{width:72px}.media-player input::-webkit-slider-thumb{appearance:none;width:13px;height:13px;border-radius:50%;background:#d9f5e7;border:2px solid #76c9a5}.media-player input::-moz-range-thumb{width:10px;height:10px;border-radius:50%;background:#d9f5e7;border:2px solid #76c9a5}.media-error{padding:10px 16px;color:#ffc9c9;font-size:12px}.media-player:fullscreen{border:0;border-radius:0;width:100%;height:100%;justify-content:center}.media-player:fullscreen .video-stage{flex:1;min-height:0}.media-player:fullscreen .video-player{height:100%;max-height:calc(100vh - 100px);min-height:0}.media-player:fullscreen .media-controls{width:100%}@media(max-width:500px){.media-controls{padding:10px}.media-buttons{gap:6px}.media-volume input{width:48px}.media-time{font-size:10px}}
.video-stage{position:relative;min-height:0;background:#090d0b}.episode{position:absolute;top:50%;transform:translateY(-50%);z-index:1;border:1px solid #ffffff55;border-radius:8px;padding:12px 5px;background:#101817bb;color:white}.episode:hover{background:#33483ddd}.episode.previous{left:10px}.episode.next{right:10px}
</style>
