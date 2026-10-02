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
 const bucket=(i:number,finished:number,avg:number,done:number,skipped:number,failed:number)=>({from:new Date(now-(6-i)*hour).toISOString(),to:new Date(now-(5-i)*hour).toISOString(),finished_batches:finished,avg_batch_ms:avg,done,skipped,failed})
 const batch=(id:number,status:string,source:string,discovered:number,done:number,skipped:number,failed:number,duration:number)=>({id,status,source,profile_id:1,profile_title:'默认视频命名',model:'vision-a',provider:'custom',protocol:'openai',created:new Date(now-6*hour).toISOString(),started:new Date(now-5*hour).toISOString(),finished:new Date(now-2*hour).toISOString(),discovered,done,skipped,failed,duration_ms:duration,avg_item_ms:Math.round(duration/Math.max(done,1))})
 const rows=[batch(31,'completed','manual',10,8,2,0,3_600_000),batch(12,'partial_failure','scheduled',4,3,0,1,1_800_000),batch(7,'cancelled','manual',2,0,0,0,600_000)]
 const scatterRows=rows.map(b=>({run:b.id,items:b.discovered,ms:b.duration_ms,status:b.status,source:b.source,profile_title:b.profile_title}))
 return {
  range:{from:new Date(now-24*hour).toISOString(),to:new Date(now).toISOString(),timezone:'Asia/Shanghai',interval:'hour'},
  totals:{batches:3,completed:2,partial_failure:1,failed_batches:0,cancelled:1,interrupted:0,active:0,done:8,skipped:2,failed:1,timed_items:6,avg_batch_ms:1_800_000,avg_item_ms:45_000,p50_item_ms:40_000,p95_item_ms:70_000,throughput_per_hour:0.46},
  trend:[bucket(0,0,0,0,0,0),bucket(1,1,600_000,2,1,0),bucket(2,0,0,1,0,0),bucket(3,1,1_800_000,3,0,1),bucket(4,1,3_600_000,2,1,0),bucket(5,0,0,0,0,0)],
  distribution:[{label:'5 秒内',count:1},{label:'5–15 秒',count:2},{label:'15–30 秒',count:1},{label:'30–60 秒',count:2},{label:'1–2 分钟',count:0},{label:'2–5 分钟',count:0},{label:'5–10 分钟',count:0},{label:'10 分钟以上',count:0}],
  profiles:[
   {id:1,title:'默认视频命名',model:'vision-a',provider:'custom',protocol:'openai',batches:3,done:8,skipped:2,failed:1,avg_item_ms:45_000,p50_item_ms:40_000,p95_item_ms:70_000,throughput_per_hour:0.46},
   {id:2,title:'备用配置',model:'vision-b',provider:'custom',protocol:'openai',batches:0,done:0,skipped:0,failed:0,avg_item_ms:0,p50_item_ms:0,p95_item_ms:0,throughput_per_hour:0}
  ],
  scatter:scatterRows,
  failures:[{message:'模型返回命名失败',count:1,last_at:new Date(now-3*hour).toISOString()}],
  batch_failures:[],
  batches:{items:rows,total:3,page:1,size:20},
  options:{profiles:[{id:1,title:'默认视频命名'},{id:2,title:'备用配置'}]}
 }
}

test('默认打开模型用量并可从侧栏切换运行分析',async({page})=>{
 await login(page)
 await page.goto('/#usage')
 await expect(page.getByRole('heading',{name:'模型用量.'})).toBeVisible()
 await expect(page.locator('.usage-kpi').getByText('累计 token',{exact:true})).toBeVisible()
 await page.getByRole('button',{name:'运行分析',exact:true}).click()
 await expect(page.getByText('批次成功率',{exact:true})).toBeVisible()
 await expect(page.locator('.analysis-kpis .usage-kpi')).toHaveCount(10)
 await page.goto('/#usage/analysis')
 await expect(page.getByText('批次成功率',{exact:true})).toBeVisible()
 await page.getByRole('button',{name:'模型用量',exact:true}).click()
 await expect(page.locator('.usage-kpi').getByText('累计 token',{exact:true})).toBeVisible()
 await expect(page.locator('.analysis-kpis .usage-kpi')).toHaveCount(0)
})

