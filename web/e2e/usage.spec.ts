import { test,expect } from '@playwright/test'

async function login(page:any){
 await page.goto('/')
 await page.getByLabel('用户名',{exact:true}).fill('admin')
 await page.getByLabel('密码',{exact:true}).fill('test-password-123')
 await page.getByRole('button',{name:'登录',exact:true}).click()
 await expect(page.getByRole('heading',{name:'文件目录.'})).toBeVisible()
}

function fixture(){
 const hour=3600_000,now=Date.now()
 const bucket=(i:number,tokens:number,records:number,estimated:number)=>({from:new Date(now-(5-i)*hour).toISOString(),to:new Date(now-(4-i)*hour).toISOString(),tokens,records,estimated_records:estimated})
 const batch=(id:number,tokens:number,records:number,model:string,source:string)=>({id,model,provider:'custom',protocol:'openai',profile_id:1,profile_title:'默认视频命名',source,status:'completed',created:new Date(now-5*hour).toISOString(),started:new Date(now-5*hour).toISOString(),finished:new Date(now-2*hour).toISOString(),tokens,records,estimated_records:0,lifetime_tokens:tokens,lifetime_records:records,last_recorded_at:new Date(now-2*hour).toISOString()})
 return {
  range:{from:new Date(now-24*hour).toISOString(),to:new Date(now).toISOString(),timezone:'Asia/Shanghai',interval:'hour'},
  totals:{tokens:4700,records:5,estimated_records:1,batches:2},
  trend:[bucket(0,0,0,0),bucket(1,1200,1,0),bucket(2,0,0,0),bucket(3,3500,4,1)],
  models:[
   {key:'custom|openai|vision-a',model:'vision-a',provider:'custom',protocol:'openai',tokens:3500,records:4,estimated_records:1,batches:1},
   {key:'custom|openai|vision-b',model:'vision-b',provider:'custom',protocol:'openai',tokens:1200,records:1,estimated_records:0,batches:1}
  ],
  profiles:[{id:1,title:'默认视频命名',tokens:4700,records:5,estimated_records:1,batches:2}],
  sources:[
   {key:'manual',tokens:3500,records:4,estimated_records:1,batches:1},
   {key:'scheduled',tokens:1200,records:1,estimated_records:0,batches:1}
  ],
  top_batches:[batch(31,3500,4,'vision-a','manual'),batch(12,1200,1,'vision-b','scheduled')],
  batches:{items:[batch(31,3500,4,'vision-a','manual'),batch(12,1200,1,'vision-b','scheduled')],total:2,page:1,size:20},
  options:{models:[{key:'custom|openai|vision-a',model:'vision-a',provider:'custom',protocol:'openai'},{key:'custom|openai|vision-b',model:'vision-b',provider:'custom',protocol:'openai'}],profiles:[{id:1,title:'默认视频命名'}]}
 }
}

