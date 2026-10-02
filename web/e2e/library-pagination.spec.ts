import {test,expect,type Page} from '@playwright/test'
import {mkdirSync,readFileSync,writeFileSync,unlinkSync} from 'node:fs'
import {join} from 'node:path'

async function setup(page:Page,name:string){
 const {mediaPath}=JSON.parse(readFileSync('test-results/fixture.json','utf8'))
 const path=join(mediaPath,name)
 mkdirSync(path,{recursive:true})
 for(let i=0;i<230;i++)writeFileSync(join(path,`clip${String(i).padStart(3,'0')}.mp4`),'pagination fixture')
 const session=await(await page.request.post('/api/auth/login',{data:{username:'admin',password:'test-password-123'}})).json()
 const headers={'X-CSRF-Token':session.csrf}
 await page.request.post('/api/directories/1/refresh',{headers,data:{}})
 await expect.poll(async()=>{
  const data=await(await page.request.get('/api/directories/1/entries?page=1&limit=200')).json()
  return data.items.some((e:any)=>e.name===name)
 }).toBe(true)
 await page.goto('/')
 await page.getByRole('button',{name,exact:true}).click()
 await expect(page.locator('tbody tr')).toHaveCount(100)
 const id=Number(new URL(page.url()).hash.split('/')[1])
 async function rescan(){
  const before=await(await page.request.get(`/api/directories/${id}/entries?page=1`)).json()
  expect((await page.request.post(`/api/directories/${id}/refresh`,{headers,data:{}})).ok()).toBe(true)
  await expect.poll(async()=>{
   const after=await(await page.request.get(`/api/directories/${id}/entries?page=1`)).json()
   return after.generation!==before.generation&&!after.refreshing
  }).toBe(true)
 }
 return {id,path,headers,rescan}
}

test('直接跳页、首末页、非法页码和窄屏键盘操作',async({page})=>{
 const {id}=await setup(page,'分页体验-跳转')
 await page.getByRole('button',{name:'多选',exact:true}).click()
 await page.getByLabel('选择 clip000.mp4',{exact:true}).check()
 const nav=page.getByRole('navigation',{name:'分页导航',exact:true}),input=nav.getByLabel('跳转页码')
 let calls=0
 page.on('request',request=>{if(request.url().includes(`/directories/${id}/entries?`))calls++})
 await input.fill('3');await input.press('Enter')
 await expect(page.locator('tbody tr').first()).toContainText('clip200.mp4')
 await expect(nav.locator('.page-current')).toHaveText('3')
 await expect(nav.getByRole('button',{name:'末页',exact:true})).toBeDisabled()
 await expect(page.locator('.batch-toolbar strong')).toHaveText('已选 1 项')
 expect(calls).toBe(1)
 for(const value of ['0','4','1.5','abc','']){
  await input.fill(value);await nav.getByRole('button',{name:'跳转',exact:true}).click()
  await expect(nav.getByRole('alert')).toContainText('1–3')
  await expect(nav.locator('.page-current')).toHaveText('3')
 }
 expect(calls).toBe(1)
 await input.press('Escape');await expect(input).toHaveValue('3')
 await nav.getByRole('button',{name:'首页',exact:true}).click()
 await expect(page.getByLabel('选择 clip000.mp4',{exact:true})).toBeChecked()
 await nav.getByRole('button',{name:'末页',exact:true}).click()
 await expect(nav.locator('.page-current')).toHaveText('3')
 await nav.scrollIntoViewIfNeeded();await page.screenshot({path:'test-results/page-navigation-desktop.png'});await page.locator('.pagination').screenshot({path:'test-results/page-navigation-detail.png'})
 await page.setViewportSize({width:390,height:844})
 await input.fill('2');await input.press('Enter')
 await expect(page.locator('tbody tr').first()).toContainText('clip100.mp4')
 await nav.scrollIntoViewIfNeeded();await page.screenshot({path:'test-results/page-navigation-mobile.png'})
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
 const next=await nav.getByRole('button',{name:'下一页',exact:true}).boundingBox()
 expect(next!.height).toBeGreaterThanOrEqual(44)
})

