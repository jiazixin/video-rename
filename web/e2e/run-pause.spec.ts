import {test,expect} from '@playwright/test'
import {mockRunPages} from './run-pages'

test('批次暂停确认、错误重试、恢复、详情与窄屏',async({page})=>{
 await page.goto('/')
 await page.getByLabel('用户名',{exact:true}).fill('admin')
 await page.getByLabel('密码',{exact:true}).fill('test-password-123')
 await page.getByRole('button',{name:'登录',exact:true}).click()
 const run={id:910,task:'rename',status:'running',source:'manual',created:new Date().toISOString(),started:new Date().toISOString(),finished:'',done:1,discovered:4,failed:0,skipped:0,targets:[1],recursive:true}
 const completed={...run,id:911,status:'completed',done:4,finished:new Date().toISOString()}
 await mockRunPages(page,[run,completed])
 await page.route('**/api/runs/910',route=>route.fulfill({json:run}))
 await page.route('**/api/runs/910/items?*',route=>route.fulfill({json:{items:[],directories:[],all_total:0,failed_total:0,total:0,page:1}}))
 let pauses=0,resumes=0,fail=true
 await page.route('**/api/runs/910/pause',route=>{
  expect(route.request().postDataJSON()).toEqual({confirm:true});pauses++
  if(fail)return route.fulfill({status:409,json:{message:'状态暂未更新，请重试'}})
  run.status='paused';return route.fulfill({json:{ok:true}})
 })
 await page.route('**/api/runs/910/resume',route=>{
  expect(route.request().postDataJSON()).toEqual({confirm:true});resumes++
  run.status='running';return route.fulfill({json:{ok:true}})
 })
 await page.getByRole('button',{name:'批次记录',exact:true}).click()
 const row=page.locator('.run-entry').filter({hasText:'#910'})
 const dialog=page.getByRole('dialog')
 await expect(page.locator('.run-entry').filter({hasText:'#911'}).getByRole('button',{name:'暂停调度'})).toHaveCount(0)
 await row.getByRole('button',{name:'暂停调度'}).click()
 await expect(dialog).toContainText('正在执行的步骤会正常完成')
 await dialog.getByRole('button',{name:'返回',exact:true}).click()
 expect(pauses).toBe(0)
 await row.getByRole('button',{name:'暂停调度'}).click()
 await dialog.getByRole('button',{name:'确认暂停'}).click()
 await expect(dialog.getByRole('alert')).toContainText('请重试')
 fail=false
 await dialog.getByRole('button',{name:'确认暂停'}).click()
 await expect(dialog).toHaveCount(0)
 await expect(row).toContainText('已暂停')
 await expect(row).not.toHaveClass(/is-running/)
 await expect(row.getByRole('button',{name:'还原批次'})).toBeDisabled()
 await expect(row.getByRole('button',{name:'取消',exact:true})).toBeEnabled()
 await row.getByRole('button',{name:'详情'}).click()
 await dialog.getByRole('button',{name:'继续',exact:true}).click()
 await expect(dialog).toContainText('从未完成的任务继续处理')
 await dialog.getByRole('button',{name:'确认继续'}).click()
 await expect(row.getByRole('button',{name:'暂停调度'})).toBeVisible()
 expect(resumes).toBe(1)
 await page.setViewportSize({width:390,height:844})
 await row.getByRole('button',{name:'暂停调度'}).click()
 await expect(dialog.getByRole('button',{name:'确认暂停'})).toBeVisible()
 expect(await dialog.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBeTruthy()
 await page.screenshot({path:'test-results/run-pause-mobile.png'})
 await dialog.getByRole('button',{name:'确认暂停'}).click()
 await expect(row).toContainText('已暂停')
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBeTruthy()
 await page.screenshot({path:'test-results/run-paused-mobile.png'})
})
