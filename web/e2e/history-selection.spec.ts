import {test,expect,type Page} from '@playwright/test'

async function openHistory(page:Page,longContent=false){
 await page.route('**/api/model-configs',route=>route.fulfill({json:[{id:1,title:'原模型',ready:true,is_default:true,body:'原提示词'},{id:2,title:'备用模型',ready:true,is_default:false,body:'新提示词'}]}))
 await page.route('**/api/logs/batches',route=>route.fulfill({json:[7]}))
 await page.route('**/api/logs?*',route=>{
  const params=new URL(route.request().url()).searchParams
  const category=params.get('category'),index=Number(params.get('page'))||1
  const items=Array.from({length:4},(_,i)=>({id:10-i,entry:10-i,category,action:'automatic',run:7,old_path:longContent?`素材库/旅行记录/2026年秋天/片段${10-i}_`+'山间晨雾与日出'.repeat(8)+'.mp4':`目录/片段${10-i}.mp4`,new_path:longContent?'素材库/旅行记录/2026年秋天/'+'秋日清晨山间云海与日出'.repeat(8)+'.mp4':'新名称.mp4',model_config_title:'原模型',model:'vision-test',tokens:123456,token_estimated:true,message:'TITLE_INVALID: 模型返回了多行标题，需要重试生成符合命名规则的标题。',created:'2026-09-30T08:00:00Z'}))
  return route.fulfill({json:{items,total:251,page:index}})
 })
 await page.request.post('/api/auth/login',{data:{username:'admin',password:'test-password-123'}})
 await page.goto('/#history')
 await expect(page.getByRole('heading',{name:'运行记录.'})).toBeVisible()
 await expect(page.getByText('共 251 项 · 4 项 / 当前页')).toBeVisible()
}

test('宽屏成功和失败记录及诊断行占满表格，多选和窄屏保持列对齐',async({page})=>{
 await page.setViewportSize({width:2560,height:1440})
 await page.route('**/api/logs/*/diagnostics',route=>route.fulfill({json:{request:'长请求内容'.repeat(600),response:'模型响应'}}))
 await openHistory(page,true)
 const table=page.locator('.history-table')
 async function expectAligned(){
  const edges=await table.evaluate(element=>{
   const bounds=element.getBoundingClientRect()
   const header=element.querySelector('thead tr')!
   const rows=element.querySelectorAll('tbody tr')
   return {right:bounds.right,header:header.lastElementChild!.getBoundingClientRect().right,
    record:rows[0].lastElementChild!.getBoundingClientRect().right,diagnostics:rows[1].firstElementChild!.getBoundingClientRect().right}
  })
  for(const right of [edges.header,edges.record,edges.diagnostics])expect(Math.abs(right-edges.right)).toBeLessThan(2)
 }
 for(const category of ['成功记录','失败记录']){
  await page.getByRole('button',{name:category,exact:true}).click()
  await expect(page.getByText('共 251 项 · 4 项 / 当前页')).toBeVisible()
  await expectAligned()
  const finalColumn=category==='成功记录'?'新文件名':'失败原因'
  await expect(table.locator('thead th')).toHaveText(['时间','操作','模型配置','批次','token数','目录','旧文件名',finalColumn,''])
  await expect(table.locator('tbody tr').first().locator('td').nth(3)).toHaveText('#7')
  await expect(table.locator('tbody tr').first().locator('td').nth(4)).toHaveText('≈123.5K')
  const compactWidths:number[][]=[]
  for(const width of [1440,2560]){
   await page.setViewportSize({width,height:1440})
   const widths=await table.locator('thead th').evaluateAll(cells=>cells.map(cell=>cell.getBoundingClientRect().width))
   compactWidths.push(widths.slice(3,5))
   expect(widths[3]).toBeLessThan(90)
   expect(widths[4]).toBeLessThan(100)
   expect(widths[6]).toBeGreaterThan(widths[5]!)
   expect(widths[6]).toBeGreaterThan(widths[4]!*1.8)
   expect(widths[7]).toBeGreaterThanOrEqual(widths[6]!-2)
   await expectAligned()
   await table.screenshot({path:`test-results/history-columns-${category}-${width}.png`,animations:'disabled'})
  }
  for(let i=0;i<2;i++)expect(Math.abs(compactWidths[0]![i]!-compactWidths[1]![i]!)).toBeLessThan(2)
  await table.getByRole('button',{name:'原始请求',exact:true}).first().click()
  await expect(table.locator('.diagnostic-content').first()).toContainText('长请求内容')
  await expectAligned()
  await page.getByRole('button',{name:'多选',exact:true}).click()
  await expectAligned()
  await page.screenshot({path:`test-results/history-wide-${category}.png`})
  await page.setViewportSize({width:390,height:844})
  await expectAligned()
  await page.setViewportSize({width:2560,height:1440})
  await page.getByRole('button',{name:'退出多选',exact:true}).click()
  await table.getByRole('button',{name:'原始请求',exact:true}).first().click()
 }
})