test('Shift 连选包含两端，支持反向选择和已选端点',async({page})=>{
 await setup(page,'多选体验-范围')
 await page.getByRole('button',{name:'多选',exact:true}).click()
 const checkbox=(index:number)=>page.getByLabel(`选择 clip${String(index).padStart(3,'0')}.mp4`,{exact:true})
 const checked=()=>page.locator('tbody input:checked').evaluateAll(inputs=>inputs.map(input=>input.getAttribute('aria-label')))
 await checkbox(20).click()
 await checkbox(0).click()
 await checkbox(9).click({modifiers:['Shift']})
 expect(await checked()).toEqual([...Array.from({length:10},(_,i)=>`选择 clip00${i}.mp4`),'选择 clip020.mp4'])
 await checkbox(9).click({modifiers:['Shift']})
 await expect(checkbox(9)).toBeChecked()
 await expect(page.locator('.batch-toolbar strong')).toHaveText('已选 11 项')
 await page.getByRole('button',{name:'清空',exact:true}).click()
 await checkbox(9).click({modifiers:['Shift']})
 await expect(page.locator('.batch-toolbar strong')).toHaveText('已选 1 项')
 await checkbox(0).click({modifiers:['Shift']})
 expect(await checked()).toEqual(Array.from({length:10},(_,i)=>`选择 clip00${i}.mp4`))
 await checkbox(4).click()
 await expect(checkbox(4)).not.toBeChecked()
 await checkbox(4).focus()
 await page.keyboard.press('Space')
 await expect(checkbox(4)).toBeChecked()
 await page.getByRole('button',{name:'反选',exact:true}).click()
 await expect(page.locator('.batch-toolbar strong')).toHaveText('已选 90 项')
 await checkbox(0).click({modifiers:['Shift']})
 await expect(page.locator('.batch-toolbar strong')).toHaveText('已选 91 项')
})

test('Shift 连选只作用于当前页，翻页和筛选后重新确定起点',async({page})=>{
 await setup(page,'多选体验-翻页')
 await page.getByRole('button',{name:'多选',exact:true}).click()
 const checkbox=(index:number)=>page.getByLabel(`选择 clip${String(index).padStart(3,'0')}.mp4`,{exact:true})
 await checkbox(0).click()
 await page.getByRole('button',{name:'下一页',exact:true}).click()
 await expect(checkbox(100)).toBeVisible()
 await checkbox(109).click({modifiers:['Shift']})
 await expect(page.locator('.batch-toolbar strong')).toHaveText('已选 2 项')
 await checkbox(100).click({modifiers:['Shift']})
 await expect(page.locator('.batch-toolbar strong')).toHaveText('已选 11 项')
 await expect(page.locator('tbody input:checked')).toHaveCount(10)
 await page.getByRole('button',{name:'上一页',exact:true}).click()
 await expect(checkbox(0)).toBeChecked()
 await checkbox(9).click({modifiers:['Shift']})
 await expect(page.locator('tbody input:checked')).toHaveCount(2)
 await page.getByLabel('搜索当前目录').fill('clip00')
 await expect(page.locator('tbody tr')).toHaveCount(10)
 await checkbox(4).click({modifiers:['Shift']})
 await expect(page.locator('tbody input:checked')).toHaveCount(3)
 await page.getByRole('button',{name:'退出多选',exact:true}).click()
 await page.getByRole('button',{name:'多选',exact:true}).click()
 await checkbox(8).click({modifiers:['Shift']})
 await expect(page.locator('.batch-toolbar strong')).toHaveText('已选 1 项')
})

