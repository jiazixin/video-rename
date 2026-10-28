# 多页面导航使用 Vue Router

状态：已采用，记录现有依赖的用途，并在规范整改中统一当前导航方式。

应用包含文件目录、运行记录、批次、计划、设置与分析等独立页面。使用清单和锁文件中已有的 Vue Router 4，路由定义集中在 `web/src/app/router.ts`，页面懒加载；不再维护另一套字符串页面状态或旧链接解析。

使用 `createWebHistory(import.meta.env.BASE_URL)`。Go 的 `internal/app/assets.go` 已对不存在的无扩展名应用路径回退到嵌入的 `index.html`，所以可以直接访问和刷新 `/library`、`/settings/models` 等页面。不存在的静态资源仍返回 404。旧 Hash 地址不保留兼容跳转，当前链接和浏览器测试同时更新。

文件目录 ID 由路由路径携带；筛选、页码和选中记录按所属页面保存到 URL query。浏览器前进、后退及刷新由路由管理，KeepAlive 页面仍保留已有交互与滚动恢复行为。

不引入额外状态管理库或路由抽象。以后若部署取消 SPA 回退，需要重新评估 history 模式及服务端静态入口，并同步服务端、前端和部署验证。