test('分组导航独立入口、刷新与前进后退',async({page})=>{
 await login(page)
 const nav=page.locator('.sidebar nav button')
 await expect(nav).toHaveText(['文件目录','抽帧记录','运行记录','计划管理','批次记录','模型用量','运行分析','系统设置'])
 await expect(page.locator('.sidebar-caption')).toHaveText(['工作台','任务记录','运行管理','分析','系统'])
 await expect(page.getByText('工作空间',{exact:true})).toHaveCount(0)
 await expect(page.getByText('本地媒体库',{exact:true})).toHaveCount(0)
 await expect(page.locator('.topbar')).toHaveCount(0)
 expect((await page.locator('.page-main').boundingBox())?.y).toBe(0)
 await page.route('**/api/system',route=>route.fulfill({json:{model:'/models/Qwen3-VL-8B-Instruct-long-model-name.gguf',ready:true,message:''}}))
 await page.reload()
 const model=page.locator('.model-name strong')
 await expect(model).toHaveText('Qwen3-VL-8B-Instruct-long-model-name.gguf')
 await expect(model).toHaveAttribute('title','/models/Qwen3-VL-8B-Instruct-long-model-name.gguf')
 const sidebar=page.locator('.sidebar')
 await expect(sidebar).toHaveCSS('width','256px')
 const active=nav.filter({hasText:'文件目录'})
 await expect(active).toHaveCSS('background-color','rgb(237, 246, 242)')
 const before=await active.boundingBox()
 await active.hover()
 expect(await active.boundingBox()).toEqual(before)
 await page.screenshot({path:'test-results/sidebar-desktop.png'})
 await page.setViewportSize({width:1280,height:600})
 expect(await sidebar.evaluate(el=>el.scrollHeight<=el.clientHeight)).toBeTruthy()
 expect(await page.locator('.sidebar nav').evaluate(el=>el.scrollHeight>el.clientHeight)).toBeTruthy()
 expect(await page.locator('.account').evaluate(el=>Math.abs(el.getBoundingClientRect().bottom-innerHeight)<2)).toBeTruthy()
 await page.getByRole('button',{name:'系统设置',exact:true}).click()
 await page.screenshot({path:'test-results/sidebar-short.png'})
 await page.setViewportSize({width:1440,height:1000})
 for(const name of ['抽帧记录','运行记录','计划管理','批次记录','模型用量','运行分析']){
  await page.getByRole('button',{name,exact:true}).click()
  await expect(page.getByRole('heading',{name:name+'.',exact:true})).toBeVisible()
  await expect(page.getByRole('button',{name,exact:true})).toHaveAttribute('aria-current','page')
 }
 await page.reload()
 await expect(page.getByRole('heading',{name:'运行分析.',exact:true})).toBeVisible()
 await page.goBack()
 await expect(page.getByRole('heading',{name:'模型用量.',exact:true})).toBeVisible()
 await page.goForward()
 await expect(page.getByRole('heading',{name:'运行分析.',exact:true})).toBeVisible()
 await page.setViewportSize({width:390,height:600})
 await expect(sidebar).toHaveCSS('width','64px')
 expect((await page.locator('.page-main').boundingBox())?.y).toBe(0)
 await page.getByRole('button',{name:'抽帧记录',exact:true}).click()
 await expect(page.getByRole('heading',{name:'抽帧记录.'})).toBeVisible()
 await page.getByRole('button',{name:'系统设置',exact:true}).click()
 await expect(page.getByRole('heading',{name:'系统设置.',exact:true})).toBeVisible()
})

test('用量总览、趋势数据表与模型对比',async({page})=>{
 await login(page)
 await page.route('**/api/usage/analytics*',route=>route.fulfill({json:fixture()}))
 await page.goto('/#usage')
 await expect(page.locator('.usage-kpi').first()).toContainText('4.7K')
 await expect(page.getByText('已记录请求',{exact:true})).toBeVisible()
 await expect(page.getByText('升级前的批次汇总',{exact:false})).toHaveCount(0)
 await expect(page.locator('.usage-model')).toHaveCount(2)
 await expect(page.locator('.usage-model').first()).toContainText('vision-a')
 await expect(page.locator('.usage-model').last()).toContainText('vision-b')
 await page.getByText('数据表',{exact:true}).click()
 await expect(page.locator('.usage-trend-table tbody tr')).toHaveCount(4)
 await expect(page.locator('.usage-trend-table tbody tr').nth(3).locator('td').nth(1)).toHaveText('3,500')
 await expect(page.locator('.trend-tooltip')).toHaveCount(0)
 await page.locator('.usage-trend svg').click({position:{x:300,y:100}})
 await expect(page.locator('.trend-tooltip')).toBeVisible()
 await page.screenshot({path:'test-results/usage-desktop.png',fullPage:true,animations:'disabled'})
})

test('有数据明细窄屏标题完整且分页换行',async({page})=>{
 await login(page)
 await page.route('**/api/usage/analytics*',route=>route.fulfill({json:fixture()}))
 await page.setViewportSize({width:390,height:844})
 await page.goto('/#usage')
 const title=page.getByRole('heading',{name:'批次明细',exact:true})
 const panel=page.locator('.panel').filter({has:title})
 const navigation=panel.getByRole('navigation',{name:'分页导航'})
 await expect(title).toBeVisible()
 await expect.poll(()=>title.evaluate(el=>{const range=document.createRange();range.selectNodeContents(el);return range.getClientRects().length})).toBe(1)
 const headingBox=(await title.boundingBox())!,navigationBox=(await navigation.boundingBox())!,panelBox=(await panel.boundingBox())!
 expect(navigationBox.y).toBeGreaterThanOrEqual(headingBox.y+headingBox.height)
 expect(navigationBox.x).toBeGreaterThanOrEqual(panelBox.x)
 expect(navigationBox.x+navigationBox.width).toBeLessThanOrEqual(panelBox.x+panelBox.width)
 await expect(panel.locator('tbody tr')).toHaveCount(2)
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBeTruthy()
 await panel.screenshot({path:'test-results/usage-detail-mobile.png',animations:'disabled'})
})

