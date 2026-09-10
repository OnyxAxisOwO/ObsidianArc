---
layout: home
title: 文档
description: 部署 Obsidian Arc，配置模型、用户与额度，并将应用接入 API 网关。
home:
  eyebrow: OBSIDIAN ARC / 文档
  title: Obsidian Arc
  subtitle: 你的 AI 工作空间。
  description: 在自己的服务器上使用 AI、管理模型与用户，也让常用工具通过同一个 API 网关接入。
  primary:
    text: 部署与开始使用
    link: /guide/introduction
  secondary:
    text: 查看源码
    link: https://github.com/OnyxAxisOwO/ObsidianArc
  facts:
    - 单个 Go 程序
    - SQLite / PostgreSQL
    - Vue 界面
  map:
    title: 一个入口，连接你的模型
    tag: 自托管
    inputs:
      - title: 网页工作空间
        symbol: ✳
        description: 对话 · 图像 · 历史记录
      - title: 你的应用与工具
        symbol: "{ }"
        description: 脚本 · 客户端 · SDK
    core: 用户权限 / 模型路由 / 用量与额度
    outputs:
      - OpenAI 兼容
      - Anthropic
    caption: 前端随程序打包，数据由你保存。
  pathsTitle: 从这里开始
  pathsDescription: 按照你现在要做的事，选择一份指南。
  paths:
    - title: 部署一个实例
      description: 从安装到配置第一个模型，完成首次对话。
      label: 安装与配置
      link: /guide/introduction
    - title: 使用工作空间
      description: 管理对话、生成图片，查看用量与个人设置。
      label: 功能指南
      link: /features/chat
    - title: 管理你的站点
      description: 接入服务商，为用户设置模型权限与额度。
      label: 管理后台
      link: /admin/overview
    - title: 接入 API
      description: 创建密钥，通过兼容接口调用已配置的模型。
      label: 接口参考
      link: /api/overview
  readingEyebrow: 继续阅读
  readingTitle: 配好之后，也容易维护。
  readingDescription: 配置项、备份方法和常见问题，都放在这里。
  reading:
    - title: 配置参考
      description: 环境变量、默认值与生效方式
      link: /guide/configuration
    - title: 备份与迁移
      description: 保存数据库、密钥与个人数据
      link: /admin/backups-and-data
    - title: 常见问题
      description: 登录、模型连接与流式响应排查
      link: /guide/troubleshooting
    - title: 架构设计
      description: 程序结构与实现中的取舍
      link: /architecture/design-philosophy
---