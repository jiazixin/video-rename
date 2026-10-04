import {test,expect,type Page} from '@playwright/test'
import {mockRunPages} from './run-pages'

const sampleRun = () => ({
 id:146,task:'rename',source:'manual',plan:0,status:'running',targets:[1],target_names:['color1 / ts'],recursive:true,
 done:1887,skipped:108,failed:1,discovered:1996,created:'2026-10-03T10:48:45Z',started:'2026-10-03T10:48:46Z',finished:'',
 model_config_id:1,model_config_title:'zcb',prompt_body:'准确描述画面，只输出文件标题。\n'+ '长提示词内容'.repeat(100),
 request_config:{model:'vision-main',retry_enabled:true,retry_count:3,fallback_model_title:'5060ti'},error:'',token_count:0
})
const sampleItems = () => [
 {id:1,entry:1,status:'running',original_path:`color1/ts/${'长文件名'.repeat(40)}.mp4`,current_path:`/path/to/current/${'长路径'.repeat(80)}/xxxxx.mp4`,message:'',attempts:[]},
 {id:2,entry:2,status:'done',original_path:'color1/ts/原文件.mp4',current_path:'color1/ts/新文件名.mp4',message:'',attempts:[{number:1,model_config_title:'主模型配置',model:'vision-main',fallback:false,started:'2026-10-03T10:51:32Z',duration_ms:12300,status:'success',message:''}]},
 {id:3,entry:3,status:'failed',original_path:'color1/ts/失败文件.mp4',current_path:'color1/ts/失败文件.mp4',message:'模型请求超时',attempts:Array.from({length:4},(_,i)=>({number:i+1,model_config_title:i?'兜底配置':'主模型配置',model:i?'5060ti':'vision-main',fallback:i>0,started:'2026-10-03T12:31:22Z',duration_ms:12000,status:'failed',message:'模型请求超时'}))}
]
async function expectEvenTimeline(page:Page) {
 const nodes=await page.locator('.detail-lifecycle li').evaluateAll(items=>items.map(item=>{
  const cell=item.getBoundingClientRect(),node=item.querySelector('.lifecycle-node')!.getBoundingClientRect()
  return {center:node.x+node.width/2,cellCenter:cell.x+cell.width/2,width:cell.width}
 }))
 expect(nodes).toHaveLength(4)
 for(const node of nodes) expect(Math.abs(node.center-node.cellCenter)).toBeLessThan(1)
 for(let i=1;i<nodes.length;i++) expect(nodes[i].center-nodes[i-1].center).toBeCloseTo(nodes[0].width,0)
}
async function login(page:Page) {
 await page.goto('/')
 await page.getByLabel('用户名',{exact:true}).fill('admin')
 await page.getByLabel('密码',{exact:true}).fill('test-password-123')
 await page.getByRole('button',{name:'登录',exact:true}).click()
 await expect(page.getByRole('heading',{name:'文件目录.'})).toBeVisible()
}
async function openSample(page:Page,run=sampleRun()) {
 await page.clock.setFixedTime(new Date('2026-10-03T13:02:10Z'))
 await mockRunPages(page,[run])
 await page.route('**/api/runs/146',route=>route.fulfill({json:run}))
 await page.route('**/api/runs/146/items?*',route=>{
  const view=new URL(route.request().url()).searchParams.get('view')
  const items=view==='failures'?sampleItems().filter(item=>item.status==='failed'):sampleItems()
  return route.fulfill({json:{page:1,total:items.length,items}})
 })
 await login(page)
 await page.getByRole('button',{name:'批次记录',exact:true}).click()
 await page.getByRole('button',{name:'详情',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'运行 #146'})
 await expect(dialog.locator('.run-item')).toHaveCount(3)
 return dialog
}