test('成功失败记录按需开启多选，支持全选反选清空与 Shift 连选',async({page})=>{
 await openHistory(page)
 await expect(page.locator('.history-heading-actions').getByRole('button',{name:'重试',exact:true})).toHaveCount(0)
 await expect(page.locator('.history-table input[type=checkbox]')).toHaveCount(0)
 await expect(page.locator('.batch-toolbar')).toHaveCount(0)
 await page.getByRole('button',{name:'多选',exact:true}).click()
 const header=page.getByRole('checkbox',{name:'选择本页记录',exact:true})
 await page.getByRole('checkbox',{name:'选择记录 10',exact:true}).click()
 await page.getByRole('checkbox',{name:'选择记录 8',exact:true}).click({modifiers:['Shift']})
 for(const id of [10,9,8])await expect(page.getByRole('checkbox',{name:`选择记录 ${id}`,exact:true})).toBeChecked()
 await expect(header).toHaveJSProperty('indeterminate',true)
 await page.getByRole('button',{name:'反选',exact:true}).click()
 await expect(page.locator('.batch-toolbar')).toContainText('已选 1 项')
 await expect(page.getByRole('checkbox',{name:'选择记录 7',exact:true})).toBeChecked()
 await page.getByRole('button',{name:'全选',exact:true}).click()
 await expect(header).toBeChecked()
 await page.getByRole('button',{name:'清空',exact:true}).click()
 await expect(page.locator('.batch-toolbar')).toContainText('已选 0 项')
 await header.check();await expect(page.locator('.batch-toolbar')).toContainText('已选 4 项')
 await header.uncheck();await expect(page.locator('.batch-toolbar')).toContainText('已选 0 项')
 await page.getByRole('button',{name:'全选',exact:true}).click()
 await page.getByRole('button',{name:'退出多选',exact:true}).click()
 await expect(page.locator('.history-table input[type=checkbox]')).toHaveCount(0)
 await page.getByRole('button',{name:'失败记录',exact:true}).click()
 await expect(page.locator('.history-heading-actions').getByRole('button',{name:'重试',exact:true})).toBeVisible()
 await page.getByRole('button',{name:'多选',exact:true}).click()
 await expect(page.locator('.batch-toolbar')).toContainText('已选 0 项')
 await expect(page.getByRole('button',{name:'批量重试（0）',exact:true})).toBeDisabled()
 await page.getByRole('button',{name:'全选',exact:true}).click()
 await expect(page.getByRole('button',{name:'批量重试（4）',exact:true})).toBeEnabled()
 await page.screenshot({path:'test-results/history-selection.png',fullPage:true})
 await page.setViewportSize({width:390,height:844})
 await expect(page.getByRole('button',{name:'退出多选',exact:true})).toBeInViewport()
 await expect(page.locator('.history-heading-actions').getByRole('button',{name:'重试',exact:true})).toBeInViewport()
 await page.screenshot({path:'test-results/history-selection-mobile.png',fullPage:true})
})

test('右上角重试全部失败不带筛选或选中 ID，提交时防重复点击',async({page})=>{
 await openHistory(page)
 await page.getByRole('button',{name:'失败记录',exact:true}).click()
 await page.getByRole('button',{name:'多选',exact:true}).click()
 await page.getByRole('checkbox',{name:'选择记录 10',exact:true}).check()
 await page.getByRole('button',{name:'下一页运行记录',exact:true}).click()
 await expect(page.getByLabel('分页导航').locator('.page-current')).toHaveText('2')
 await page.getByLabel('搜索运行记录').fill('片段')
 await expect(page.getByLabel('分页导航').locator('.page-current')).toHaveText('1')
 await page.getByRole('checkbox',{name:'选择记录 10',exact:true}).check()
 let calls=0
 let release:()=>void=()=>{}
 const gate=new Promise<void>(resolve=>{release=resolve})
 await page.route('**/api/logs/retry',async route=>{
  calls++
  expect(route.request().postDataJSON()).toMatchObject({all:true,model_config_id:2,key:expect.any(String)})
  expect(route.request().postDataJSON()).not.toHaveProperty('ids')
  expect(route.request().postDataJSON()).not.toHaveProperty('q')
  await gate
  await route.fulfill({json:{accepted:[10,9,8,7],runs:[500],errors:[],skipped:2}})
 })
 await page.route('**/api/runs/500',route=>route.fulfill({json:{id:500,status:'completed'}}))
 await page.locator('.history-heading-actions').getByRole('button',{name:'重试',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'重试失败记录',exact:true})
 await expect(dialog).toContainText('包括其他页，不受筛选条件限制')
 expect(calls).toBe(0)
 await dialog.getByRole('combobox',{name:'模型配置',exact:true}).click()
 await page.getByRole('option',{name:'备用模型',exact:true}).click()
 await dialog.getByRole('button',{name:'确认重试',exact:true}).click()
 await expect(dialog.getByRole('button',{name:'正在提交…',exact:true})).toBeDisabled()
 await dialog.getByRole('button',{name:'关闭',exact:true}).click();await expect(dialog).toBeVisible()
 expect(calls).toBe(1)
 release()
 await expect(page.getByRole('status')).toContainText('已提交 4 条失败记录重试')
 await expect(page.getByRole('status')).toContainText('2 条记录不支持自动重试，已保留')
 await expect(page.locator('.batch-toolbar')).toContainText('已选 0 项')
 await page.getByRole('button',{name:'成功记录',exact:true}).click()
 await expect(page.locator('.history-heading-actions').getByRole('button',{name:'重试',exact:true})).toHaveCount(0)
})