test('目录重扫后继续翻页和刷新，保留页码及跨页勾选',async({page})=>{
 const {rescan}=await setup(page,'分页体验-重扫')
 await page.getByRole('button',{name:'多选',exact:true}).click()
 await page.getByLabel('选择 clip000.mp4',{exact:true}).check()
 await page.getByRole('button',{name:'下一页',exact:true}).click()
 await expect(page.locator('tbody tr').first()).toContainText('clip100.mp4')
 await page.getByLabel('选择 clip100.mp4',{exact:true}).check()
 await rescan()
 await page.getByRole('button',{name:'下一页',exact:true}).click()
 await expect(page.locator('tbody tr').first()).toContainText('clip200.mp4')
 await expect(page.locator('.pagination .page-current').last()).toHaveText('3')
 await page.getByRole('button',{name:'上一页',exact:true}).click()
 await expect(page.getByLabel('选择 clip100.mp4',{exact:true})).toBeChecked()
 await page.getByRole('button',{name:'刷新',exact:true}).click()
 await expect(page.locator('.pagination .page-current').last()).toHaveText('2')
 await expect(page.locator('tbody tr').first()).toContainText('clip100.mp4')
 await expect(page.locator('.batch-toolbar strong')).toHaveText('已选 2 项')
 await expect(page.getByRole('alert')).toHaveCount(0)
 await page.getByRole('button',{name:'上一页',exact:true}).click()
 await expect(page.getByLabel('选择 clip000.mp4',{exact:true})).toBeChecked()
})

test('第二页后台重扫与改名不移动阅读位置和勾选行',async({page})=>{
 const {id,headers,rescan}=await setup(page,'分页体验-原位')
 let requests=0,release!:()=>void,started!:()=>void
 const delayed=new Promise<void>(resolve=>{release=resolve})
 const waiting=new Promise<void>(resolve=>{started=resolve})
 await page.route(`**/api/directories/${id}/entries?*`,async route=>{
  if(++requests>1){started();await delayed}
  const response=await route.fetch()
  const data=await response.json()
  await route.fulfill({response,json:{...data,refreshing:requests===1}})
 })
 try{
  await page.getByRole('button',{name:'下一页',exact:true}).click()
  await expect(page.locator('tbody tr').first()).toContainText('clip100.mp4')
  await page.getByRole('button',{name:'多选',exact:true}).click()
  await page.getByLabel('选择 clip100.mp4',{exact:true}).check()
  const rows=await page.locator('tbody tr').evaluateAll(rows=>rows.map(row=>row.getAttribute('data-entry-id')))
  await page.evaluate(()=>window.scrollTo(0,1500))
  const scroll=await page.evaluate(()=>window.scrollY)
  await waiting
  const entries=await(await page.request.get(`/api/entries/status?ids=${rows[0]}`)).json()
  expect((await page.request.post(`/api/entries/${rows[0]}/rename`,{headers,data:{name:'zz-renamed.mp4',version:entries[0].version,key:'pagination-background-rename'}})).ok()).toBe(true)
  await rescan()
  release()
  await expect(page.locator('tbody tr').first()).toContainText('zz-renamed.mp4')
  expect(await page.locator('tbody tr').evaluateAll(rows=>rows.map(row=>row.getAttribute('data-entry-id')))).toEqual(rows)
  await expect(page.getByLabel('选择 zz-renamed.mp4',{exact:true})).toBeChecked()
  expect(Math.abs(await page.evaluate(()=>window.scrollY)-scroll)).toBeLessThan(5)
  await expect(page.getByRole('alert')).toHaveCount(0)
  await page.screenshot({path:'test-results/library-pagination-stable.png'})
  await page.unroute(`**/api/directories/${id}/entries?*`)
  await page.getByRole('button',{name:'刷新',exact:true}).click()
  await expect(page.locator('tbody tr').first()).toContainText('clip101.mp4')
  await expect(page.locator('.pagination .page-current').last()).toHaveText('2')
 }finally{release()}
})