test('时间线详情示例、模型展开、复制、移动布局和实时刷新',async({page,context})=>{
 const run=sampleRun()
 const dialog=await openSample(page,run)
 await expect(dialog.locator('.detail-header-meta')).toContainText('2h13m24s')
 await expect(dialog.locator('.detail-statistic strong')).toHaveText(['1,887','108','1','1,996'])
 await expect(dialog.locator('.detail-statistic small').first()).toHaveText('94.5%')
 await expect(dialog.getByRole('progressbar')).toHaveAttribute('aria-valuenow','100')
 for(const [label,expanded] of [['目标目录与文件','true'],['运行日志','false'],['失败记录','false'],['配置信息','false']]) {
  await expect(dialog.getByRole('button',{name:new RegExp(`^${label}`)})).toHaveAttribute('aria-expanded',expanded)
 }
 await expect(dialog.locator('.detail-lifecycle [aria-current="step"]')).toContainText('处理中')
 await expect(dialog.locator('.modal-actions button')).toHaveText(['暂停','取消'])
 await expect(dialog.getByRole('button',{name:'关闭',exact:true})).toHaveCount(1)
 await expect(dialog.locator('.detail-current-path .detail-path-line > span')).toHaveAttribute('title',sampleItems()[0].current_path)
 await context.grantPermissions(['clipboard-read','clipboard-write'])
 await dialog.getByRole('button',{name:'复制当前路径',exact:true}).click()
 await expect(page.locator('.toast-message').first()).toContainText('路径已复制')
 expect(await page.evaluate(()=>navigator.clipboard.readText())).toBe(sampleItems()[0].current_path)
 await page.setViewportSize({width:1440,height:1200})
 await expectEvenTimeline(page)
 await page.screenshot({path:'test-results/run-detail-timeline-desktop.png',animations:'disabled'})
 await dialog.locator('.detail-lifecycle').screenshot({path:'test-results/run-detail-lifecycle-running.png',animations:'disabled'})
 await page.setViewportSize({width:390,height:844})
 await expectEvenTimeline(page)
 await page.screenshot({path:'test-results/run-detail-timeline-mobile.png',animations:'disabled'})
 await page.setViewportSize({width:1440,height:1200})
 await dialog.getByRole('button',{name:/^运行日志/}).click()
 await dialog.locator('.attempt-history summary').first().click()
 await expect(dialog.locator('.detail-attempt').first()).toContainText('12300 ms')
 await expect(dialog.locator('.detail-attempt').first()).toContainText('vision-main')
 await dialog.getByRole('button',{name:/^失败记录/}).click()
 await expect(dialog.locator('.detail-failure')).toContainText('3 / 3')
 await expect(dialog.locator('.detail-failure')).toContainText('5060ti')
 // A polling update must keep user-controlled expansion and request details open.
 run.status='paused'
 await expect(dialog.locator('.detail-header-status')).toHaveText('已暂停',{timeout:10000})
 await expect(dialog.locator('.modal-actions button')).toHaveText(['继续','取消'])
 await expect(dialog.getByRole('button',{name:/^运行日志/})).toHaveAttribute('aria-expanded','true')
 await expect(dialog.locator('.attempt-history').first()).toHaveAttribute('open','')
 await expect(dialog.locator('.detail-lifecycle [aria-current="step"]')).toHaveAttribute('data-tone','warning')
 await dialog.getByRole('button',{name:/^配置信息/}).click()
 await expect(dialog.locator('.detail-config')).toContainText('vision-main')
 await dialog.getByRole('button',{name:/^提示词快照/}).click()
 await expect(dialog.locator('.detail-prompt-body pre')).toContainText('长提示词内容')
 await page.setViewportSize({width:390,height:844})
 await expectEvenTimeline(page)
 const overflow=await dialog.evaluate(el=>Array.from(el.querySelectorAll<HTMLElement>('.modal-content,.detail-prompt-body,.detail-log-list,.detail-file-list')).some(node=>node.scrollWidth>node.clientWidth))
 expect(overflow).toBe(false)
 const header=await dialog.locator(':scope > header').boundingBox()
 await dialog.locator('.modal-content').evaluate(el=>{el.scrollTop=el.scrollHeight})
 expect((await dialog.locator(':scope > header').boundingBox())!.y).toBe(header!.y)
 await dialog.locator('.modal-content').evaluate(el=>{el.scrollTop=0})
 await page.screenshot({path:'test-results/run-detail-timeline-mobile-expanded.png',animations:'disabled'})
 await dialog.getByRole('button',{name:'关闭',exact:true}).first().focus()
 await page.keyboard.press('Shift+Tab')
 await expect(dialog.locator('.modal-actions button').last()).toBeFocused()
 await page.keyboard.press('Escape')
 await expect(dialog).toHaveCount(0)
})

test('失败、取消、完成与未开始批次的生命周期',async({page})=>{
 const run=sampleRun()
 const dialog=await openSample(page,run)
 for(const status of ['completed','failed','partial_failure','interrupted','cancelled','queued','scanning']) {
  run.status=status
  run.started=['queued','scanning','cancelled'].includes(status)?'':'2026-10-03T10:48:46Z'
  run.finished=['queued','scanning'].includes(status)?'':'2026-10-03T13:02:10Z'
  await expect(dialog.locator('.detail-header-status')).toHaveAttribute('data-status',status,{timeout:10000})
  const terminal=dialog.locator('.detail-lifecycle li').last()
  await expect(terminal).toHaveAttribute('data-tone',status==='completed'?'success':status==='cancelled'?'muted':['queued','scanning'].includes(status)?'pending':'error')
  if(['completed','failed','cancelled'].includes(status)) await dialog.locator('.detail-lifecycle').screenshot({path:`test-results/run-detail-lifecycle-${status}.png`,animations:'disabled'})
  if(status==='scanning') await expect(dialog.locator('.detail-lifecycle [aria-current="step"]')).toHaveAttribute('data-tone','primary')
  if(['failed','partial_failure','interrupted','cancelled'].includes(status)) {
   await expect(terminal).not.toHaveText(/已完成/)
  }
 }
})


test('独立抽帧详情不展示命名配置，保留抽帧文件与批次控制',async({page})=>{
 const run={...sampleRun(),task:'extract',status:'paused',prompt_body:'',request_config:undefined,model_config_id:0,model_config_title:''}
 const dialog=await openSample(page,run as ReturnType<typeof sampleRun>)
 await expect(dialog.locator('.detail-metadata')).toHaveText('独立抽帧 · 不使用模型')
 await expect(dialog.locator('.detail-prompt-tag')).toHaveCount(0)
 await expect(dialog.locator('.run-item')).toHaveCount(3)
 await dialog.getByRole('button',{name:/^配置信息/}).click()
 await expect(dialog.locator('.detail-config')).toContainText('独立抽帧')
 await expect(dialog.locator('.detail-config')).not.toContainText('主模型')
 await expect(dialog.getByRole('button',{name:'继续',exact:true})).toBeVisible()
 await expect(dialog.getByRole('button',{name:'取消',exact:true})).toBeVisible()
})