test('趋势悬停沿时间轴顺序移动且同一时间点附近保持稳定',async({page})=>{
 await login(page)
 await page.route('**/api/usage/analytics*',route=>route.fulfill({json:fixture()}))
 await page.goto('/#usage')
 const svg=page.locator('.usage-trend svg')
 await expect(svg).toBeVisible()
 const bounds=(await svg.boundingBox())!
 const plotWidth=bounds.width-62-18
 for(const i of [0,1,2,3,2,1,0]){
  for(const offset of [-0.1,0,0.1]){
   const x=Math.max(0,Math.min(plotWidth,plotWidth*(i+offset)/3))
   await page.mouse.move(bounds.x+62+x,bounds.y+100)
   await expect(page.locator('.trend-tooltip strong')).toHaveText(`${[0,1200,0,3500][i]!.toLocaleString()} token`)
   await expect(page.locator('.crosshair')).toHaveAttribute('x1',String(62+plotWidth*i/3))
  }
 }
 await svg.focus()
 await page.keyboard.press('ArrowRight')
 await expect(page.locator('.trend-tooltip strong')).toHaveText('1,200 token')
 await page.keyboard.press('Escape')
 await expect(page.locator('.trend-tooltip')).toHaveCount(0)
})

test('高消耗批次、明细、筛选与分页请求',async({page})=>{
 await login(page)
 const data=fixture()
 const queries:string[]=[]
 await page.route('**/api/usage/analytics*',async route=>{
  const url=new URL(route.request().url())
  queries.push(url.searchParams.toString())
  const run=Number(url.searchParams.get('run'))
  const filtered=structuredClone(data)
  if(run){
   filtered.batches.items=filtered.batches.items.filter(b=>b.id===run)
   filtered.top_batches=filtered.top_batches.filter(b=>b.id===run)
   filtered.batches.total=filtered.batches.items.length
  }
  if(url.searchParams.get('page')==='2'){
   filtered.batches={items:[],total:41,page:2,size:20}
   filtered.top_batches=[]
  }
  await route.fulfill({json:filtered})
 })
 await page.goto('/#usage')
 await expect(page.getByRole('cell',{name:'vision-a',exact:true}).first()).toBeVisible()
 await expect(page.getByText('共 2 个批次 · 第 1 / 1 页',{exact:true})).toBeVisible()
 await page.getByRole('button',{name:'仅看此批次'}).first().click()
 await expect(page.locator('.usage-run-chip')).toContainText('批次 #31')
 await expect(page.locator('.usage-run-chip button',{name:'清除批次筛选'})).toBeVisible()
 const withRun=queries.find(q=>new URLSearchParams(q).get('run')==='31')
 expect(withRun).toBeTruthy()
 await page.getByRole('button',{name:'清除批次筛选'}).click()
 await page.getByRole('combobox',{name:'模型',exact:true}).click()
 await page.getByRole('option',{name:'vision-b',exact:true}).click()
 const withModel=queries.find(q=>new URLSearchParams(q).get('model')==='custom|openai|vision-b')
 expect(withModel).toBeTruthy()
 await page.getByRole('combobox',{name:'时间范围'}).click()
 await page.getByRole('option',{name:'最近 1 小时',exact:true}).click()
 const hourly=queries.map(q=>new URLSearchParams(q)).filter(p=>p.get('to')&&p.get('from')&&!p.get('run')).at(-1)
 expect(hourly).toBeTruthy()
 if(hourly){
  const from=Date.parse(hourly.get('from')!),to=Date.parse(hourly.get('to')!)
  expect(to-from).toBeLessThanOrEqual(3600_000)
 }
 await page.getByRole('combobox',{name:'时间范围'}).click()
 await page.getByRole('option',{name:'自定义',exact:true}).click()
 await expect(page.getByLabel('起始时刻')).toBeVisible()
 await page.getByRole('button',{name:'查询',exact:true}).click()
 await expect(page.getByLabel('起始日期')).toBeVisible()
})

