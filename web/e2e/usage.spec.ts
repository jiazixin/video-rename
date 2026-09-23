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
 const bucket=(i:number,tokens:number,records:number,estimated:number,legacy:number)=>({from:new Date(now-(5-i)*hour).toISOString(),to:new Date(now-(4-i)*hour).toISOString(),tokens,records,estimated_records:estimated,legacy_records:legacy})
 const batch=(id:number,tokens:number,records:number,model:string,source:string)=>({id,model,provider:'custom',protocol:'openai',profile_id:1,profile_title:'默认视频命名',source,status:'completed',created:new Date(now-5*hour).toISOString(),started:new Date(now-5*hour).toISOString(),finished:new Date(now-2*hour).toISOString(),tokens,records,estimated_records:0,legacy_records:0,lifetime_tokens:tokens,lifetime_records:records,last_recorded_at:new Date(now-2*hour).toISOString()})
 return {
  range:{from:new Date(now-24*hour).toISOString(),to:new Date(now).toISOString(),timezone:'Asia/Shanghai',interval:'hour'},
  totals:{tokens:4700,records:5,estimated_records:1,legacy_records:1,batches:2},
  trend:[bucket(0,0,0,0,0),bucket(1,1200,1,0,0),bucket(2,0,0,0,0),bucket(3,3500,4,1,1)],
  models:[
   {key:'custom|openai|vision-a',model:'vision-a',provider:'custom',protocol:'openai',tokens:3500,records:4,estimated_records:1,legacy_records:1,batches:1},
   {key:'custom|openai|vision-b',model:'vision-b',provider:'custom',protocol:'openai',tokens:1200,records:1,estimated_records:0,legacy_records:0,batches:1}
  ],
  profiles:[{id:1,title:'默认视频命名',tokens:4700,records:5,estimated_records:1,legacy_records:1,batches:2}],
  sources:[
   {key:'manual',tokens:3500,records:4,estimated_records:1,legacy_records:1,batches:1},
   {key:'scheduled',tokens:1200,records:1,estimated_records:0,legacy_records:0,batches:1}
  ],
  top_batches:[batch(31,3500,4,'vision-a','manual'),batch(12,1200,1,'vision-b','scheduled')],
  batches:{items:[batch(31,3500,4,'vision-a','manual'),batch(12,1200,1,'vision-b','scheduled')],total:2,page:1,size:20},
  options:{models:[{key:'custom|openai|vision-a',model:'vision-a',provider:'custom',protocol:'openai'},{key:'custom|openai|vision-b',model:'vision-b',provider:'custom',protocol:'openai'}],profiles:[{id:1,title:'默认视频命名'}]}
 }
}

test('导航包含用量分析并直达路由',async({page})=>{
 await login(page)
 const nav=page.locator('.sidebar nav button')
 await expect(nav.filter({hasText:'运行计划'})).toBeVisible()
 const names=await nav.allInnerTexts()
 expect(names.indexOf('用量分析')).toBeGreaterThan(names.indexOf('运行计划'))
 expect(names.indexOf('系统设置')).toBeGreaterThan(names.indexOf('用量分析'))
 await page.goto('/#usage')
 await expect(page.getByRole('heading',{name:'用量分析.'})).toBeVisible()
 await page.getByRole('button',{name:'用量分析',exact:true}).click()
 await expect(page.locator('.topbar strong')).toHaveText('用量分析')
})

test('用量总览、趋势数据表与模型对比',async({page})=>{
 await login(page)
 await page.route('**/api/usage/analytics*',route=>route.fulfill({json:fixture()}))
 await page.goto('/#usage')
 await expect(page.locator('.usage-kpi').first()).toContainText('4.7K')
 await expect(page.getByText('已记录请求',{exact:true})).toBeVisible()
 await expect(page.getByText('其中 1 条为升级前的批次汇总',{exact:false})).toBeVisible()
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
  totals:{tokens:0,records:0,estimated_records:0,legacy_records:0,batches:0},
  trend:Array.from({length:24},(_,i)=>({from:new Date(now-(24-i)*hour).toISOString(),to:new Date(now-(23-i)*hour).toISOString(),tokens:0,records:0,estimated_records:0,legacy_records:0})),
  models:[],profiles:[],sources:[],top_batches:[],
  batches:{items:[],total:0,page:1,size:20},
  options:{models:[],profiles:[]}
 }}))
 await page.goto('/#usage')
 await expect(page.getByText('所选区间没有记录到的用量',{exact:true})).toBeVisible()
 await expect(page.getByText('区间内没有模型用量记录',{exact:true})).toBeVisible()
 await expect(page.getByText('当前筛选没有批次用量记录',{exact:true})).toBeVisible()
 await page.setViewportSize({width:390,height:844})
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
