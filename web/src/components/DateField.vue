<script setup lang="ts">
import {computed,nextTick,onMounted,onUnmounted,ref,useId} from 'vue'
import {CalendarDays,ChevronLeft,ChevronRight} from 'lucide-vue-next'
import SelectField from './SelectField.vue'
const props=defineProps<{modelValue:string;label:string;min?:string;max?:string}>()
const emit=defineEmits<{'update:modelValue':[string];change:[]}>()
const id=useId(),root=ref<HTMLElement>(),panel=ref<HTMLElement>(),trigger=ref<HTMLButtonElement>(),open=ref(false),active=ref('')
const now=new Date(),year=ref(now.getFullYear()),month=ref(now.getMonth()),position=ref({left:'0px',top:'0px'})
const format=(date:Date)=>`${date.getFullYear()}-${String(date.getMonth()+1).padStart(2,'0')}-${String(date.getDate()).padStart(2,'0')}`
const parse=(value:string)=>new Date(`${value}T12:00:00`)
const today=format(now)
const years=computed(()=>Array.from({length:Math.max(now.getFullYear()+21,year.value+1)-1900},(_,i)=>({value:1900+i,label:`${1900+i} 年`})))
const months=Array.from({length:12},(_,i)=>({value:i,label:`${i+1} 月`}))
const days=computed(()=>{const first=new Date(year.value,month.value,1,12);first.setDate(1-(first.getDay()+6)%7);return Array.from({length:42},(_,i)=>{const d=new Date(first);d.setDate(first.getDate()+i);return {value:format(d),day:d.getDate(),outside:d.getMonth()!==month.value}})})
const disabled=(value:string)=>!!(props.min&&value<props.min||props.max&&value>props.max)
function place(){const r=trigger.value!.getBoundingClientRect(),height=panel.value?.offsetHeight||360,width=panel.value?.offsetWidth||296;position.value={left:`${Math.max(8,Math.min(r.left+(r.width-width)/2,innerWidth-width-8))}px`,top:`${innerHeight-r.bottom>=height+12?r.bottom+6:Math.max(8,r.top-height-6)}px`}}
function changeMonth(){active.value=format(new Date(year.value,month.value,1,12))}
function close(){open.value=false}
async function show(){if(open.value){close();return}let value=props.modelValue||today;if(props.min&&value<props.min)value=props.min;if(props.max&&value>props.max)value=props.max;const d=parse(value);year.value=d.getFullYear();month.value=d.getMonth();active.value=value;open.value=true;await nextTick();place();focusDay()}
function focusDay(){document.getElementById(`${id}-${active.value}`)?.focus({preventScroll:true})}
function choose(value:string){if(value&&disabled(value))return;emit('update:modelValue',value);emit('change');close();trigger.value?.focus()}
async function shift(delta:number){const d=new Date(year.value,month.value+delta,1,12);year.value=d.getFullYear();month.value=d.getMonth();active.value=format(d);await nextTick();place()}
async function keys(event:KeyboardEvent){if(event.key==='Escape'){event.preventDefault();event.stopPropagation();close();trigger.value?.focus();return}if(!(event.target as HTMLElement).matches('.calendar-day'))return;let d=parse(active.value);const shifts:Record<string,number>={ArrowLeft:-1,ArrowRight:1,ArrowUp:-7,ArrowDown:7};if(event.key in shifts)d.setDate(d.getDate()+shifts[event.key]);else if(event.key==='Home')d.setDate(d.getDate()-(d.getDay()+6)%7);else if(event.key==='End')d.setDate(d.getDate()+6-(d.getDay()+6)%7);else if(event.key==='PageUp'||event.key==='PageDown'){d=new Date(year.value,month.value+(event.key==='PageUp'?-1:1),1,12)}else return;event.preventDefault();active.value=format(d);year.value=d.getFullYear();month.value=d.getMonth();await nextTick();focusDay()}
function outside(event:PointerEvent){const target=event.target as HTMLElement;if(!root.value?.contains(target)&&!panel.value?.contains(target)&&!target.closest('.select-menu'))close()}
function reposition(){if(open.value)place()}
onMounted(()=>{document.addEventListener('pointerdown',outside);window.addEventListener('resize',reposition);window.addEventListener('scroll',reposition,true)})
onUnmounted(()=>{document.removeEventListener('pointerdown',outside);window.removeEventListener('resize',reposition);window.removeEventListener('scroll',reposition,true)})
</script>
<template><span ref="root" class="date-field"><button ref="trigger" type="button" class="date-trigger" :class="{placeholder:!modelValue}" :aria-label="label" aria-haspopup="dialog" :aria-expanded="open" @click="show" @keydown.down.prevent="show"><CalendarDays :size="15"/><span>{{modelValue||label}}</span></button><Teleport to="body"><section v-if="open" ref="panel" class="calendar-panel" role="dialog" :aria-label="`${label}日历`" :style="position" @keydown="keys"><header><button type="button" class="icon-btn" aria-label="上个月" @click="shift(-1)"><ChevronLeft :size="16"/></button><SelectField v-model="year" :label="`${label}年份`" :options="years" @change="changeMonth"/><SelectField v-model="month" :label="`${label}月份`" :options="months" @change="changeMonth"/><button type="button" class="icon-btn" aria-label="下个月" @click="shift(1)"><ChevronRight :size="16"/></button></header><div class="calendar-week"><span v-for="day in ['一','二','三','四','五','六','日']" :key="day">{{day}}</span></div><div class="calendar-grid"><button v-for="day in days" :id="`${id}-${day.value}`" :key="day.value" type="button" class="calendar-day" :class="{outside:day.outside,today:day.value===today,selected:day.value===modelValue}" :aria-label="day.value" :aria-pressed="day.value===modelValue" :aria-disabled="disabled(day.value)" :tabindex="day.value===active?0:-1" @focus="active=day.value" @click="choose(day.value)">{{day.day}}</button></div><footer><button type="button" @click="choose('')">清除</button><button type="button" :disabled="disabled(today)" @click="choose(today)">今天</button><button type="button" @click="close();trigger?.focus()">关闭</button></footer></section></Teleport></span></template>