test('目录缩小后保留最后一个有效页，空目录回到第一页',async({page})=>{
 const {path,rescan}=await setup(page,'分页体验-删除')
 await page.getByRole('button',{name:'下一页',exact:true}).click()
 await expect(page.locator('tbody tr').first()).toContainText('clip100.mp4')
 await page.getByRole('button',{name:'下一页',exact:true}).click()
 await expect(page.locator('tbody tr')).toHaveCount(30)
 for(let i=190;i<230;i++)unlinkSync(join(path,`clip${String(i).padStart(3,'0')}.mp4`))
 await rescan()
 await page.getByRole('button',{name:'刷新',exact:true}).click()
 await expect(page.locator('tbody tr')).toHaveCount(90)
 await expect(page.locator('.pagination .page-current').last()).toHaveText('2')
 await expect(page.getByRole('button',{name:'下一页',exact:true})).toBeDisabled()
 for(let i=0;i<190;i++)unlinkSync(join(path,`clip${String(i).padStart(3,'0')}.mp4`))
 await rescan()
 await page.getByRole('button',{name:'刷新',exact:true}).click()
 await expect(page.getByText('这里还没有内容',{exact:true})).toBeVisible()
 await expect(page.locator('.pagination .page-current').last()).toHaveText('1')
 await expect(page.getByRole('alert')).toHaveCount(0)
})

test('迟到的第二页响应不覆盖新搜索，真实错误在当前页可重试',async({page})=>{
 const {id}=await setup(page,'分页体验-迟到')
 let release!:()=>void,started!:()=>void
 const delayed=new Promise<void>(resolve=>{release=resolve})
 const waiting=new Promise<void>(resolve=>{started=resolve})
 let fail=true
 await page.route(`**/api/directories/${id}/entries?*`,async route=>{
  const params=new URL(route.request().url()).searchParams
  if(params.get('page')==='2'&&!params.get('q')){
   if(fail){fail=false;await route.fulfill({status:409,json:{message:'测试读取失败'}});return}
   started();await delayed
  }
  await route.continue()
 })
 try{
  await page.getByRole('button',{name:'下一页',exact:true}).click()
  await expect(page.getByRole('alert')).toContainText('测试读取失败')
  await page.getByRole('button',{name:'重试',exact:true}).click()
  await waiting
  await page.getByLabel('搜索当前目录').fill('clip005')
  await expect(page.locator('tbody tr')).toHaveCount(1)
  const oldResponse=page.waitForResponse(r=>r.url().includes(`/directories/${id}/entries?`)&&new URL(r.url()).searchParams.get('page')==='2')
  release();await oldResponse
  await expect(page.locator('tbody tr')).toHaveCount(1)
  await expect(page.locator('tbody tr')).toContainText('clip005.mp4')
  await expect(page.locator('.pagination .page-current').last()).toHaveText('1')
  await expect(page.getByRole('alert')).toHaveCount(0)
 }finally{release()}
})