test('指标卡、趋势图与散点渲染',async({page})=>{
 await login(page)
 await page.route('**/api/runs/analytics*',route=>route.fulfill({json:fixture()}))
 await page.goto('/#usage/analysis')
 await expect(page.locator('.analysis-kpis .usage-kpi').first()).toContainText('50.0%')
 await expect(page.locator('.analysis-kpis .usage-kpi').nth(2)).toContainText('30m')
 await expect(page.locator('.analysis-kpis .usage-kpi').nth(4)).toContainText('0.5')
 await expect(page.getByText('升级前的批次',{exact:false})).toHaveCount(0)
 await expect(page.locator('.analysis-line svg')).toHaveCount(3)
 await expect(page.locator('.analysis-legend')).toHaveCount(1)
 await expect(page.locator('.analysis-legend-item')).toHaveCount(3)
 await expect(page.locator('.analysis-hist-row')).toHaveCount(8)
 await expect(page.locator('.scatter-dot')).toHaveCount(3)
 await page.locator('.analysis-line svg').first().click({position:{x:300,y:100}})
 await expect(page.locator('.trend-tooltip')).toBeVisible()
 await page.locator('.analysis-scatter svg').click({position:{x:300,y:100}})
 await expect(page.locator('.analysis-scatter .trend-tooltip')).toContainText('批次 #')
 await expect(page.getByText('模型返回命名失败',{exact:false})).toBeVisible()
 await page.screenshot({path:'test-results/usage-analysis-desktop.png',fullPage:true,animations:'disabled'})
})

test('有数据明细窄屏标题完整且分页换行',async({page})=>{
 await login(page)
 await page.route('**/api/runs/analytics*',route=>route.fulfill({json:fixture()}))
 await page.setViewportSize({width:390,height:844})
 await page.goto('/#usage/analysis')
 const title=page.getByRole('heading',{name:'批次明细',exact:true})
 const panel=page.locator('.panel').filter({has:title})
 const navigation=panel.getByRole('navigation',{name:'分页导航'})
 await expect(title).toBeVisible()
 await expect.poll(()=>title.evaluate(el=>{const range=document.createRange();range.selectNodeContents(el);return range.getClientRects().length})).toBe(1)
 const headingBox=(await title.boundingBox())!,navigationBox=(await navigation.boundingBox())!,panelBox=(await panel.boundingBox())!
 expect(navigationBox.y).toBeGreaterThanOrEqual(headingBox.y+headingBox.height)
 expect(navigationBox.x).toBeGreaterThanOrEqual(panelBox.x)
 expect(navigationBox.x+navigationBox.width).toBeLessThanOrEqual(panelBox.x+panelBox.width)
 await expect(panel.locator('tbody tr')).toHaveCount(3)
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBeTruthy()
 await panel.screenshot({path:'test-results/usage-analysis-detail-mobile.png',animations:'disabled'})
})

test('明细排序与筛选请求参数',async({page})=>{
 await login(page)
 const data=fixture()
 const queries:string[]=[]
 await page.route('**/api/runs/analytics*',async route=>{
  queries.push(new URL(route.request().url()).searchParams.toString())
  const sorted=structuredClone(data)
  const url=new URL(route.request().url())
  if(url.searchParams.get('sort')==='duration')sorted.batches.items=[data.batches.items[0]!]
  await route.fulfill({json:sorted})
 })
 await page.goto('/#usage/analysis')
 await expect(page.getByRole('cell',{name:'vision-a',exact:true}).first()).toBeVisible()
 await expect(page.getByText('共 3 个批次 · 第 1 / 1 页',{exact:true})).toBeVisible()
 await page.getByRole('button',{name:'按时间排序'}).click()
 await expect(page.getByRole('button',{name:'按耗时排序'})).toBeVisible()
 expect(queries.find(q=>new URLSearchParams(q).get('sort')==='duration')).toBeTruthy()
 await page.getByRole('combobox',{name:'模型配置'}).click()
 await page.getByRole('option',{name:'备用配置',exact:true}).click()
 const withProfile=queries.map(q=>new URLSearchParams(q)).filter(p=>p.get('profile')==='2').at(-1)
 expect(withProfile).toBeTruthy()
 await page.getByRole('combobox',{name:'运行来源'}).click()
 await page.getByRole('option',{name:'自动计划',exact:true}).click()
 const withSource=queries.map(q=>new URLSearchParams(q)).filter(p=>p.get('source')==='scheduled').at(-1)
 expect(withSource).toBeTruthy()
 await page.getByRole('combobox',{name:'时间范围'}).click()
 await page.getByRole('option',{name:'最近 1 小时',exact:true}).click()
 const hourly=queries.map(q=>new URLSearchParams(q)).filter(p=>p.get('to')&&p.get('from')).at(-1)
 expect(hourly).toBeTruthy()
 if(hourly){
  const from=Date.parse(hourly.get('from')!),to=Date.parse(hourly.get('to')!)
  expect(to-from).toBeLessThanOrEqual(3600_000)
 }
})

