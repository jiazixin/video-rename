<script setup lang="ts">
import {Folder,FileVideo,Image,FileAudio} from 'lucide-vue-next'
import type {Entry} from '../../app/api'
defineProps<{contents?:Entry['contents']}>()
const types=[
 {key:'directories',label:'子目录',icon:Folder},
 {key:'videos',label:'视频',icon:FileVideo},
 {key:'images',label:'图片',icon:Image},
 {key:'audio',label:'音频',icon:FileAudio},
] as const
</script>

<template>
 <div v-if="contents" class="directory-contents" role="group" aria-label="目录内容（仅直接子项）" title="最近完整索引的直接子项数量，包含全部分页，不受搜索和类型筛选影响">
  <span v-for="type in types" :key="type.key" class="content-stat" :class="type.key">
   <component :is="type.icon" :size="14" aria-hidden="true"/>
   <span>{{type.label}}</span><strong>{{contents[type.key].toLocaleString()}}</strong>
  </span>
 </div>
 <span v-else class="contents-pending">目录内容待统计</span>
</template>

<style scoped>
.directory-contents{display:inline-flex;align-items:center;gap:0;max-width:100%;padding:6px 2px;border:1px solid #e4e7eb;border-radius:8px;background:#f8fafb;vertical-align:middle}
.content-stat{display:inline-flex;align-items:center;gap:5px;padding:0 9px;white-space:nowrap;font-size:11px;line-height:18px;color:#60646c}
.content-stat+.content-stat{border-left:1px solid #e0e5e9}
.content-stat strong{font-size:12px;font-weight:600;font-variant-numeric:tabular-nums;color:#303940}
.directories svg{color:#657b6c}.videos svg{color:#366ba0}.images svg{color:#785ba0}.audio svg{color:#896018}
.contents-pending{font-size:11px;color:var(--steel);white-space:nowrap}
@media(max-width:600px){.directory-contents{flex-wrap:wrap;row-gap:6px}.content-stat{padding:0 7px}}
</style>