test('空区间显示无数据而不是伪造零值',async({page})=>{
 await login(page)
 const hour=3600_000,now=Date.now()
 await page.route('**/api/usage/analytics*',route=>route.fulfill({json:{
  range:{from:new Date(now-24*hour).toISOString(),to:new Date(now).toISOString(),timezone:'Asia/Shanghai',interval:'hour'},
  totals:{tokens:0,records:0,estimated_records:0,batches:0},
  trend:Array.from({length:24},(_,i)=>({from:new Date(now-(24-i)*hour).toISOString(),to:new Date(now-(23-i)*hour).toISOString(),tokens:0,records:0,estimated_records:0})),
  models:[],profiles:[],sources:[],top_batches:[],
  batches:{items:[],total:0,page:1,size:20},
  options:{models:[],profiles:[]}
 }}))
 await page.goto('/#usage')
 await expect(page.getByText('所选区间没有记录到的用量',{exact:true})).toBeVisible()
 await expect(page.getByText('区间内没有模型用量记录',{exact:true})).toBeVisible()
 await expect(page.getByText('当前筛选没有批次用量记录',{exact:true})).toBeVisible()
 await page.setViewportSize({width:390,height:844})
 await expect.poll(()=>page.locator('.usage-trend').evaluate(el=>el.querySelector('svg')!.getBoundingClientRect().width<=el.clientWidth)).toBe(true)
 await page.screenshot({path:'test-results/usage-mobile.png',fullPage:true,animations:'disabled'})
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBeTruthy()
})

test('接口错误显示重试而不是空数据',async({page})=>{
 await login(page)
 await page.route('**/api/usage/analytics*',route=>route.fulfill({status:500,json:{message:'数据库暂时不可用'}}))
 await page.goto('/#usage')
 await expect(page.getByRole('alert')).toContainText('数据库暂时不可用')
 let recovered=false
 await page.unroute('**/api/usage/analytics*')
 await page.route('**/api/usage/analytics*',route=>{recovered=true;return route.fulfill({json:fixture()})})
 await page.getByRole('button',{name:'重试'}).click()
 await expect(page.getByRole('cell',{name:'vision-a',exact:true}).first()).toBeVisible()
 expect(recovered).toBe(true)
})


test('无效自定义区间会中止上一请求并保留校验提示',async({page})=>{
 await login(page)
 let release!:()=>void
 const gate=new Promise<void>(resolve=>{release=resolve})
 await page.route('**/api/usage/analytics*',async route=>{await gate;await route.fulfill({json:fixture()})})
 const started=page.waitForRequest(req=>req.url().includes('/api/usage/analytics'))
 await page.goto('/#usage')
 await started
 const aborted=page.waitForEvent('requestfailed',req=>req.url().includes('/api/usage/analytics'))
 await page.getByRole('combobox',{name:'时间范围',exact:true}).click()
 await page.getByRole('option',{name:'自定义',exact:true}).click()
 try{
  await aborted
  await expect(page.getByRole('alert')).toContainText('请选择自定义区间的起止日期')
  await expect(page.locator('.usage-kpi')).toHaveCount(0)
 }finally{release()}
})

test('非法 JSON 响应显示错误并可重试',async({page})=>{
 await login(page)
 let malformed=true
 await page.route('**/api/usage/analytics*',route=>malformed?route.fulfill({status:200,contentType:'text/html',body:'<html>broken response</html>'}):route.fulfill({json:fixture()}))
 await page.goto('/#usage')
 await expect(page.getByRole('alert')).toContainText('服务响应异常，请重试')
 malformed=false
 await page.getByRole('button',{name:'重试',exact:true}).click()
 await expect(page.getByRole('alert')).toHaveCount(0)
 await expect(page.locator('.usage-kpi').first()).toBeVisible()
})