test('长列表下滑后操作、分页与表头保持可用，窄屏表头同步横向滚动',async({page})=>{
 await setup(page,'分页体验-固定表头')
 const controls=page.locator('.library-fixed-controls'),actions=page.locator('.directory-action-row>button')
 const nav=page.getByRole('navigation',{name:'分页导航',exact:true})
 const aligned=()=>page.evaluate(()=>{
  const controls=document.querySelector('.library-fixed-controls')!.getBoundingClientRect()
  const head=document.querySelector('.file-table thead')!.getBoundingClientRect()
  return Math.abs(controls.bottom-head.top)
 })
 expect(await actions.allTextContents()).toEqual(['抽帧','重命名','编号','标记','取消标记'])
 const tops=await actions.evaluateAll(buttons=>buttons.map(button=>button.getBoundingClientRect().top))
 expect(Math.max(...tops)-Math.min(...tops)).toBeLessThan(1)
 const title=(await page.getByRole('heading',{name:'文件目录.'}).boundingBox())!
 expect(tops[0]).toBeGreaterThanOrEqual(title.y+title.height)
 await page.screenshot({path:'test-results/library-actions-desktop.png'})
 await page.evaluate(()=>window.scrollTo(0,1600))
 await expect.poll(()=>controls.evaluate(el=>Math.abs(el.getBoundingClientRect().top))).toBeLessThan(1)
 await expect.poll(aligned).toBeLessThan(2)
 // Sample immediately after each scroll, before a JS scroll handler could move
 // the header, to catch the old frame-by-frame vertical jitter.
 const jitter=await page.evaluate(async()=>{
  let largest=0
  for(let i=0;i<30;i++){
   window.scrollTo(0,700+i*17)
   const controls=document.querySelector('.library-fixed-controls')!.getBoundingClientRect()
   const head=document.querySelector('.library-table-header thead')!.getBoundingClientRect()
   largest=Math.max(largest,Math.abs(controls.bottom-head.top))
   await new Promise<void>(resolve=>requestAnimationFrame(()=>resolve()))
  }
  return largest
 })
 expect(jitter).toBeLessThan(2)
 await page.getByRole('button',{name:'多选',exact:true}).click()
 await expect.poll(aligned).toBeLessThan(2)
 await page.getByRole('button',{name:'全选',exact:true}).click()
 await expect(page.locator('.batch-toolbar strong')).toHaveText('已选 100 项')
 await nav.getByRole('button',{name:'下一页',exact:true}).click()
 await expect(page.locator('tbody tr').first()).toContainText('clip100.mp4')
 await expect(page.locator('.batch-toolbar strong')).toHaveText('已选 100 项')
 await expect.poll(aligned).toBeLessThan(2)
 await page.screenshot({path:'test-results/library-fixed-header-desktop.png'})
 await page.setViewportSize({width:390,height:844})
 await page.evaluate(()=>window.scrollTo(0,1600))
 await expect.poll(aligned).toBeLessThan(2)
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
 expect(await controls.evaluate(el=>{
  const rect=el.getBoundingClientRect(),nav=el.querySelector('.page-navigation')!.getBoundingClientRect()
  return nav.bottom<=rect.bottom
 })).toBe(true)
 const mobileButtons=await actions.evaluateAll(buttons=>buttons.map(button=>{const r=button.getBoundingClientRect();return {left:r.left,right:r.right}}))
 for(const button of mobileButtons){expect(button.left).toBeGreaterThanOrEqual(0);expect(button.right).toBeLessThanOrEqual(390)}
 await page.locator('.library-file-rows .table-scroll').evaluate(el=>{el.scrollLeft=400})
 const columnPositions=()=>page.evaluate(()=>{
  const heads=Array.from(document.querySelectorAll('.library-table-header th'),cell=>cell.getBoundingClientRect())
  const cells=Array.from(document.querySelectorAll('.library-file-rows tbody tr:first-child td'),cell=>cell.getBoundingClientRect())
  return {difference:Math.max(...heads.map((head,i)=>Math.max(Math.abs(head.left-cells[i].left),Math.abs(head.width-cells[i].width)))),top:heads[0].top,bottom:heads[0].bottom}
 })
 await expect.poll(async()=>(await columnPositions()).difference).toBeLessThan(1)
 const columns=await columnPositions()
 expect(columns.top).toBeGreaterThan(0)
 expect(columns.bottom).toBeLessThan(844)
 await page.screenshot({path:'test-results/library-fixed-header-mobile.png'})
 await nav.getByLabel('跳转页码').fill('3')
 await nav.getByRole('button',{name:'跳转',exact:true}).click()
 await expect(page.locator('tbody tr').first()).toContainText('clip200.mp4')
 await expect(page.locator('.batch-toolbar strong')).toHaveText('已选 100 项')
})