test('空区间显示无数据而不是伪造零值',async({page})=>{
 await login(page)
 const hour=3600_000,now=Date.now()
 await page.route('**/api/runs/analytics*',route=>route.fulfill({json:{
  range:{from:new Date(now-24*hour).toISOString(),to:new Date(now).toISOString(),timezone:'Asia/Shanghai',interval:'hour'},
  totals:{batches:0,completed:0,partial_failure:0,failed_batches:0,cancelled:0,interrupted:0,active:0,done:0,skipped:0,failed:0,timed_items:0,avg_batch_ms:0,avg_item_ms:0,p50_item_ms:0,p95_item_ms:0,throughput_per_hour:0},
  trend:Array.from({length:24},(_,i)=>({from:new Date(now-(24-i)*hour).toISOString(),to:new Date(now-(23-i)*hour).toISOString(),finished_batches:0,avg_batch_ms:0,done:0,skipped:0,failed:0})),
  distribution:[{label:'5 秒内',count:0},{label:'5–15 秒',count:0},{label:'15–30 秒',count:0},{label:'30–60 秒',count:0},{label:'1–2 分钟',count:0},{label:'2–5 分钟',count:0},{label:'5–10 分钟',count:0},{label:'10 分钟以上',count:0}],
  profiles:[],scatter:[],failures:[],batch_failures:[],
  batches:{items:[],total:0,page:1,size:20},
  options:{profiles:[]}
 }}))
 await page.goto('/#usage/analysis')
 await expect(page.getByText('区间内没有已结束的批次',{exact:true})).toBeVisible()
 await expect(page.getByText('还没有可统计的逐项耗时',{exact:true})).toBeVisible()
 await expect(page.getByText('区间内没有批次记录',{exact:true})).toBeVisible()
 await page.setViewportSize({width:390,height:844})
 await expect.poll(()=>page.locator('.usage-trend').evaluateAll(plots=>plots.every(el=>el.querySelector('svg')!.getBoundingClientRect().width<=el.clientWidth))).toBe(true)
 await page.screenshot({path:'test-results/usage-analysis-mobile.png',fullPage:true,animations:'disabled'})
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBeTruthy()
})

test('接口错误显示重试而不是空数据',async({page})=>{
 await login(page)
 await page.route('**/api/runs/analytics*',route=>route.fulfill({status:500,json:{message:'数据库暂时不可用'}}))
 await page.goto('/#usage/analysis')
 await expect(page.getByRole('alert')).toContainText('数据库暂时不可用')
 let recovered=false
 await page.unroute('**/api/runs/analytics*')
 await page.route('**/api/runs/analytics*',route=>{recovered=true;return route.fulfill({json:fixture()})})
 await page.getByRole('button',{name:'重试'}).click()
 await expect(page.getByRole('cell',{name:'vision-a',exact:true}).first()).toBeVisible()
 expect(recovered).toBe(true)
})


test('无效自定义区间会中止上一请求并保留校验提示',async({page})=>{
 await login(page)
 let release!:()=>void
 const gate=new Promise<void>(resolve=>{release=resolve})
 await page.route('**/api/runs/analytics*',async route=>{await gate;await route.fulfill({json:fixture()})})
 const started=page.waitForRequest(req=>req.url().includes('/api/runs/analytics'))
 await page.goto('/#usage/analysis')
 await started
 const aborted=page.waitForEvent('requestfailed',req=>req.url().includes('/api/runs/analytics'))
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
 await page.route('**/api/runs/analytics*',route=>malformed?route.fulfill({status:200,contentType:'text/html',body:'<html>broken response</html>'}):route.fulfill({json:fixture()}))
 await page.goto('/#usage/analysis')
 await expect(page.getByRole('alert')).toContainText('服务响应异常，请重试')
 malformed=false
 await page.getByRole('button',{name:'重试',exact:true}).click()
 await expect(page.getByRole('alert')).toHaveCount(0)
 await expect(page.locator('.usage-kpi').first()).toBeVisible()
})
