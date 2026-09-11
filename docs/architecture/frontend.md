# 前端开发

主应用使用 Vue、TypeScript 和 Sass，由 Vite 构建，产物写入 `internal/web/dist` 后嵌入 Go 程序。VitePress 文档站独立位于 `docs/`，不打包进应用。

## 启动开发环境

在仓库根目录安装现有前端依赖，并启动 Vite：

```bash
npm --prefix web ci --no-fund --no-audit
npm --prefix web run dev
```

另开一个终端，启动开发模式的 Go 服务：

```bash
OBSIDIAN_DEV=1 OBSIDIAN_LOG_LEVEL=debug go run ./cmd/server
```

PowerShell 使用：

```powershell
$env:OBSIDIAN_DEV = "1"
$env:OBSIDIAN_LOG_LEVEL = "debug"
go run ./cmd/server
```

访问 Go 服务的 `http://localhost:8080`。开发模式会将前端资源代理到本机 Vite，API 仍由 Go 处理。

## 组件、路由与状态

运行时依赖为 `vue`、`vue-router`、`@vueuse/core` 和 `lucide-vue-next`。项目没有另外引入 UI 组件库、CSS 框架或状态管理库。

路由使用 `createWebHistory`。设置、密钥和用量是聊天工作区中的子面板，切换时保持聊天布局。状态通过组合式函数与模块中的响应式引用管理。

共享组件、布局、路由和状态文件影响多个页面，修改前应读完整文件。组件卸载时不要通过清空共享目标引用触发已经卸载的组件再次渲染。

## 样式与语言

颜色使用 `web/src/styles/_chat.scss` 定义的 `--ai-*` 变量。侧边面板采用独立列，按钮为胶囊形，表单字段使用填充背景和焦点环。

非聊天界面的通用控件样式位于 `_surfaces.scss`。组件使用全局类名，不使用 scoped 样式。修改现有样式时记录原因，避免为小改动重排整份样式表。

界面字符串通过 `composables/useI18n` 的 `t()` 读取。英文键定义类型，中文使用同一组键，缺失翻译由类型检查发现。不要直接导入底层翻译函数绕过响应式更新。

## 内容渲染

聊天 Markdown 通过 DOM API 创建节点，不把模型输出当作 HTML 插入。普通外部图片标记转换为链接；应用附件使用专门组件展示。

`safe-intro.ts` 是主应用唯一使用 `innerHTML` 解析输入的位置。它在脱离页面的模板中解析，再按允许列表重建内容，不直接挂载原始 HTML。

数学公式渲染器按需加载并生成 MathML。它的支持范围由项目实现决定，不保证覆盖完整 LaTeX 语法。

## 分包与检查

主应用产物约束为五个文件：

```text
index-[hash].js
index-[hash].css
AdminPage-[hash].js
i18n.zh-[hash].js
math-[hash].js
```

后台、中文字典和数学渲染器独立加载。主入口的静态导入不能意外把它们重新拉进主包。`web/test/bundle.test.ts` 检查实际构建结果，因此测试前需要存在前端产物。

```bash
npm --prefix web run build
make test-full
```

## 维护文档站

```bash
npm --prefix docs ci --no-fund --no-audit
npm --prefix docs run dev
npm --prefix docs run build
npm --prefix docs test
```

文档主题复用应用的中性配色，首页内容放在 `docs/index.md`，主题组件和样式放在 `docs/.vitepress/theme/`。导航、搜索和 Markdown 由 VitePress 提供。

文档修改后需要检查实际命令、路由和默认值，不要用推测补全功能。构建会检查页面链接，测试还会检查产物中的站内链接和锚点。
