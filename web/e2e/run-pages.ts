import type {Page} from '@playwright/test'

export async function mockRunPages(page:Page,runs:any[]){
 await page.route('**/api/runs?*',route=>{
  const query=new URL(route.request().url()).searchParams
  const filtered=runs.filter(run=>(run.source==='schedule'||run.plan>0)===(query.get('category')==='automatic'))
  const size=Number(query.get('size'))||50,index=Number(query.get('page'))||1
  return route.fulfill({json:{items:filtered.slice((index-1)*size,index*size),total:filtered.length,page:index,size}})
 })
}
