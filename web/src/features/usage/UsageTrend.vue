<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref} from 'vue'
import type {UsageBucket} from '../../app/api'
const props=defineProps<{buckets:UsageBucket[];interval:string}>()
const box=ref<HTMLElement>(),width=ref(860),height=250,active=ref(-1)
const pad={top:18,right:18,bottom:34,left:62}
const inner=()=>({w:Math.max(80,width.value-pad.left-pad.right),h:height-pad.top-pad.bottom})
const maxY=computed(()=>{let top=0;for(const b of props.buckets)if(b.tokens>top)top=b.tokens;return niceCeil(top)})
function niceCeil(value:number){if(value<=0)return 1;const pow=10**Math.floor(Math.log10(value));const f=value/pow;return (f<=1?1:f<=2?2:f<=5?5:10)*pow}
function compact(value:number){if(value>=999_950)return `${(value/1e6).toFixed(2).replace(/\.?0+$/,'')}M`;if(value>=1000)return `${(value/1000).toFixed(1).replace(/\.?0+$/,'')}K`;return String(value)}
const ticks=computed(()=>{const{h}=inner(),out:number[]=[];for(let i=0;i<=4;i++)out.push(maxY.value*i/4);return out.reverse()})
const points=computed(()=>{const{w,h}=inner(),n=props.buckets.length;return props.buckets.map((b,i)=>({x:pad.left+(n===1?w/2:w*i/(n-1)),y:pad.top+h-(b.tokens/maxY.value)*h,b}))})
const linePath=computed(()=>points.value.map(p=>`${p.x},${p.y}`).join(' '))
const areaPath=computed(()=>{if(!points.value.length)return '';const{h}=inner(),first=points.value[0]!,last=points.value[points.value.length-1]!;return `M${first.x},${pad.top+h} L${points.value.map(p=>`${p.x},${p.y}`).join(' L')} L${last.x},${pad.top+h} Z`})
const labelSteps=computed(()=>{const n=props.buckets.length;return Math.max(1,Math.ceil(n/6))})
function axisLabel(value:string){const d=new Date(value);switch(props.interval){case '15m':case 'hour':return d.toLocaleTimeString('zh-CN',{hour12:false,hour:'2-digit',minute:'2-digit'});case 'day':return d.toLocaleDateString('zh-CN',{month:'numeric',day:'numeric'});case 'month':return `${d.getFullYear()}-${d.getMonth()+1}`;default:return String(d.getFullYear())}}
function bucketTitle(b:UsageBucket){return `${new Date(b.from).toLocaleString('zh-CN',{hour12:false})} – ${new Date(b.to).toLocaleTimeString('zh-CN',{hour12:false})}`}
function pick(event:PointerEvent){const n=props.buckets.length;if(!n)return;const svg=(event.currentTarget as SVGRectElement).ownerSVGElement!;const rect=svg.getBoundingClientRect();const x=(event.clientX-rect.left)*width.value/rect.width;const ratio=(x-pad.left)/inner().w;active.value=Math.max(0,Math.min(n-1,Math.round(ratio*(n-1))))}
function keys(event:KeyboardEvent){if(event.key==='Escape'){active.value=-1;return}if(event.key!=='ArrowLeft'&&event.key!=='ArrowRight')return;event.preventDefault();const n=props.buckets.length;if(!n)return;active.value=active.value<0?event.key==='ArrowLeft'?n-1:0:Math.max(0,Math.min(n-1,active.value+(event.key==='ArrowRight'?1:-1)))}
const tooltipStyle=computed(()=>{const p=points.value[active.value];if(!p)return {};return {left:`${Math.max(0,Math.min(width.value-190,p.x-95))}px`}})
function resize(){const el=box.value;if(el)width.value=Math.max(320,el.clientWidth)}
let observer:ResizeObserver|undefined
onMounted(()=>{resize();observer=new ResizeObserver(resize);if(box.value)observer.observe(box.value)})
onUnmounted(()=>observer?.disconnect())
</script>
<template><div ref="box" class="usage-trend"><svg :width="width" :height="height" role="img" aria-label="Token 消耗趋势图，精确数值见下方数据表" tabindex="0" @keydown="keys" @pointerleave="active=-1"><line v-for="t in ticks" :key="t" :x1="pad.left" :x2="width-pad.right" :y1="pad.top+inner().h-(t/maxY)*inner().h" :y2="pad.top+inner().h-(t/maxY)*inner().h" class="gridline"/><text v-for="t in ticks" :key="t" :x="pad.left-10" :y="pad.top+inner().h-(t/maxY)*inner().h+4" class="axis-text" text-anchor="end">{{compact(t)}}</text><path v-if="areaPath" :d="areaPath" class="trend-area"/><polyline v-if="points.length" :points="linePath" class="trend-line"/><template v-if="active>=0&&points[active]"><line :x1="points[active]!.x" :x2="points[active]!.x" :y1="pad.top" :y2="pad.top+inner().h" class="crosshair"/><circle :cx="points[active]!.x" :cy="points[active]!.y" r="4.5" class="trend-dot"/></template><text v-for="(p,i) in points.filter((_,i)=>i%labelSteps===0||i===points.length-1)" :key="p.b.from" :x="p.x" :y="height-10" class="axis-text" text-anchor="middle">{{axisLabel(p.b.from)}}</text><rect v-for="(p,i) in points" :key="`h${i}`" :x="points.length===1?p.x-inner().w/2:p.x-inner().w/(points.length-1)/2" :y="pad.top" :width="points.length===1?inner().w:inner().w/(points.length-1)" :height="inner().h" class="hit-zone" @pointermove="pick"/></svg><div v-if="active>=0&&points[active]" class="trend-tooltip" :style="tooltipStyle" role="status"><strong>{{points[active]!.b.tokens.toLocaleString()}} token</strong><span>{{bucketTitle(points[active]!.b)}}</span><span>{{points[active]!.b.records.toLocaleString()}} 条请求<template v-if="points[active]!.b.estimated_records"> · 粗估 {{points[active]!.b.estimated_records}}</template></span></div><p v-if="buckets.every(b=>!b.tokens)" class="trend-empty">所选区间没有记录到的用量</p><p class="trend-hint">方向键查看每个时间点，精确数值见数据表</p></div></template>
