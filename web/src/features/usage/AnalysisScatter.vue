<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref} from 'vue'
export interface ScatterPoint{run:number;items:number;ms:number;status:string;source:string;profile_title:string}
const props=defineProps<{points:ScatterPoint[];formatMs:(v:number)=>string;statusName:(s:string)=>string;color:string}>()
const box=ref<HTMLElement>(),width=ref(860),height=280,active=ref(-1)
const pad={top:16,right:18,bottom:38,left:62}
const inner=()=>({w:Math.max(80,width.value-pad.left-pad.right),h:height-pad.top-pad.bottom})
function niceCeil(value:number){if(value<=0)return 1;const pow=10**Math.floor(Math.log10(value));const f=value/pow;return (f<=1?1:f<=2?2:f<=5?5:10)*pow}
function compact(value:number){if(value>=999_950)return `${(value/1e6).toFixed(2).replace(/\.?0+$/,'')}M`;if(value>=1000)return `${(value/1000).toFixed(1).replace(/\.?0+$/,'')}K`;return String(Math.round(value))}
const maxX=computed(()=>niceCeil(Math.max(1,...props.points.map(p=>p.items))))
const maxY=computed(()=>niceCeil(Math.max(1,...props.points.map(p=>p.ms))))
const yTicks=computed(()=>{const out:number[]=[];for(let i=0;i<=4;i++)out.push(maxY.value*i/4);return out.reverse()})
const xTicks=computed(()=>{const out:number[]=[];for(let i=0;i<=4;i++)out.push(maxX.value*i/4);return out})
const placed=computed(()=>{const{w,h}=inner();return props.points.map((p,i)=>({...p,i,x:pad.left+(p.items/maxX.value)*w,y:pad.top+h-(p.ms/maxY.value)*h}))})
function pick(event:PointerEvent){const rect=(event.currentTarget as SVGRectElement).getBoundingClientRect();const x=pad.left+(event.clientX-rect.left)*inner().w/rect.width,y=pad.top+(event.clientY-rect.top)*inner().h/rect.height;let best=-1,bestDist=Infinity;for(const p of placed.value){const d=(p.x-x)**2+(p.y-y)**2;if(d<bestDist){bestDist=d;best=p.i}}active.value=best}
function keys(event:KeyboardEvent){if(event.key==='Escape'){active.value=-1;return}if(event.key!=='ArrowLeft'&&event.key!=='ArrowRight')return;event.preventDefault();if(!props.points.length)return;active.value=active.value<0?event.key==='ArrowLeft'?props.points.length-1:0:Math.max(0,Math.min(props.points.length-1,active.value+(event.key==='ArrowRight'?1:-1)))}
const tooltipStyle=computed(()=>{const p=placed.value[active.value];if(!p)return {};return {left:`${Math.max(0,Math.min(width.value-210,p.x-105))}px`,top:`${Math.max(0,p.y-84)}px`}})
function resize(){const el=box.value;if(el)width.value=Math.max(320,el.clientWidth)}
let observer:ResizeObserver|undefined
onMounted(()=>{resize();observer=new ResizeObserver(resize);if(box.value)observer.observe(box.value)})
onUnmounted(()=>observer?.disconnect())
</script>
<template><div ref="box" class="usage-trend analysis-scatter">
 <svg :width="width" :height="height" role="img" aria-label="批次大小与批次耗时散点图，每行数值见下方数据表" tabindex="0" @keydown="keys" @pointerleave="active=-1">
  <line v-for="t in yTicks" :key="`y${t}`" :x1="pad.left" :x2="width-pad.right" :y1="pad.top+inner().h-(t/maxY)*inner().h" :y2="pad.top+inner().h-(t/maxY)*inner().h" class="gridline"/>
  <text v-for="t in yTicks" :key="`yt${t}`" :x="pad.left-10" :y="pad.top+inner().h-(t/maxY)*inner().h+4" class="axis-text" text-anchor="end">{{compact(t)}}</text>
  <text v-for="t in xTicks" :key="`xt${t}`" :x="pad.left+(t/maxX)*inner().w" :y="height-16" class="axis-text" text-anchor="middle">{{compact(t)}}</text>
  <text :x="pad.left+inner().w/2" :y="height-3" class="axis-text axis-caption" text-anchor="middle">批次目标数（个）</text>
  <circle v-for="p in placed" :key="p.run" :cx="p.x" :cy="p.y" r="4" class="scatter-dot" :fill="color"/>
  <circle v-if="active>=0&&placed[active]" :cx="placed[active]!.x" :cy="placed[active]!.y" r="7" class="scatter-halo" :stroke="color"/>
  <rect :x="pad.left" :y="pad.top" :width="inner().w" :height="inner().h" class="hit-zone" @pointermove="pick"/>
 </svg>
 <div v-if="active>=0&&placed[active]" class="trend-tooltip" :style="tooltipStyle" role="status">
  <strong>批次 #{{placed[active]!.run}} · {{formatMs(placed[active]!.ms)}}</strong>
  <span>{{placed[active]!.items.toLocaleString()}} 个目标 · {{placed[active]!.source==='manual'?'手动':'自动计划'}} · {{placed[active]!.profile_title||'未命名配置'}}</span>
  <span>状态：{{statusName(placed[active]!.status)}}</span>
 </div>
 <p v-if="!points.length" class="trend-empty">区间内没有已完成的批次</p>
 <p class="trend-hint">悬停或方向键查看每个批次，y 轴为批次耗时</p>
</div></template>
