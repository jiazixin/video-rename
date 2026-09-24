<script setup lang="ts">
import { ref,onActivated,onDeactivated,onUnmounted,computed,nextTick } from 'vue'
import {Folder,FileVideo,FileAudio,File,ArrowDownUp,ChevronRight,ChevronLeft,RefreshCw,Search,ArrowUpRight,PenLine,Undo2,Sparkles,Home,History,ArrowRight,Link,Image as ImageIcon,CheckCheck,RotateCcw} from 'lucide-vue-next'
import {api,key,time,size,type Entry,type Page,statusNames,type Run} from '../../app/api'
import {ModelPicker} from '../models'
import StatusBadge from '../../components/StatusBadge.vue'
import DirectoryStatus from './DirectoryStatus.vue'
import Modal from '../../components/Modal.vue'
import SelectField from '../../components/SelectField.vue'
import VideoPlayer from './VideoPlayer.vue'
import AudioPlayer from './AudioPlayer.vue'
import MediaNumbering from './MediaNumbering.vue'
defineProps<{ready:boolean}>();const emit=defineEmits<{run:[]}>()
const multi=ref(false),chosen=ref(new Map<number,Entry>()),batch=ref(''),batchError=ref(''),batchProgress=ref(''),playing=ref<Entry>()
const preview=ref<Entry>(),imageError=ref(false),imageBusy=ref(false),galleryError=ref('')
const video=(e:Entry)=>/\.(mp4|m4v|mkv|mov|avi|webm|flv|wmv|mpg|mpeg|ts|m2ts|mts|3gp|ogv|rmvb|asf|vob)$/i.test(e.name)
const isImage=(e:Entry)=>e.kind==='file'&&/\.(jpe?g|png|gif|webp|avif|bmp)$/i.test(e.name)
const isAudio=(e:Entry)=>e.kind==='file'&&/\.(mp3|m4a|m4b|aac|flac|wav|wave|ogg|oga|opus|wma|aiff|aif|caf)$/i.test(e.name)
function showImage(e:Entry){preview.value=e;imageError.value=false;galleryError.value=''}
async function switchImage(previous:boolean){if(!preview.value||imageBusy.value)return;const id=preview.value.id;imageBusy.value=true;galleryError.value='';try{const direction=(sort.value==='name_desc'?!previous:previous)?'previous':'next';const next=await api<Entry>(`/entries/${id}/image-neighbor?direction=${direction}`);if(preview.value?.id===id)showImage(next)}catch(e){galleryError.value=(e as Error).message}finally{imageBusy.value=false}}
function imageKeys(e:KeyboardEvent){if(!preview.value||e.altKey||e.ctrlKey||e.metaKey)return;if(['ArrowLeft','ArrowRight','ArrowUp','ArrowDown'].includes(e.key)){e.preventDefault();switchImage(e.key==='ArrowLeft'||e.key==='ArrowUp')}}
const selectable=(e:Entry)=>e.kind==='file'||e.kind==='directory'
const autoEntries=computed(()=>[...chosen.value.values()].filter(e=>(e.kind==='directory'||video(e))&&!e.processed&&!e.task_status))
const markEntries=computed(()=>[...chosen.value.values()].filter(e=>e.kind==='directory'||e.kind==='file'&&video(e)))
const restoreEntries=computed(()=>[...chosen.value.values()].filter(e=>e.restore_id&&!e.task_status))
function toggle(e:Entry){chosen.value.has(e.id)?chosen.value.delete(e.id):chosen.value.set(e.id,e)}
function selectPage(invert=false){for(const e of page.value?.items||[])if(selectable(e)){if(invert)toggle(e);else chosen.value.set(e.id,e)}}
function startBatch(action:string){batch.value=action;batchError.value='';batchProgress.value='';modelConfigID.value=0}
async function submitBatch(){statusSeq++;saving.value=true;batchError.value='';try{
 if(batch.value==='auto'){const entries=[...autoEntries.value];const runs:number[]=[];for(let i=0;i<entries.length;i+=100){const group=entries.slice(i,i+100);const run=await api('/runs','POST',{targets:group.map(e=>e.id),recursive:recursive.value,model_config_id:modelConfigID.value,key:key()});runs.push(run.id);trackRun(run.id,group);notice.value=`运行 ${runs.map(id=>'#'+id).join('、')} 已加入队列`;batchProgress.value=`已提交 ${Math.min(i+100,entries.length)} / ${entries.length}`}batch.value=''}
 else {const entries=[...restoreEntries.value];let done=0;const failures:string[]=[];for(const e of entries){batchProgress.value=`正在还原 ${++done} / ${entries.length}`;try{await api(`/operations/${e.restore_id}/restore`,'POST',{key:key()});chosen.value.delete(e.id)}catch(err){failures.push(`${e.name}：${(err as Error).message}`)}}notice.value=`已还原 ${entries.length-failures.length} 项，失败 ${failures.length} 项`;batchProgress.value='';if(failures.length)batchError.value=failures.join('\n');else batch.value=''}
 await syncEntries()
 }catch(err){batchError.value=(err as Error).message}finally{saving.value=false}}
