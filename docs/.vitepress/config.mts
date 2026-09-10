import { defineConfig } from 'vitepress';

const base = process.env.DOCS_BASE || '/';

const sidebar = [
  { text: '开始使用', items: [
    { text: '安装', link: '/guide/introduction' },
    { text: '首次配置', link: '/guide/quick-start' },
    { text: '配置参考', link: '/guide/configuration' },
    { text: '部署与升级', link: '/guide/deployment' },
    { text: '常见问题', link: '/guide/troubleshooting' },
  ] },
  { text: '功能指南', collapsed: false, items: [
    { text: '对话与附件', link: '/features/chat' },
    { text: '图像生成', link: '/features/image-lab' },
    { text: '用量与额度', link: '/features/quota-and-billing' },
    { text: '账号与权限', link: '/features/users-and-groups' },
    { text: '访客试用', link: '/features/trial' },
    { text: '服务状态', link: '/features/uptime' },
    { text: '注册与安全', link: '/features/security' },
  ] },
  { text: '管理后台', collapsed: false, items: [
    { text: '后台概览', link: '/admin/overview' },
    { text: '服务商与模型', link: '/admin/providers-and-models' },
    { text: '用户与分组', link: '/admin/users-and-groups' },
    { text: '兑换码', link: '/admin/codes' },
    { text: '可用性管理', link: '/admin/availability' },
    { text: '系统设置', link: '/admin/system-settings' },
    { text: '请求日志', link: '/admin/logs-and-auditing' },
    { text: '公告与通知', link: '/admin/announcements' },
    { text: '备份与迁移', link: '/admin/backups-and-data' },
  ] },
  { text: 'API 参考', collapsed: false, items: [
    { text: '接入与鉴权', link: '/api/overview' },
    { text: 'Chat Completions', link: '/api/openai-completions' },
    { text: 'Anthropic Messages', link: '/api/anthropic-messages' },
    { text: 'Responses', link: '/api/openai-responses' },
    { text: '图像生成', link: '/api/images' },
    { text: '模型列表', link: '/api/models' },
  ] },
  { text: '开发与架构', collapsed: false, items: [
    { text: '架构设计', link: '/architecture/design-philosophy' },
    { text: '前端开发', link: '/architecture/frontend' },
  ] },
];

export default defineConfig({
  title: 'Obsidian Arc',
  description: 'Obsidian Arc 文档：部署、使用、管理与 API 接入。',
  lang: 'zh-CN',
  base,
  cleanUrls: true,
  srcExclude: ['ARCHITECTURE.md'],
  appearance: true,
  head: [
    ['link', { rel: 'icon', href: `${base}logo.svg`, type: 'image/svg+xml' }],
    ['meta', { name: 'theme-color', content: '#f8f8f9', media: '(prefers-color-scheme: light)' }],
    ['meta', { name: 'theme-color', content: '#1c1c1e', media: '(prefers-color-scheme: dark)' }],
  ],
  themeConfig: {
    siteTitle: 'Obsidian Arc',
    logo: '/logo.svg',
    nav: [
      { text: '指南', link: '/guide/introduction', activeMatch: '^/guide/' },
      { text: '功能', link: '/features/chat', activeMatch: '^/features/' },
      { text: '管理', link: '/admin/overview', activeMatch: '^/admin/' },
      { text: 'API', link: '/api/overview', activeMatch: '^/api/' },
      { text: '架构', link: '/architecture/design-philosophy', activeMatch: '^/architecture/' },
    ],
    sidebar,
    search: {
      provider: 'local',
      options: {
        translations: {
          button: { buttonText: '搜索文档', buttonAriaLabel: '搜索文档' },
          modal: {
            displayDetails: '显示详细结果',
            noResultsText: '没有找到相关内容',
            resetButtonTitle: '清除搜索',
            backButtonTitle: '返回',
            footer: { selectText: '选择', navigateText: '切换', closeText: '关闭' },
          },
        },
      },
    },
    socialLinks: [{ icon: 'github', link: 'https://github.com/OnyxAxisOwO/ObsidianArc' }],
    footer: {
      message: 'Obsidian Arc · 自托管 AI 工作空间',
      copyright: 'MIT License · © 2026 Obsidian Arc Contributors',
    },
    docFooter: { prev: '上一篇', next: '下一篇' },
    outline: { level: [2, 3], label: '本页内容' },
    sidebarMenuLabel: '文档目录',
    returnToTopLabel: '回到顶部',
    darkModeSwitchLabel: '外观',
    lightModeSwitchTitle: '切换到浅色模式',
    darkModeSwitchTitle: '切换到深色模式',
    skipToContentLabel: '跳到正文',
    notFound: {
      title: '页面不存在',
      quote: '这个地址没有对应的文档。可以返回首页，或搜索你需要的内容。',
      linkLabel: '返回文档首页',
      linkText: '返回首页',
    },
  },
});
