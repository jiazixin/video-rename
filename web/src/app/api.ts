import { ref } from 'vue'
export const session = ref<{username:string;csrf:string}|null>(null)
// getRandomValues also works over HTTP on a LAN, where randomUUID is unavailable.
export function key(): string {
 const bytes = crypto.getRandomValues(new Uint8Array(16))
 bytes[6] = (bytes[6]! & 0x0f) | 0x40
 bytes[8] = (bytes[8]! & 0x3f) | 0x80
 const hex = Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')
 return `${hex.slice(0,8)}-${hex.slice(8,12)}-${hex.slice(12,16)}-${hex.slice(16,20)}-${hex.slice(20)}`
}
export async function api<T=any>(path:string,method='GET',body?:unknown,signal?:AbortSignal):Promise<T>{
 const response=await fetch(`/api${path}`,{method,signal,headers:{'Content-Type':'application/json',...(session.value?{'X-CSRF-Token':session.value.csrf}:{})},credentials:'same-origin',...(body===undefined?{}:{body:JSON.stringify(body)})})
 const data=await response.json().catch((error)=>{if(signal?.aborted)throw error;return {message:'服务响应异常'}})
 if(!response.ok){if(response.status===401)session.value=null;throw new Error(data.message||'请求失败')}
 return data
}
export async function bootstrap(){try{session.value=await api('/auth/session')}catch{session.value=null}}
export interface Entry {progress?:{total:number;processed:number;unknown:number;active:number};contents?:{directories:number;videos:number;images:number}|null;id:number;parent:number;name:string;kind:string;size:number;mtime:string;renamed_at?:string;version:number;processed:string;restore_id:number;task_status:string}
export interface Page {total:number;items:Entry[];next:number;generation:string;refreshing:boolean;refreshed:string;error:string;directory:Entry;breadcrumbs:Entry[]}
export interface Run {target_names?:string[];started?:string;request_config?:{provider?:string;protocol?:string;base_url?:string;omit_temperature?:boolean;token_parameter?:string;system_location?:string;model:string;temperature?:number;max_tokens?:number;stream?:boolean;message_roles?:string[]};model_config_id:number;model_config_title:string;prompt_body:string;id:number;source:string;targets:number[];recursive:boolean;status:string;created:string;finished:string;discovered:number;done:number;skipped:number;failed:number;error:string;plan:number}
export interface Plan {model_config_id:number;target_names?:string[];id:number;name:string;targets:number[];recursive:boolean;kind:string;expression:string;timezone:string;enabled:boolean;next:string;last:string;version:number}
export const time=(v:string)=>v?new Date(v).toLocaleString('zh-CN',{hour12:false}):'—'
export const size=(v:number)=>v>=1073741824?`${(v/1073741824).toFixed(1)} GB`:v>=1048576?`${(v/1048576).toFixed(1)} MB`:v>=1024?`${(v/1024).toFixed(1)} KB`:`${v} B`
export const statusNames:Record<string,string>={queued:'排队中',scanning:'扫描与稳定性检查',running:'运行中',cancelling:'正在取消',completed:'已完成',partial_failure:'部分失败',failed:'失败',cancelled:'已取消',interrupted:'已中断',done:'已完成',skipped:'已跳过'}
export const actionNames:Record<string,string>={manual:'手动命名',automatic:'自动命名',restore:'还原',recovery:'恢复核对',scan:'目录扫描',run:'运行',schedule:'计划触发',scheduler:'调度器'}

export interface ModelProfile {workers:number;thinking:string;thinking_adapter:string;thinking_effort:string;thinking_budget:number;provider:string;protocol:string;base_url:string;model:string;timeout:string;temperature:number;max_tokens:number;omit_temperature:boolean;has_api_key:boolean;ready:boolean;is_default:boolean;id:number;title:string;body:string;version:number;created:string;updated:string}
