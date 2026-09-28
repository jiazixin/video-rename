import {test,expect,type Page} from '@playwright/test'
import {mockRunPages} from './run-pages'

async function login(page:Page){
 await page.request.post('/api/auth/login',{data:{username:'admin',password:'test-password-123'}})
 await page.goto('/')
 await expect(page.getByRole('heading',{name:'文件目录.'})).toBeVisible()
}

test('运行记录和抽帧记录直接请求指定页，筛选重置页码',async({page})=>{
 let logPage=1,successPage=1,failurePage=1
 await page.route('**/api/logs?*',route=>{
  const q=new URL(route.request().url()).searchParams;logPage=Number(q.get('page'))
  return route.fulfill({json:{items:[{id:logPage,category:'success',action:'manual',old_path:`第${logPage}页.mp4`,new_path:'新名称.mp4',created:'2026-09-29T00:00:00Z'}],total:q.get('q')?1:501,page:q.get('q')?1:logPage,next:0}})
 })
 await page.route('**/api/extractions?*',route=>{
  const q=new URL(route.request().url()).searchParams;successPage=Number(q.get('success_page'));failurePage=Number(q.get('failure_page'))
  const group=(status:string,index:number)=>({page:index,total:501,next:0,items:[{id:index,run:0,path:`${status}第${index}页.mp4`,source:'manual',status,started:'2026-09-29T00:00:00Z',finished:'2026-09-29T00:00:01Z',duration_ms:1000,frames:1}]})
  return route.fulfill({json:{success:group('success',successPage),failure:group('failure',failurePage),running:0}})
 })
 await login(page)
 await page.getByRole('button',{name:'运行记录',exact:true}).click()
 let nav=page.getByRole('navigation',{name:'分页导航',exact:true})
 await nav.getByLabel('跳转页码').fill('9');await nav.getByRole('button',{name:'跳转',exact:true}).click()
 await expect.poll(()=>logPage).toBe(9);await expect(page.locator('tbody')).toContainText('第9页.mp4')
 await page.getByLabel('搜索运行记录').fill('查询')
 await expect(nav.locator('.page-current')).toHaveText('1');await expect(nav.getByLabel('跳转页码')).toBeDisabled()
 await page.getByRole('button',{name:'抽帧记录',exact:true}).click()
 nav=page.getByRole('navigation',{name:'分页导航',exact:true})
 await nav.getByLabel('跳转页码').fill('8');await nav.getByLabel('跳转页码').press('Enter')
 await expect.poll(()=>successPage).toBe(8)
 await page.getByRole('button',{name:'失败记录',exact:true}).click()
 await expect(nav.locator('.page-current')).toHaveText('1')
 await nav.getByLabel('跳转页码').fill('6');await nav.getByLabel('跳转页码').press('Enter')
 await expect.poll(()=>failurePage).toBe(6)
 await page.getByRole('button',{name:'成功记录',exact:true}).click()
 await expect(nav.locator('.page-current')).toHaveText('8')
})

test('批次与详情直接跳页，目录选择器跳页不提交外层表单',async({page})=>{
 const run={id:1001,source:'manual',plan:0,status:'completed',discovered:205,done:205,skipped:0,failed:0,created:'2026-09-29T00:00:00Z'}
 await mockRunPages(page,Array.from({length:501},(_,i)=>({...run,id:1001-i})))
 await page.route('**/api/runs/1001',route=>route.fulfill({json:{...run,targets:[]}}))
 await page.route('**/api/runs/1001/items?*',route=>{
  const index=Number(new URL(route.request().url()).searchParams.get('page'))
  return route.fulfill({json:{page:index,total:205,items:[{id:index,entry:index,status:'done',original_path:`第${index}页视频.mp4`}]}})
 })
 await login(page)
 await page.getByRole('button',{name:'批次记录',exact:true}).click()
 const pager=page.getByLabel('批次记录分页')
 await pager.getByLabel('跳转页码').fill('10');await pager.getByLabel('跳转页码').press('Enter')
 await expect(page.locator('.run-id').first()).toHaveText('#551')
 await pager.getByRole('button',{name:'首页',exact:true}).click()
 await page.locator('.run-entry').first().getByRole('button',{name:'详情',exact:true}).click()
 const detail=page.getByRole('dialog')
 await detail.getByLabel('跳转页码').fill('3');await detail.getByLabel('跳转页码').press('Enter')
 await expect(detail).toContainText('第3页视频.mp4')
 await detail.getByRole('button',{name:'关闭',exact:true}).last().click()
 await page.getByRole('button',{name:'计划管理',exact:true}).click()
 await page.getByRole('button',{name:'新建手动计划',exact:true}).click()
 const dialog=page.getByRole('dialog')
 await dialog.getByRole('button',{name:'返回定位测试',exact:true}).click()
 await expect(dialog.locator('.page-readout')).toContainText('4')
 await dialog.getByLabel('跳转页码').fill('3');await dialog.getByLabel('跳转页码').press('Enter')
 await expect(dialog.getByLabel('选择 目录_100',{exact:true})).toBeVisible()
 await expect(dialog).toBeVisible()
})