const videoBusy=ref(false),videoNotice=ref('')
const audioPlaying=ref<Entry>(),audioNotice=ref(''),audioBusy=ref(false)
// Cross-page switching shared by the video and audio players: replay the
// directory's filter, search and sort to find the current entry's neighbour.
type MediaList={kind:'video'|'audio';unit:string;match:(e:Entry)=>boolean;current:{value:Entry|undefined};notice:{value:string};busy:{value:boolean};token:{value:number}}
const videoList:MediaList={kind:'video',unit:'集',match:video,current:playing,notice:videoNotice,busy:videoBusy,token:{value:0}}
const audioList:MediaList={kind:'audio',unit:'首',match:isAudio,current:audioPlaying,notice:audioNotice,busy:audioBusy,token:{value:0}}
function closeMediaList(list:MediaList){list.token.value++;list.busy.value=false;list.current.value=undefined;list.notice.value=''}
function closePlayback(){closeMediaList(videoList);closeMediaList(audioList)}
async function switchMedia(previous:boolean,list:MediaList){
 if(!list.current.value||list.busy.value)return
 const id=list.current.value.id,request=++list.token.value
 list.busy.value=true;list.notice.value=''
 try{
  let offset=0,generation='0',last:Entry|undefined,found=false
  // Read the same ordered, filtered directory across page boundaries.
  while(true){
   const params=new URLSearchParams({offset:String(offset),limit:'200',q:q.value,kind:list.kind,sort:sort.value,generation})
   const result=await api<Page>(`/directories/${dir.value}/entries?${params}`)
   if(request!==list.token.value||list.current.value?.id!==id)return
   for(const entry of result.items){
    if(entry.kind!=='file'||!list.match(entry))continue
    if(found&&!previous){list.current.value=entry;return}
    if(entry.id===id){
     found=true
     if(previous){if(last)list.current.value=last;else list.notice.value=`已经是第一${list.unit}`;return}
    }
    last=entry
   }
   if(!result.next){list.notice.value=found?`已经是最后一${list.unit}`:'当前文件已不在列表中，请关闭后重新选择';return}
   offset=result.next;generation=result.generation
  }
 }catch(e){if(request===list.token.value)list.notice.value=(e as Error).message}
 finally{if(request===list.token.value)list.busy.value=false}
}
function switchVideo(previous:boolean){return switchMedia(previous,videoList)}
function switchAudio(previous:boolean){return switchMedia(previous,audioList)}
function play(e:Entry){videoNotice.value='';playing.value=e}
function playAudio(e:Entry){audioPlaying.value=e}
const modelConfigID=ref(0)
const sort=ref('name_asc')
function toggleSort(field:string){sort.value=sort.value===`${field}_asc`?`${field}_desc`:`${field}_asc`;offset.value=0;cursors.value=[];load()}
function sortLabel(field:string){return sort.value===`${field}_asc`?'升序':sort.value===`${field}_desc`?'降序':''}
const cursors=ref<number[]>([])
function routeDirectory(){const value=Number(location.hash.match(/^#library\/(\d+)$/)?.[1]||1);return Number.isSafeInteger(value)&&value>0?value:1}
const dir=ref(routeDirectory()),page=ref<Page>(),busy=ref(false),error=ref(''),notice=ref(''),q=ref(''),kind=ref(''),offset=ref(0),limit=ref(100)
const selected=ref<Entry>(),mode=ref(''),newName=ref(''),saving=ref(false),dialogError=ref(''),recursive=ref(true),history=ref<any[]>([])
const inlineEntry=ref<Entry>(),inlineInput=ref<HTMLInputElement>(),inlineName=ref(''),inlineExtension=ref(''),inlineVersion=ref(0),inlineSaving=ref(false),inlineError=ref('')
function setInlineInput(el:unknown){inlineInput.value=el instanceof HTMLInputElement?el:undefined}
function cancelInlineRename(){if(inlineSaving.value)return;inlineEntry.value=undefined;inlineError.value=''}
async function beginInlineRename(e:Entry){
 if(e.kind!=='file'||inlineSaving.value)return
 inlineEntry.value=e;inlineVersion.value=e.version;inlineError.value=''
 inlineExtension.value=e.name.match(/\.[^.]+$/)?.[0]?.toLowerCase()||''
 inlineName.value=e.name.slice(0,e.name.length-(e.name.match(/\.[^.]+$/)?.[0].length||0))
 await nextTick();inlineInput.value?.focus();inlineInput.value?.select()
}
async function saveInlineRename(){
 const e=inlineEntry.value
 if(!e||inlineSaving.value)return
 if(!inlineName.value.trim()){inlineError.value='请输入文件名';return}
 const name=inlineName.value+inlineExtension.value
 if(name===e.name){cancelInlineRename();return}
 inlineSaving.value=true;inlineError.value='';statusSeq++
 try{
  await api(`/entries/${e.id}/rename`,'POST',{name,version:inlineVersion.value,key:key()})
  notice.value='名称已更新，历史已保存'
  await syncEntries()
  inlineEntry.value=undefined
 }catch(err){inlineError.value=(err as Error).message;await nextTick();inlineInput.value?.focus()}
 finally{inlineSaving.value=false}
}
const trackedRuns=new Set<number>()
let disposed=false,statusSeq=0
let seq=0,timer:ReturnType<typeof setTimeout>|undefined,searchTimer:ReturnType<typeof setTimeout>|undefined
const extension=computed(()=>selected.value?.kind==='file'?selected.value.name.match(/\.[^.]+$/)?.[0]?.toLowerCase()||'':'')
function scheduleSync(){clearTimeout(timer);if(!disposed&&active)timer=setTimeout(()=>{if(saving.value)scheduleSync();else void syncEntries()},1500)}
function trackRun(id:number,entries:Entry[]){trackedRuns.add(id);for(const e of entries)if(e.kind==='file'&&!e.processed)e.task_status='queued';scheduleSync()}
async function syncEntries(){
 const n=seq,request=++statusSeq;clearTimeout(timer)
 if(disposed||!active||busy.value||!page.value)return
 try{
  const runs=await Promise.all([...trackedRuns].map(id=>api<Run>(`/runs/${id}`)))
  if(n!==seq||request!==statusSeq||disposed)return
  for(const run of runs)if(!['queued','scanning','running','cancelling'].includes(run.status))trackedRuns.delete(run.id)
  const ids=[...new Set([page.value.directory.id,...page.value.items.map(e=>e.id),...chosen.value.keys()])]
  const entries:Entry[]=[]
  for(let i=0;i<ids.length;i+=300)entries.push(...await api<Entry[]>(`/entries/status?ids=${ids.slice(i,i+300).join(',')}`))
  if(n!==seq||request!==statusSeq||disposed)return
  // A queued/scanning run may not have populated run_items yet.
  for(const run of runs)if(['queued','scanning','running','cancelling'].includes(run.status))for(const e of entries)if((run.targets||[]).includes(e.id)&&!e.processed&&!e.task_status)e.task_status=run.status
  const latest=new Map(entries.map(e=>[e.id,e]))
  const directory=latest.get(page.value.directory.id);if(directory)Object.assign(page.value.directory,directory)
  // A task/recursive mark may rebuild the directory index. Keep this page's
  // rows in place, but resolve the next user-initiated page against the new index.
  page.value.generation='0'
  page.value.items=page.value.items.filter(e=>latest.has(e.id))
  for(const e of page.value.items)Object.assign(e,latest.get(e.id))
  for(const id of chosen.value.keys()){const e=latest.get(id);if(e)chosen.value.set(id,e);else chosen.value.delete(id)}
  error.value=''
  if(trackedRuns.size||entries.some(e=>e.task_status||e.progress?.active))scheduleSync()
 }catch(e){if(n===seq&&request===statusSeq&&!disposed){error.value=`状态更新失败：${(e as Error).message}`;scheduleSync()}}
}
async function load(quiet=false){const n=++seq;clearTimeout(timer);busy.value=!quiet;error.value='';try{const params=new URLSearchParams({offset:String(offset.value),limit:String(limit.value),q:q.value,kind:kind.value,sort:sort.value,generation:offset.value>0?page.value?.generation||'0':'0'});const p=await api<Page>(`/directories/${dir.value}/entries?${params}`);if(n!==seq)return;page.value=p;if(inlineEntry.value&&!p.items.some(e=>e.id===inlineEntry.value?.id))cancelInlineRename();await restorePosition(n,p);if(n!==seq)return;for(const e of p.items)if(chosen.value.has(e.id))chosen.value.set(e.id,e);if(p.refreshing)timer=setTimeout(()=>load(true),1500);else if(trackedRuns.size||p.directory.task_status||p.directory.progress?.active||p.items.some(e=>e.task_status||e.progress?.active))scheduleSync()}catch(e){if(n===seq)error.value=(e as Error).message}finally{if(n===seq)busy.value=false}}
type DirectoryPosition={offset:number;cursors:number[];q:string;kind:string;sort:string;limit:number;scrollY:number;anchor?:number;snapshot?:Page}
const positions=new Map<number,DirectoryPosition>()
const listRoot=ref<HTMLElement>(),returnAnchor=ref<number>()
let pendingPosition:DirectoryPosition|undefined
const nativeScrollRestoration=window.history.scrollRestoration
let active=false,parkedScrollY=0
async function restorePosition(request:number,result:Page){
 const saved=pendingPosition
 if(!saved||(result.refreshing&&!result.items.some(e=>e.id===saved.anchor)))return
 await nextTick()
 if(disposed||request!==seq||saved!==pendingPosition)return
 pendingPosition=undefined
 const row=saved.anchor?listRoot.value?.querySelector<HTMLElement>(`[data-entry-id="${saved.anchor}"]`):undefined
 window.scrollTo({top:row?window.scrollY+row.getBoundingClientRect().top+row.offsetHeight/2-window.innerHeight/2:saved.scrollY,behavior:'instant'})
 if(row){
  returnAnchor.value=saved.anchor
  // 行内异步内容(如目录状态)在滚动后才渲染会改变行高,半秒内多次复核并校正,保持锚定行精确居中。
  for(const delay of [0,60,180,400]){
   setTimeout(()=>{
    if(disposed||request!==seq||pendingPosition||!returnAnchor.value)return
    const r=listRoot.value?.querySelector<HTMLElement>(`[data-entry-id="${saved.anchor}"]`)
    if(!r)return
    const rect=r.getBoundingClientRect(),offset=rect.top+rect.height/2-window.innerHeight/2
    if(Math.abs(offset)>1)window.scrollTo({top:window.scrollY+offset,behavior:'instant'})
   },delay)
  }
 }
}
function cancelPendingPosition(){pendingPosition=undefined}
function scrollKey(event:KeyboardEvent){
 if(['ArrowDown','ArrowUp','PageDown','PageUp','Home','End',' '].includes(event.key))cancelPendingPosition()
}
function clearReturnOnBlank(event:MouseEvent){
 const target=event.target
 if(!(target instanceof Element)||target.closest('[data-entry-id],button,a,input,select,textarea,[role="dialog"],[role="listbox"]'))return
 returnAnchor.value=undefined
 pendingPosition=undefined
}
function enter(id:number,updateRoute=true){
 if(id===dir.value)return
 cancelInlineRename()
 clearTimeout(searchTimer);returnAnchor.value=undefined
 chosen.value.clear()
 const child=page.value?.items.find(e=>e.id===id&&e.kind==='directory')
 positions.set(dir.value,{offset:offset.value,cursors:[...cursors.value],q:q.value,kind:kind.value,sort:sort.value,limit:limit.value,scrollY:window.scrollY,anchor:child?.id??positions.get(dir.value)?.anchor,snapshot:page.value})
 // Bound retained lists while keeping recent return navigation immediate.
 if(positions.size>30)positions.delete(positions.keys().next().value!)
 const saved=positions.get(id)
 pendingPosition=saved?{...saved}:undefined
 if(!saved)window.scrollTo({top:0,behavior:'instant'})
 dir.value=id
 if(updateRoute&&(routeDirectory()!==id||!location.hash.startsWith('#library/')))location.hash=`library/${id}`
 offset.value=saved?.offset||0;cursors.value=[...(saved?.cursors||[])];q.value=saved?.q||'';kind.value=saved?.kind??'';sort.value=saved?.sort??sort.value;limit.value=saved?.limit??limit.value
 page.value=saved?.snapshot
 void load(!!page.value)
 if(page.value)void restorePosition(seq,page.value)
}
function search(){offset.value=0;cursors.value=[];seq++;clearTimeout(timer);clearTimeout(searchTimer);searchTimer=setTimeout(load,250)}
async function refresh(){offset.value=0;cursors.value=[];try{await api(`/directories/${dir.value}/refresh`,'POST',{});await load()}catch(e){error.value=(e as Error).message}}
async function open(e:Entry,m:string){if(m==='auto')modelConfigID.value=0;selected.value=e;mode.value=m;dialogError.value='';newName.value=e.kind==='file'?e.name.slice(0,e.name.length-(e.name.match(/\.[^.]+$/)?.[0].length||0)):e.name;history.value=[];if(m==='restore'||m==='history'){try{history.value=await api(`/entries/${e.id}/history`)}catch(err){dialogError.value=(err as Error).message}}}
async function submit(){if(!selected.value)return;statusSeq++;saving.value=true;dialogError.value='';try{const e=selected.value;if(mode.value==='rename'){await api(`/entries/${e.id}/rename`,'POST',{name:newName.value+extension.value,version:e.version,key:key()});notice.value='名称已更新，历史已保存'}else if(mode.value==='restore'){await api(`/operations/${e.restore_id}/restore`,'POST',{key:key()});notice.value='名称已还原，可以再次自动命名'}else{const run=await api('/runs','POST',{model_config_id:modelConfigID.value,targets:[e.id],recursive:recursive.value,key:key()});trackRun(run.id,[e]);notice.value=`运行 #${run.id} 已加入队列`}
 mode.value='';await syncEntries()}catch(err){dialogError.value=(err as Error).message}finally{saving.value=false}}
const marking=ref<Entry[]>([]),markValue=ref(true),markError=ref(''),markProgress=ref('')
function openMark(entries:Entry[],value:boolean){marking.value=[...entries];markValue.value=value;markError.value='';markProgress.value=''}
async function submitMark(){
 statusSeq++;saving.value=true;markError.value='';let changed=0,unchanged=0,failed=0;const failures:string[]=[]
 try{
  for(let i=0;i<marking.value.length;i+=100){
   markProgress.value=`正在处理第 ${i+1}–${Math.min(i+100,marking.value.length)} 个目标…`
   const result=await api<{changed:number;unchanged:number;failed:number;failures:{name:string;message:string}[]}>('/entries/processed','POST',{targets:marking.value.slice(i,i+100).map(e=>e.id),marked:markValue.value})
   changed+=result.changed;unchanged+=result.unchanged;failed+=result.failed
   failures.push(...result.failures.map(e=>`${e.name}：${e.message}`))
  }
  notice.value=`${markValue.value?'已标记':'已取消标记'} ${changed} 个视频，未变更 ${unchanged} 个，失败 ${failed} 项`
  if(failed)markError.value=failures.join('\n');else marking.value=[]
 }catch(e){markError.value=`已完成 ${changed} 个视频；${(e as Error).message}。可重试，已完成的标记不会重复创建。`}
 finally{markProgress.value='';saving.value=false;await syncEntries()}
}
function status(e:Entry){if(e.task_status)return statusNames[e.task_status]||e.task_status;return e.processed==='marked'?'手动标记已处理':e.processed==='manual'?'已手动命名':e.processed==='automatic'?'已自动命名':e.processed==='restore'?'已还原':'未处理'}
function routeChanged(){const id=routeDirectory();if((!location.hash||location.hash.startsWith('#library'))&&id!==dir.value)enter(id,false)}
function activate(){
 if(active)return
 active=true
 window.history.scrollRestoration='manual'
 window.addEventListener('wheel',cancelPendingPosition,{passive:true});window.addEventListener('touchmove',cancelPendingPosition,{passive:true});window.addEventListener('pointerdown',cancelPendingPosition);window.addEventListener('keydown',scrollKey);window.addEventListener('keydown',imageKeys);document.addEventListener('click',clearReturnOnBlank);window.addEventListener('hashchange',routeChanged)
 void nextTick(()=>{
  if(!active)return
  window.scrollTo({top:parkedScrollY,behavior:'instant'})
  if(location.hash.startsWith('#library')&&routeDirectory()!==dir.value)enter(routeDirectory(),false)
  else void load(!!page.value)
 })
}
function deactivate(){
 if(!active)return
 parkedScrollY=window.scrollY
 active=false;seq++;statusSeq++
 mode.value='';batch.value='';marking.value=[];preview.value=undefined;cancelInlineRename();closePlayback()
 window.removeEventListener('wheel',cancelPendingPosition);window.removeEventListener('touchmove',cancelPendingPosition);window.removeEventListener('pointerdown',cancelPendingPosition);window.removeEventListener('keydown',scrollKey);window.removeEventListener('keydown',imageKeys);document.removeEventListener('click',clearReturnOnBlank);window.removeEventListener('hashchange',routeChanged)
 window.history.scrollRestoration=nativeScrollRestoration;clearTimeout(timer);clearTimeout(searchTimer)
}
onActivated(activate);onDeactivated(deactivate);onUnmounted(()=>{deactivate();disposed=true})
</script>
<template><section ref="listRoot"><div class="page-heading"><div><h1>文件目录<span class="title-dot">.</span></h1></div><div class="heading-actions"><button :disabled="!page" @click="openMark([page!.directory],true)">标记此目录</button><button :disabled="!page" @click="openMark([page!.directory],false)">取消目录标记</button><button class="primary" :disabled="!ready||!page||!!page.directory.task_status" @click="open(page!.directory,'auto')"><Sparkles :size="16"/>运行此目录</button></div></div><div v-if="notice" class="notice success" role="status">{{notice}}<button @click="notice=''">关闭</button></div><div v-if="error" class="notice error" role="alert">{{error}}<button @click="enter(1)">返回媒体库</button><button @click="offset=0;cursors=[];load()">返回第一页重试</button></div><div class="panel file-panel"><div class="panel-toolbar directory-toolbar"><div class="breadcrumbs"><button @click="enter(1)"><Home :size="15"/>媒体库</button><template v-for="crumb in page?.breadcrumbs.filter(x=>x.id!==1)" :key="crumb.id"><ChevronRight :size="13"/><button @click="enter(crumb.id)">{{crumb.name}}</button></template></div><div class="directory-summary"><DirectoryStatus v-if="page?.directory.progress" :entry="page.directory"/><button :disabled="busy" @click="refresh"><RefreshCw :size="16" :class="{spin:busy}"/>刷新</button></div></div><div class="filterbar"><button :aria-pressed="multi" @click="multi=!multi;chosen.clear()">{{multi?'退出多选':'多选'}}</button><div class="search-input"><Search :size="16"/><input v-model="q" placeholder="搜索当前目录…" aria-label="搜索当前目录" @input="search"/></div><SelectField v-model="kind"  @change="offset=0;cursors=[];load()" label="类型筛选" :options="[{value:'',label:'全部类型'},{value:'directory',label:'目录'},{value:'video',label:'视频'},{value:'image',label:'图片'},{value:'audio',label:'音频'},{value:'other',label:'其他文件'}]"/><MediaNumbering v-if="kind==='image'||kind==='audio'" :media="kind" :directory="dir" :query="q" :sort="sort" :disabled="busy||!page||!!page.refreshing||!!page.directory.task_status" @changed="preview=undefined;chosen.clear();syncEntries()"/><div class="filter-spacer"/><span class="subtle">共 {{page?.total??0}} 项 · {{page?.items.length||0}} 项 / 当前页</span></div><div v-if="multi" class="batch-toolbar"><strong>已选 {{chosen.size}} 项</strong><button @click="selectPage()">全选本页</button><button @click="selectPage(true)">反选本页</button><button :disabled="!chosen.size" @click="chosen.clear()">清空</button><button :disabled="!ready||!autoEntries.length" @click="startBatch('auto')"><Sparkles :size="15"/>批量自动命名（{{autoEntries.length}}）</button><button :disabled="!markEntries.length" @click="openMark(markEntries,true)">标记已处理</button><button :disabled="!markEntries.length" @click="openMark(markEntries,false)">取消已处理标记</button><button :disabled="!restoreEntries.length" @click="startBatch('restore')"><Undo2 :size="15"/>批量还原（{{restoreEntries.length}}）</button></div><div class="table-scroll"><table class="file-table"><thead><tr><th v-if="multi" class="selection-cell">选择</th><th class="filename-cell"><button class="table-sort" aria-label="切换名称排序" @click="toggleSort('name')">名称<ArrowDownUp :size="12"/>{{sortLabel('name')}}</button></th><th><button class="table-sort" aria-label="切换大小排序" @click="toggleSort('size')">大小<ArrowDownUp :size="12"/>{{sortLabel('size')}}</button></th><th class="contents-cell" title="仅统计该目录的直接子项；进入目录完成索引后显示">目录内容</th><th>处理状态</th><th><button class="table-sort" aria-label="切换文件修改时间排序" @click="toggleSort('mtime')">文件修改时间<ArrowDownUp :size="12"/>{{sortLabel('mtime')}}</button></th><th><button class="table-sort" aria-label="切换重命名时间排序" @click="toggleSort('renamed')">重命名时间<ArrowDownUp :size="12"/>{{sortLabel('renamed')}}</button></th><th class="align-right">操作</th></tr></thead><tbody><tr v-for="e in page?.items" :key="e.id" :data-entry-id="e.id" :class="{'is-selected':multi&&chosen.has(e.id),'return-anchor':returnAnchor===e.id}"><td v-if="multi" class="selection-cell"><input v-if="selectable(e)" type="checkbox" :aria-label="`选择 ${e.name}`" :checked="chosen.has(e.id)" @change="toggle(e)"/></td><td class="filename-cell"><div class="file-name"><button v-if="e.kind==='file'&&video(e)" class="file-symbol video-symbol" :aria-label="`播放视频 ${e.name}`" @click="play(e)"><FileVideo :size="21"/></button><button v-else-if="isImage(e)" class="file-symbol image-symbol" :aria-label="`预览图片 ${e.name}`" @click="showImage(e)"><ImageIcon :size="21"/></button><button v-else-if="isAudio(e)" class="file-symbol audio-symbol" :aria-label="`播放音频 ${e.name}`" @click="playAudio(e)"><FileAudio :size="21"/></button><span v-else class="file-symbol" :class="{directory:e.kind==='directory'}"><Folder v-if="e.kind==='directory'" :size="21"/><Link v-else-if="e.kind==='symlink'" :size="21"/><FileVideo v-else-if="video(e)" :size="21"/><File v-else :size="21"/></span><div v-if="inlineEntry?.id===e.id" class="inline-rename"><div class="inline-rename-field"><input :ref="setInlineInput" v-model="inlineName" :aria-label="`编辑 ${e.name}`" :readonly="inlineSaving" maxlength="240" @keydown.enter.prevent="saveInlineRename" @keydown.esc.prevent="cancelInlineRename" @blur="cancelInlineRename"/><span>{{inlineExtension}}</span></div><small v-if="inlineError" role="alert">{{inlineError}}</small></div><button v-else-if="e.kind==='directory'" class="name-button" :title="e.name" @click="enter(e.id)">{{e.name}}<ChevronRight :size="14"/></button><button v-else-if="e.kind==='file'" type="button" class="name-text inline-rename-trigger" :title="`双击重命名 ${e.name}`" :aria-label="`双击或按 Enter 重命名 ${e.name}`" @dblclick="beginInlineRename(e)" @keydown.enter.prevent="beginInlineRename(e)" @keydown.f2.prevent="beginInlineRename(e)">{{e.name}}</button><span v-else class="name-text" :title="e.name">{{e.name}}</span></div></td><td class="subtle mono">{{e.kind==='directory'?'—':size(e.size)}}</td><td class="subtle contents-cell"><template v-if="e.kind==='directory'"><div v-if="e.contents" class="contents-counts"><span>子目录 {{e.contents.directories}}</span><span>视频 {{e.contents.videos}}</span><span>图片 {{e.contents.images}}</span></div><span v-else>待统计</span></template><span v-else>—</span></td><td><DirectoryStatus v-if="e.kind==='directory'" :entry="e"/><StatusBadge v-else :status="e.task_status||e.processed||'unprocessed'">{{status(e)}}</StatusBadge></td><td class="subtle date-cell">{{time(e.mtime)}}</td><td class="subtle date-cell">{{e.renamed_at?time(e.renamed_at):'—'}}</td><td><div class="row-actions" v-if="e.kind==='file'||e.kind==='directory'"><button v-if="isImage(e)" :aria-label="`查看图片 ${e.name}`" title="查看图片" @click="showImage(e)"><ImageIcon :size="15"/></button><button v-if="e.kind==='directory'" title="重命名" :aria-label="`重命名 ${e.name}`" @click="open(e,'rename')"><PenLine :size="15"/></button><button v-if="e.kind==='directory'||video(e)" :disabled="!ready||!!e.processed||!!e.task_status" :aria-label="`自动命名 ${e.name}`" :title="e.processed?'已处理，自动任务将跳过':'自动命名'" @click="open(e,'auto')"><Sparkles :size="15"/></button><button v-if="e.kind==='directory'||video(e)&&!e.processed" :aria-label="`标记已处理 ${e.name}`" title="标记已处理" :disabled="!!e.task_status" @click="openMark([e],true)"><CheckCheck :size="15"/></button><button v-if="e.kind==='directory'||video(e)&&e.processed" :aria-label="`取消已处理标记 ${e.name}`" title="取消已处理标记" :disabled="!!e.task_status" @click="openMark([e],false)"><RotateCcw :size="15"/></button><button v-if="e.restore_id" :aria-label="`还原 ${e.name}`" title="还原" @click="open(e,'restore')"><Undo2 :size="15"/></button><button title="命名历史" :aria-label="`历史 ${e.name}`" @click="open(e,'history')"><History :size="15"/></button></div><span v-else class="subtle">不可操作</span></td></tr></tbody></table><div v-if="!page?.items.length" class="empty"><Folder :size="35"/><h3>{{busy||page?.refreshing?'正在读取目录':'这里还没有内容'}}</h3><p>{{busy||page?.refreshing?'首次进入时会建立当前层的目录索引。':q?'没有符合搜索条件的条目。':'可以返回上级目录，或刷新查看最新文件。'}}</p></div></div><div v-if="page?.error" class="notice error">目录同步失败：{{page.error}}</div><div class="pagination"><span class="subtle">{{page?.refreshed?`更新于 ${time(page.refreshed)}`:'等待首次同步'}}</span><SelectField v-model="limit"  @change="offset=0;cursors=[];load()" label="每页条目数" :options="[{value:50,label:'50 项 / 页'},{value:100,label:'100 项 / 页'},{value:200,label:'200 项 / 页'}]"/><button :disabled="offset===0||busy" aria-label="上一页" @click="offset=cursors.pop()||0;load()"><ChevronLeft :size="16"/></button><span>{{cursors.length+1}}</span><button :disabled="!page?.next||busy" aria-label="下一页" @click="cursors.push(offset);offset=page!.next;load()"><ChevronRight :size="16"/></button></div></div><div class="page-footnote"><button class="text-button" @click="emit('run')">查看运行计划 <ArrowUpRight :size="14"/></button></div></section>
<Modal v-if="mode" :title="mode==='rename'?'重命名':mode==='restore'?'还原名称':mode==='history'?'命名历史':'自动命名'" @close="!saving&&(mode='')"><form @submit.prevent="submit"><div class="selection-summary"><Folder v-if="selected?.kind==='directory'" :size="23"/><FileVideo v-else :size="23"/><strong>{{selected?.name||'整个媒体库'}}</strong></div><template v-if="mode==='rename'"><label>新名称<div class="extension-input"><input v-model="newName" aria-label="新名称" required maxlength="240"/><span>{{extension}}</span></div></label><p class="help">只修改名称，文件保留在原目录。成功后可以还原。</p></template><template v-else-if="mode==='restore'"><p>将恢复到最近一次改名前的名称：</p><div class="restore-target"><Undo2 :size="18"/>{{history.find(x=>x.id===selected?.restore_id)?.old_name||'正在读取…'}}</div><p class="help">还原后可再次自动命名，也会被后续计划重新处理。不会覆盖同名文件。</p></template><template v-else-if="mode==='auto'"><ModelPicker v-model="modelConfigID"/><p>后台将抽取视频画面，由指定模型生成名称并自动修改。</p><label v-if="selected?.kind==='directory'" class="check-label"><input type="checkbox" v-model="recursive"/>包含子目录</label><p class="help">已命名的视频自动跳过。你可以在“运行计划”查看进度。</p></template><template v-else><div v-for="h in history" :key="h.id" class="history-item"><span class="badge">{{h.source==='restore'?'还原':h.source==='manual'?'手动命名':'自动命名'}} · {{h.status}}</span><p>{{h.old_name}} <ArrowRight :size="14"/> {{h.new_name}}</p><small>{{time(h.created)}}</small></div><p v-if="!history.length" class="empty compact">暂无命名历史</p></template><div v-if="dialogError" class="notice error" role="alert">{{dialogError}}</div><div class="modal-actions"><button type="button" :disabled="saving" @click="mode=''">{{mode==='history'?'关闭':'取消'}}</button><button v-if="mode!=='history'" class="primary" :disabled="saving||mode==='restore'&&!history.length||mode==='auto'&&!modelConfigID">{{saving?'正在提交…':mode==='rename'?'保存名称':mode==='restore'?'确认还原':'开始运行'}}</button></div></form></Modal>
<Modal v-if="batch" :title="batch==='auto'?'批量自动命名':'批量还原'" @close="!saving&&(batch='')"><form @submit.prevent="submitBatch"><template v-if="batch==='auto'"><p>处理选中的 {{autoEntries.length}} 个条目。</p><ModelPicker v-model="modelConfigID"/><label class="check-label"><input type="checkbox" v-model="recursive"/>包含子目录</label><p class="help">已命名和正在处理的视频会跳过。</p></template><template v-else><p>还原选中的 {{restoreEntries.length}} 个条目最近一次改名。目录还原只恢复目录名称。</p><p class="help">还原后可以再次自动命名。冲突条目保留原状。</p></template><p v-if="batchProgress" role="status">{{batchProgress}}</p><div v-if="batchError" class="notice error batch-errors" role="alert">{{batchError}}</div><div class="modal-actions"><button type="button" :disabled="saving" @click="batch=''">关闭</button><button class="primary" :disabled="saving||(batch==='auto'?(!modelConfigID||!autoEntries.length):!restoreEntries.length)">{{saving?'正在处理…':batch==='auto'?'开始批量运行':'确认批量还原'}}</button></div></form></Modal>
<Modal v-if="marking.length" :title="markValue?'标记已处理':'取消已处理标记'" @close="!saving&&(marking=[])"><form @submit.prevent="submitMark"><p>已选择 {{marking.length}} 个目标<span v-if="marking.length===1">：{{marking[0].name||'整个媒体库'}}</span>。</p><p v-if="marking.some(e=>e.kind==='directory')">包含所选目录及所有子目录中的现有视频；以后新增的视频不受影响。</p><p>{{markValue?'标记后，自动命名和定时任务会跳过这些视频。已经处理的视频保留原状态。':'取消后，这些视频可以再次自动命名，包括之前实际改名的视频。'}}</p><p class="help">只修改处理状态，不修改文件名、不删除命名历史。活动任务中的视频会报告失败，请等待任务结束后重试。标记保存在数据库中，数据库丢失后需重新标记。</p><p v-if="markProgress" role="status">{{markProgress}}</p><div v-if="markError" class="notice error batch-errors" role="alert">{{markError}}</div><div class="modal-actions"><button type="button" :disabled="saving" @click="marking=[]">关闭</button><button class="primary" :disabled="saving">{{saving?'正在处理…':markValue?'确认标记':'确认取消标记'}}</button></div></form></Modal>
<Modal v-if="playing" :title="playing.name" player @close="closePlayback"><VideoPlayer :key="playing.id" :src="`/api/entries/${playing.id}/video`" :switching="videoBusy" @previous="switchVideo(true)" @next="switchVideo(false)"/><p v-if="videoNotice" role="status">{{videoNotice}}</p><p class="help">← 快退 10 秒 · → 快进 10 秒 · 两侧按钮切换上一集 / 下一集</p></Modal>
<Modal v-if="audioPlaying" :title="audioPlaying.name" @close="audioPlaying=undefined"><AudioPlayer :key="audioPlaying.id" :src="`/api/entries/${audioPlaying.id}/audio`" :switching="audioBusy" @previous="switchAudio(true)" @next="switchAudio(false)"/><p v-if="audioNotice" role="status">{{audioNotice}}</p><p class="help">← 快退 10 秒 · → 快进 10 秒 · 按钮切换上一首 / 下一首 · 使用浏览器原生解码，不支持时会提示失败。</p></Modal>


<Modal v-if="preview" :title="preview.name" wide @close="preview=undefined"><div class="image-gallery"><button class="image-nav previous" aria-label="上一张图片" :disabled="imageBusy" @click="switchImage(true)"><ChevronLeft :size="25"/></button><img v-if="!imageError" :key="preview.id" class="image-preview" :src="`/api/entries/${preview.id}/image`" :alt="preview.name" @error="imageError=true"/><p v-else class="notice error" role="alert">无法显示图片，文件可能不可用或浏览器不支持其格式。</p><button class="image-nav next" aria-label="下一张图片" :disabled="imageBusy" @click="switchImage(false)"><ChevronRight :size="25"/></button></div><p v-if="galleryError" class="notice error" role="alert">{{galleryError}}</p><p class="help gallery-hint">← / ↑ 上一张 · → / ↓ 下一张 · 仅切换当前目录的图片</p></Modal>

</template>

<style scoped>
.file-table tr.return-anchor{background:#e3f2e9;box-shadow:inset 3px 0 #398366}
.file-table .row-actions button{width:36px;min-height:36px;padding:0}
.inline-rename-trigger{border:0;background:transparent;padding:0;text-align:left;color:var(--ink);font:inherit;font-weight:500;cursor:text}
.inline-rename-trigger:focus-visible{outline:2px solid #89bca7;outline-offset:3px;border-radius:2px}
.inline-rename{min-width:0;max-width:320px;flex:1}
.inline-rename-field{display:flex;align-items:center;min-width:0;border:1px solid #89bca7;border-radius:6px;background:white;box-shadow:0 0 0 2px #e3f2e9}
.inline-rename-field input{min-width:0;width:100%;padding:6px 8px;border:0;background:transparent;font:inherit;color:var(--ink)}
.inline-rename-field input:focus{outline:0}
.inline-rename-field span{padding-right:8px;white-space:nowrap;color:var(--ash)}
.inline-rename small{display:block;margin-top:5px;color:#b44444;overflow-wrap:anywhere}
.file-table{min-width:1120px}
.file-table .contents-cell{min-width:120px;white-space:nowrap;font-size:11px}
.contents-counts{display:flex;flex-direction:column;align-items:center;gap:2px;line-height:1.5}
</style>
