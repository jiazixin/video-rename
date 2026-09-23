import {test,expect} from '@playwright/test'

test('文件名双击进入行内编辑，保存与取消保留扩展名',async({page})=>{
 await page.goto('/')
 await page.getByLabel('用户名',{exact:true}).fill('admin')
 await page.getByLabel('密码',{exact:true}).fill('test-password-123')
 await page.getByRole('button',{name:'登录',exact:true}).click()
 await page.getByRole('button',{name:'文件目录',exact:true}).click()

 const filename=page.getByRole('button',{name:'双击或按 Enter 重命名 VID_20260920_001.mp4'})
 await filename.click()
 await expect(page.getByRole('textbox',{name:'编辑 VID_20260920_001.mp4'})).toHaveCount(0)
 await filename.dblclick()
 const editor=page.getByRole('textbox',{name:'编辑 VID_20260920_001.mp4'})
 await expect(editor).toBeFocused()
 await expect(editor).toHaveValue('VID_20260920_001')
 await expect(page.locator('.inline-rename-field span')).toHaveText('.mp4')
 await page.screenshot({path:'test-results/inline-rename.png'})
 await editor.fill('快速重命名测试')
 await editor.press('Enter')
 const renamed=page.getByRole('button',{name:'双击或按 Enter 重命名 快速重命名测试.mp4'})
 await expect(renamed).toBeVisible()
 await expect(page.getByRole('dialog')).toHaveCount(0)

 await renamed.focus()
 await renamed.press('Enter')
 const again=page.getByRole('textbox',{name:'编辑 快速重命名测试.mp4'})
 await again.fill('不会保存')
 await again.press('Escape')
 await expect(renamed).toBeVisible()
 await expect(page.getByRole('button',{name:'双击或按 Enter 重命名 不会保存.mp4'})).toHaveCount(0)

 await renamed.dblclick()
 await again.fill('已占用名称')
 await again.press('Enter')
 await expect(page.locator('.inline-rename small[role="alert"]')).toBeVisible()
 await again.press('Escape')
 await expect(renamed).toBeVisible()

 // 用还原功能恢复共享夹具文件名:再改名一次会留下新的改名历史,
 // 还原则回到原始状态,不污染后续用例对 VID_20260920_001.mp4 的断言。
 await page.getByRole('button',{name:'还原 快速重命名测试.mp4',exact:true}).click()
 await expect(page.getByRole('dialog')).toContainText('VID_20260920_001.mp4')
 await page.getByRole('button',{name:'确认还原',exact:true}).click()
 await expect(filename).toBeVisible()
})
