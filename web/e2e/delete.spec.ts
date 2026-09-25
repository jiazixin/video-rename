import {test,expect} from '@playwright/test'
import {existsSync,readFileSync} from 'node:fs'
import {join} from 'node:path'

test('文件和非空目录确认后真实删除，取消保留磁盘内容',async({page})=>{
 const {mediaPath}=JSON.parse(readFileSync('test-results/fixture.json','utf8'))
 await page.goto('/')
 await page.getByLabel('用户名',{exact:true}).fill('admin')
 await page.getByLabel('密码',{exact:true}).fill('test-password-123')
 await page.getByRole('button',{name:'登录',exact:true}).click()
 await page.getByRole('button',{name:'文件目录',exact:true}).click()
 for(const name of ['删除测试.txt','删除目录测试']){
  const remove=page.getByRole('button',{name:`删除 ${name}`,exact:true})
  await remove.click()
  const dialog=page.getByRole('dialog')
  await expect(dialog).toContainText(name)
  await expect(dialog).toContainText('无法通过还原功能恢复')
  if(name==='删除目录测试')await expect(dialog).toContainText('所有文件和子目录')
  await dialog.getByRole('button',{name:'取消',exact:true}).click()
  expect(existsSync(join(mediaPath,name))).toBe(true)
  await expect(remove).toBeVisible()
  await remove.click()
  await page.getByRole('button',{name:'确认永久删除',exact:true}).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(remove).toHaveCount(0)
  expect(existsSync(join(mediaPath,name))).toBe(false)
 }
 await page.reload()
 await expect(page.getByRole('button',{name:'删除 删除目录测试',exact:true})).toHaveCount(0)
})