for(const mode of ['单条','详情','多选'])test(`${mode}重试确认后使用所选模型，取消不提交`,async({page})=>{
 await openHistory(page)
 await page.getByRole('button',{name:'失败记录',exact:true}).click()
 const requests:any[]=[]
 await page.route('**/api/logs/retry',async route=>{
  const body=route.request().postDataJSON();requests.push(body)
  await route.fulfill({json:{accepted:body.ids,runs:[],errors:[]}})
 })
 async function openRetry(){
  if(mode==='多选'){
   if(await page.getByRole('button',{name:'多选',exact:true}).count())await page.getByRole('button',{name:'多选',exact:true}).click()
   await page.getByRole('checkbox',{name:'选择记录 10',exact:true}).check()
   await page.getByRole('checkbox',{name:'选择记录 9',exact:true}).check()
   await page.getByRole('button',{name:'批量重试（2）',exact:true}).click()
  }else if(mode==='详情'){
   await page.locator('.history-table tbody tr').first().getByRole('button',{name:'详情',exact:true}).click()
   await page.getByRole('dialog',{name:'运行记录详情',exact:true}).getByRole('button',{name:'重试',exact:true}).click()
  }else await page.getByRole('button',{name:'重试记录 10',exact:true}).click()
 }
 await openRetry()
 let dialog=page.getByRole('dialog',{name:'重试失败记录',exact:true})
 await expect(dialog.getByRole('combobox',{name:'模型配置',exact:true})).toHaveText('原模型')
 await dialog.getByRole('button',{name:'取消',exact:true}).click()
 expect(requests).toHaveLength(0)
 await openRetry()
 dialog=page.getByRole('dialog',{name:'重试失败记录',exact:true})
 await dialog.getByRole('combobox',{name:'模型配置',exact:true}).click()
 await page.getByRole('option',{name:'备用模型',exact:true}).click()
 await expect(dialog).toContainText(mode==='多选'?'选中的 2 条':'片段10.mp4')
 await page.screenshot({path:`test-results/history-retry-${mode}.png`})
 await page.setViewportSize({width:390,height:844})
 await expect(dialog.getByRole('button',{name:'确认重试',exact:true})).toBeInViewport()
 await page.screenshot({path:`test-results/history-retry-${mode}-mobile.png`})
 await dialog.getByRole('button',{name:'确认重试',exact:true}).click()
 await expect(dialog).toHaveCount(0)
 expect(requests).toHaveLength(1)
 expect(requests[0]).toMatchObject({ids:mode==='多选'?[10,9]:[10],model_config_id:2})
 expect(requests[0]).not.toHaveProperty('all')
})

test('重试提交失败保留弹窗、选择和模型，修正后可重试',async({page})=>{
 await openHistory(page)
 await page.getByRole('button',{name:'失败记录',exact:true}).click()
 let calls=0
 await page.route('**/api/logs/retry',async route=>{
  calls++
  await route.fulfill({json:calls===1?{accepted:[],runs:[],errors:['所选模型配置已删除']}:{accepted:[10],runs:[],errors:[]}})
 })
 await page.getByRole('button',{name:'重试记录 10',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'重试失败记录',exact:true})
 await expect(dialog.getByRole('button',{name:'确认重试',exact:true})).toBeEnabled()
 await dialog.getByRole('button',{name:'确认重试',exact:true}).click()
 await expect(dialog.getByRole('alert')).toContainText('所选模型配置已删除')
 await expect(dialog.getByRole('combobox',{name:'模型配置',exact:true})).toHaveText('原模型')
 await dialog.getByRole('combobox',{name:'模型配置',exact:true}).click()
 await page.getByRole('option',{name:'备用模型',exact:true}).click()
 await dialog.getByRole('button',{name:'确认重试',exact:true}).click()
 await expect(dialog).toHaveCount(0)
 expect(calls).toBe(2)
})
