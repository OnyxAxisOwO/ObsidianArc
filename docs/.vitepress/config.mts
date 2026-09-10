import { defineConfig } from 'vitepress';

export default defineConfig({
  title: 'Obsidian Arc',
  description: '自托管多用户 AI 聊天服务与 API 网关',
  lang: 'zh-CN',
  base: '/',
  cleanUrls: true,
  srcExclude: ['ARCHITECTURE.md'],

  head: [
    ['link', { rel: 'icon', href: '/logo.svg', type: 'image/svg+xml' }],
    ['link', { rel: 'alternate icon', href: '/favicon.ico' }],
    ['meta', { name: 'theme-color', content: '#6366f1' }],
    ['style', {}, `
      /* 屏蔽多余的 hr 防止出现双重横线 */
      .vp-doc hr {
        display: none !important;
      }
      /* 主页全景目录完全去除横线与边框 */
      .home-toc-wrapper h2 {
        border-top: none !important;
        border-bottom: none !important;
      }
    `],
  ],

  themeConfig: {
    siteTitle: 'Obsidian Arc',
    logo: '/logo.svg',

    nav: [
      { text: '首页', link: '/' },
      { text: '目录', link: '/guide/introduction' },
      { text: '指南', link: '/guide/introduction' },
      { text: '功能实现', link: '/features/chat' },
      { text: '管理后台', link: '/admin/overview' },
      { text: 'API 接口', link: '/api/overview' },
      { text: '架构设计', link: '/architecture/design-philosophy' },
    ],

    sidebar: {
      '/guide/': [
        {
          text: '使用指南',
          items: [
            { text: '安装', link: '/guide/introduction' },
            { text: '首次配置', link: '/guide/quick-start' },
            { text: '配置参考', link: '/guide/configuration' },
            { text: '运维', link: '/guide/deployment' },
            { text: '常见问题', link: '/guide/troubleshooting' },
          ],
        },
      ],
      '/features/': [
        {
          text: '功能实现',
          items: [
            { text: '对话与流式交互', link: '/features/chat' },
            { text: '访客试用与落地页模式', link: '/features/trial' },
            { text: '图像生成', link: '/features/image-lab' },
            { text: '服务健康与可用性监控', link: '/features/uptime' },
            { text: '配额、用量账本与兑换卡', link: '/features/quota-and-billing' },
            { text: '安全防护与风控策略', link: '/features/security' },
            { text: '多用户与权限分组', link: '/features/users-and-groups' },
          ],
        },
      ],
      '/admin/': [
        {
          text: '管理后台',
          items: [
            { text: '后台概览与监控', link: '/admin/overview' },
            { text: '服务商与模型调度', link: '/admin/providers-and-models' },
            { text: '用户与分组管理', link: '/admin/users-and-groups' },
            { text: '兑换卡码', link: '/admin/codes' },
            { text: '服务健康与熔断器', link: '/admin/availability' },
            { text: '全局系统设置', link: '/admin/system-settings' },
            { text: '请求日志与排错', link: '/admin/logs-and-auditing' },
            { text: '全站公告与通知', link: '/admin/announcements' },
            { text: '数据备份与迁移', link: '/admin/backups-and-data' },
          ],
        },
      ],
      '/api/': [
        {
          text: '开发者接口与网关',
          items: [
            { text: '网关概述与鉴权', link: '/api/overview' },
            { text: 'OpenAI Completions 兼容', link: '/api/openai-completions' },
            { text: 'Anthropic Messages (Claude Code)', link: '/api/anthropic-messages' },
            { text: 'OpenAI Responses (Codex)', link: '/api/openai-responses' },
            { text: '图像生成接口', link: '/api/images' },
            { text: '模型发现接口', link: '/api/models' },
          ],
        },
      ],
      '/architecture/': [
        {
          text: '架构设计',
          items: [
            { text: '架构设计与底层约束', link: '/architecture/design-philosophy' },
            { text: '前端架构与渲染安全', link: '/architecture/frontend' },
          ],
        },
      ],
    },

    search: {
      provider: 'local',
      options: {
        translations: {
          button: {
            buttonText: '搜索文档',
            buttonAriaLabel: '搜索文档',
          },
          modal: {
            noResultsText: '无法找到相关结果',
            resetButtonTitle: '清除查询条件',
            footer: {
              selectText: '选择',
              navigateText: '切换',
              closeText: '关闭',
            },
          },
        },
      },
    },

    socialLinks: [
      { icon: 'github', link: 'https://github.com/OnyxAxisOwO/ObsidianArc' },
    ],

    footer: {
      message: 'Released under the MIT License.',
      copyright: 'Copyright © 2026 Axis AI & Obsidian Arc Contributors',
    },

    docFooter: {
      prev: '上一篇',
      next: '下一篇',
    },

    outline: {
      level: [2, 3],
      label: '本页导航',
    },
  },
});
