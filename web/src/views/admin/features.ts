// All functional items across the administration panel.
//
// Kept in this chunk rather than in the root graph, so the administration's
// search index is only loaded by administrators opening the backoffice.

import type { Component } from 'vue';
import type { OaIcon } from '@/icons';
import { t, type StringKey } from '@/composables/useI18n';
import { matchesSearch } from '@/lib/search';

export interface AdminPageSpec {
  /** The path segment after /admin, empty for the dashboard. */
  slug: string;
  label: StringKey;
  icon: OaIcon;
  component: Component;
}

export interface AdminFeatureItem {
  /** The anchor ID matching the element ID on the page. */
  id: string;
  /** The admin page slug, empty for the dashboard. */
  pageSlug: string;
  /** The primary display title of the feature or section. */
  titleKey: StringKey;
  /** Related string keys whose translations are indexed for search. */
  searchKeys?: StringKey[];
  /** Synonyms and technical search terms in Chinese, English or slang. */
  keywords?: string[];
}

export interface SearchGroup {
  page: AdminPageSpec;
  matchedSelf: boolean;
  items: AdminFeatureItem[];
}

export const ADMIN_FEATURES: AdminFeatureItem[] = [
  // --- Dashboard
  {
    id: 'secInstance',
    pageSlug: '',
    titleKey: 'secInstance',
    searchKeys: ['secLast24h', 'secLast7d', 'statRequests', 'statTokens', 'statCredits'],
    keywords: ['运行概况', '系统统计', '请求数', '用户概况', '积分', 'overview', 'dashboard', 'stats', 'tokens', 'credits'],
  },
  {
    id: 'secBusiestModels',
    pageSlug: '',
    titleKey: 'secBusiestModels',
    searchKeys: ['secRanking', 'rankModels'],
    keywords: ['热门模型', '调用排行', '消耗排行', 'busiest models', 'top models'],
  },
  {
    id: 'secRecentRequests',
    pageSlug: '',
    titleKey: 'secRecentRequests',
    keywords: ['最近请求', '调用历史', '实时请求', 'recent requests', 'traffic'],
  },

  // --- Users
  {
    id: 'usersList',
    pageSlug: 'users',
    titleKey: 'searchUsers',
    searchKeys: ['usersTitle', 'anyRole', 'anyStatus', 'filterUsers', 'filterAdmins', 'filterDisabled'],
    keywords: ['用户列表', '搜索用户', '账号管理', '封禁', '解封', 'users list', 'accounts', 'banned'],
  },
  {
    id: 'secProfile',
    pageSlug: 'users',
    titleKey: 'secProfile',
    searchKeys: ['nickname', 'email', 'bio', 'avatar', 'setNewPassword'],
    keywords: ['个人资料', '修改密码', '重置密码', '昵称', '邮箱', '头像', 'password', 'avatar'],
  },
  {
    id: 'secAccess',
    pageSlug: 'users',
    titleKey: 'secAccess',
    searchKeys: ['role', 'roleAdmin', 'roleUser', 'statusActive', 'statusDisabled', 'apiRestricted'],
    keywords: ['角色与权限', '管理员角色', '封禁账号', '用户组分配', 'API限制', 'role', 'admin', 'permissions'],
  },
  {
    id: 'secAllowanceOverride',
    pageSlug: 'users',
    titleKey: 'secAllowanceOverride',
    searchKeys: ['allowanceOverrideHint', 'requestsPerMinute', 'tokensPerMinute'],
    keywords: ['额度重写', '个人配额', '速率限制', '单用户额度', 'quota override', 'rate limit'],
  },
  {
    id: 'secHeldCards',
    pageSlug: 'users',
    titleKey: 'secHeldCards',
    searchKeys: ['grantCards', 'cardFullReset'],
    keywords: ['重置卡', '额度重置卡', '赠送重置卡', 'cards', 'reset allowance'],
  },
  {
    id: 'secConversations',
    pageSlug: 'users',
    titleKey: 'secConversations',
    searchKeys: ['conversationsHint', 'viewConversations'],
    keywords: ['用户对话审计', '聊天记录', '消息记录', '会话历史', 'audit conversations', 'chat history'],
  },

  // --- Groups
  {
    id: 'groupsList',
    pageSlug: 'groups',
    titleKey: 'groupsTitle',
    searchKeys: ['groupsSubtitle'],
    keywords: ['用户组列表', '组管理', 'groups list'],
  },
  {
    id: 'addGroup',
    pageSlug: 'groups',
    titleKey: 'addGroup',
    searchKeys: ['isDefault'],
    keywords: ['新建用户组', '添加用户组', '默认用户组', 'create group', 'new group', 'default group'],
  },
  {
    id: 'secModelAccess',
    pageSlug: 'groups',
    titleKey: 'secModelAccess',
    searchKeys: ['allowAllModels', 'allowedModels', 'modelAccessTiersHint'],
    keywords: ['模型访问权限', '可用模型', '模型白名单', 'allowed models', 'model permissions'],
  },
  {
    id: 'secGroupAbilities',
    pageSlug: 'groups',
    titleKey: 'secGroupAbilities',
    searchKeys: ['showStats', 'groupAllowStats', 'groupAllowDelete'],
    keywords: ['功能权限', '允许删除对话', '显示推理耗时', 'group abilities', 'delete chat permission'],
  },
  {
    id: 'secAllowance',
    pageSlug: 'groups',
    titleKey: 'secAllowance',
    searchKeys: ['every5h', 'everyWeek', 'everyMonth', 'allowanceHint'],
    keywords: ['用户组配额', '周期配额', '5小时配额', '周配额', '月配额', 'group quota', 'allowance'],
  },

  // --- Providers
  {
    id: 'providersList',
    pageSlug: 'providers',
    titleKey: 'providersTitle',
    searchKeys: ['providersSubtitle'],
    keywords: ['供应商列表', '渠道列表', 'providers list'],
  },
  {
    id: 'addProvider',
    pageSlug: 'providers',
    titleKey: 'addProvider',
    keywords: ['添加供应商', '新建供应商', '添加渠道', 'new provider', 'add provider'],
  },
  {
    id: 'providerProtocol',
    pageSlug: 'providers',
    titleKey: 'protocol',
    searchKeys: ['protocolHint', 'protocolAnthropic', 'protocolOpenAI', 'baseURL', 'apiKey'],
    keywords: ['接口协议', 'OpenAI', 'Anthropic', 'Base URL', 'API Key', '密钥', '端点', 'endpoint'],
  },
  {
    id: 'secBehaviour',
    pageSlug: 'providers',
    titleKey: 'secBehaviour',
    searchKeys: ['reasoningStyle', 'timeoutSeconds', 'allowInsecure'],
    keywords: ['供应商行为', '超时时间', '思考推理格式', '代理', 'reasoning style', 'timeout', 'proxy'],
  },
  {
    id: 'providerDetect',
    pageSlug: 'providers',
    titleKey: 'detect',
    searchKeys: ['detecting', 'nModelsFound'],
    keywords: ['检测模型', '自动拉取模型', '探测端点', 'detect models', 'fetch models'],
  },

  // --- Models
  {
    id: 'modelsList',
    pageSlug: 'models',
    titleKey: 'modelsTitle',
    searchKeys: ['searchModels', 'dragToOrder'],
    keywords: ['模型列表', '搜索模型', '排序模型', 'models list', 'order'],
  },
  {
    id: 'addModel',
    pageSlug: 'models',
    titleKey: 'addModel',
    searchKeys: ['modelIDLabel', 'displayName'],
    keywords: ['添加模型', '新建模型', '模型ID', '显示名称', 'add model', 'display name'],
  },
  {
    id: 'modelImportExport',
    pageSlug: 'models',
    titleKey: 'exportModels',
    searchKeys: ['importModels'],
    keywords: ['导出模型', '导入模型', '模型备份', 'export models', 'import models'],
  },
  {
    id: 'secCapabilities',
    pageSlug: 'models',
    titleKey: 'secCapabilities',
    searchKeys: ['capVision', 'capReasoning', 'capImages', 'capImageGen', 'capChatImageGen', 'capStreams', 'capTools', 'contextWindow', 'maxOutputTokens'],
    keywords: ['模型特性', '模型能力', '视觉识图', '画图', '生图', '工具调用', '流式输出', '上下文长度', '最大Token', 'capabilities', 'vision', 'image generation', 'tools', 'context window'],
  },
  {
    id: 'secWeights',
    pageSlug: 'models',
    titleKey: 'secWeights',
    searchKeys: ['perRequest', 'per1kInput', 'per1kOutput', 'per1kReasoning', 'weightsHint'],
    keywords: ['计费权重', '模型费率', '倍率', '积分计费', '输入输出费用', 'pricing', 'weights', 'multiplier', 'credits'],
  },
  {
    id: 'secGroupAccess',
    pageSlug: 'models',
    titleKey: 'secGroupAccess',
    searchKeys: ['groupAccessHint'],
    keywords: ['模型组权限', '授权用户组', 'group access'],
  },
  {
    id: 'secRouting',
    pageSlug: 'models',
    titleKey: 'secRouting',
    searchKeys: ['routeTo', 'routeToHint'],
    keywords: ['模型重定向', '路由转发', '备用模型', 'routing', 'fallback model'],
  },
  {
    id: 'secThinking',
    pageSlug: 'models',
    titleKey: 'secThinking',
    searchKeys: ['reasoningStyleModel', 'reasoningTiers'],
    keywords: ['思考配置', '推理等级', '深度思考', 'extended thinking', 'reasoning tiers', 'reasoning_effort'],
  },

  // --- Availability
  {
    id: 'modelHealthOverview',
    pageSlug: 'availability',
    titleKey: 'modelHealthOverview',
    searchKeys: ['viewUptimePage', 'uptimeModels'],
    keywords: ['可用性监控', '健康度总览', '在线状态', 'uptime', 'health', 'status'],
  },
  {
    id: 'secDegradationPolicy',
    pageSlug: 'availability',
    titleKey: 'secDegradationPolicy',
    searchKeys: ['healthWarnBelow', 'healthDisableBelow', 'healthDisableAfter'],
    keywords: ['降级策略', '成功率告警', '自动熔断', '自动下线', 'degradation', 'circuit breaker', 'auto disable'],
  },
  {
    id: 'secProbingWindow',
    pageSlug: 'availability',
    titleKey: 'secProbingWindow',
    searchKeys: ['healthProbe', 'healthWindow', 'livenessHint'],
    keywords: ['探针检测', '静默模型探测', '检测窗口', 'probing', 'liveness probe', 'heartbeat'],
  },
  {
    id: 'secUserVisibility',
    pageSlug: 'availability',
    titleKey: 'secUserVisibility',
    searchKeys: ['healthShowUsers'],
    keywords: ['用户端可见性', '公开在线率', '公开状态页', 'show uptime to users', 'status page'],
  },
  {
    id: 'secResetUptime',
    pageSlug: 'availability',
    titleKey: 'secResetUptime',
    searchKeys: ['resetUptime', 'resetUptimeHint'],
    keywords: ['重置在线率', '清空探针历史', '恢复自动停用模型', 'reset uptime'],
  },

  // --- Usage
  {
    id: 'secTotals',
    pageSlug: 'usage',
    titleKey: 'secTotals',
    searchKeys: ['usageTitle', 'rangeDay', 'rangeWeek', 'rangeMonth'],
    keywords: ['用量统计', '总额度消耗', '请求总量', 'Token统计', 'totals', 'consumption'],
  },
  {
    id: 'secOverTime',
    pageSlug: 'usage',
    titleKey: 'secOverTime',
    keywords: ['用量趋势', '历史消耗折线图', 'over time chart', 'sparkline'],
  },
  {
    id: 'secRanking',
    pageSlug: 'usage',
    titleKey: 'secRanking',
    searchKeys: ['rankBy', 'rankMetric', 'chartShape', 'metricCredits', 'metricTokens', 'metricRequests'],
    keywords: ['用量排行', '消耗排行', 'ranking', 'top users', 'top models', 'usage ranking'],
  },
  {
    id: 'secByModel',
    pageSlug: 'usage',
    titleKey: 'secByModel',
    keywords: ['按模型用量', '模型消耗明细', 'usage by model'],
  },
  {
    id: 'secByProvider',
    pageSlug: 'usage',
    titleKey: 'secByProvider',
    keywords: ['按供应商用量', '供应商消耗明细', 'usage by provider'],
  },
  {
    id: 'resetQuota',
    pageSlug: 'usage',
    titleKey: 'resetQuota',
    searchKeys: ['resetExplain', 'resetScopeAll', 'resetScopeGroup', 'resetScopeUser'],
    keywords: ['重置配额', '清空已用额度', '恢复额度', '重置用户用量', 'reset quota', 'allowance reset'],
  },
  {
    id: 'defaultLimits',
    pageSlug: 'usage',
    titleKey: 'defaultLimits',
    searchKeys: ['defaultLimitsHint', 'requestsPerMinute', 'tokensPerMinute'],
    keywords: ['全局默认配额限制', '默认限速', '默认速率', 'default limits', 'instance quota limits'],
  },

  // --- Resources
  {
    id: 'resStorage',
    pageSlug: 'resources',
    titleKey: 'resStorage',
    searchKeys: ['resHeld', 'resFiles', 'resDiscarded'],
    keywords: ['存储监控', '磁盘占用', '附件体积', '用户存储', 'storage', 'disk usage'],
  },
  {
    id: 'resMemory',
    pageSlug: 'resources',
    titleKey: 'resMemory',
    searchKeys: ['resHeap', 'resProcessMemory'],
    keywords: ['内存监控', '堆内存', '进程内存', '内存泄漏排查', 'memory', 'heap'],
  },
  {
    id: 'resGoroutines',
    pageSlug: 'resources',
    titleKey: 'resGoroutines',
    searchKeys: ['resGC', 'resGCPause'],
    keywords: ['协程监控', 'GC耗时', '垃圾回收', '并发Goroutine', 'goroutines', 'gc pause'],
  },
  {
    id: 'resCPU',
    pageSlug: 'resources',
    titleKey: 'resCPU',
    searchKeys: ['resCPUShare', 'resCores', 'resCPUTime'],
    keywords: ['CPU监控', 'CPU占用率', '核心数', '系统负载', 'cpu usage', 'cores'],
  },

  // --- Codes
  {
    id: 'codesTitle',
    pageSlug: 'codes',
    titleKey: 'codesTitle',
    searchKeys: ['codesSubtitle'],
    keywords: ['兑换码列表', '卡密列表', '核销记录', 'redeem codes'],
  },
  {
    id: 'addCode',
    pageSlug: 'codes',
    titleKey: 'addCode',
    searchKeys: ['codeCards', 'codeCardDays', 'codeExpiresDays', 'copyAll'],
    keywords: ['生成兑换码', '批量生成卡密', '创建充值码', '额度卡生成', 'generate codes', 'create code'],
  },

  // --- Logs
  {
    id: 'logsFilter',
    pageSlug: 'logs',
    titleKey: 'logOutcome',
    searchKeys: ['logWindow', 'logUser', 'logModel', 'logStatus', 'logErrorCode', 'logChannel', 'logPath'],
    keywords: ['日志筛选', '搜索日志', '过滤日志', '错误日志', 'filter logs', 'error codes'],
  },
  {
    id: 'logsTable',
    pageSlug: 'logs',
    titleKey: 'navLogs',
    searchKeys: ['logHolding', 'logWhen', 'logDuration', 'logIP'],
    keywords: ['系统审计日志', '实时请求日志', '客户端IP', '响应耗时', 'audit logs', 'live stream'],
  },

  // --- Security
  {
    id: 'secAccounts',
    pageSlug: 'security',
    titleKey: 'secAccounts',
    searchKeys: ['newAccountsJoin', 'theDefaultGroup'],
    keywords: ['账户准入', '默认用户组', '新账号归属', 'account access', 'default group'],
  },
  {
    id: 'secRegistration',
    pageSlug: 'security',
    titleKey: 'secRegistration',
    searchKeys: ['anyoneCanRegister', 'requireEmail', 'verifyEmail', 'emailDomains', 'qqRequirement', 'signupsPerMinute', 'signupsPerHour', 'signupsPerIP'],
    keywords: ['开放注册', '注册开关', '强制邮箱', '邮箱验证码', '邮箱域名白名单', 'QQ号验证', '注册频率限制', '单IP限制', 'registration policy', 'email verification', 'rate limit', 'whitelist'],
  },
  {
    id: 'secTurnstile',
    pageSlug: 'security',
    titleKey: 'secTurnstile',
    searchKeys: ['turnstileSiteKey', 'turnstileSecretKey', 'turnstileOnLogin', 'turnstileOnSignup', 'turnstileOnAPIKey', 'chatChallengeRequests', 'chatChallengeWindow', 'chatChallengeClearance'],
    keywords: ['人机验证', 'Cloudflare Turnstile', '验证码', '防刷', '登录验证', '注册验证', 'API Key验证', '对话防刷挑战', 'turnstile', 'captcha', 'bot challenge', 'anti-spam'],
  },
  {
    id: 'secSignupReview',
    pageSlug: 'security',
    titleKey: 'secSignupReview',
    searchKeys: ['signupReviewIntro', 'signupReviewModel', 'signupReviewMode', 'signupReviewRefusal'],
    keywords: ['AI注册审查', '账号审核', '自动拒绝', '审查模型', '审查演练', 'signup review', 'account review'],
  },
  {
    id: 'secSecurityLog',
    pageSlug: 'security',
    titleKey: 'secSecurityLog',
    searchKeys: ['securityLogHint'],
    keywords: ['安全事件日志', '拦截记录', '风控日志', '防刷触发记录', 'security log', 'audit events'],
  },

  // --- Settings
  {
    id: 'secIdentity',
    pageSlug: 'settings',
    titleKey: 'secIdentity',
    searchKeys: ['siteName', 'signInNote', 'aboutHeading', 'aboutText', 'homeNotice', 'homeNoticeDismissible'],
    keywords: ['站点标识', '网站名称', '登录提示', '关于我们', '首页公告', '弹出公告', 'site name', 'identity', 'home notice', 'branding'],
  },
  {
    id: 'secLanding',
    pageSlug: 'settings',
    titleKey: 'secLanding',
    searchKeys: ['landingMode', 'landingLogin', 'landingIntro', 'landingChat', 'landingIntroHTML', 'trialEnabled', 'trialTurns', 'trialModel'],
    keywords: ['访问模式', '未登录页面', '介绍页HTML', '公开试用', '免登录试用', '试用轮数', '试用模型', 'landing mode', 'free trial', 'intro page'],
  },
  {
    id: 'secChat',
    pageSlug: 'settings',
    titleKey: 'secChat',
    searchKeys: ['instanceSystemPrompt', 'turnsResent'],
    keywords: ['对话策略', '全局系统提示词', '实例系统提示词', '历史上下文轮数', 'system prompt', 'turns resent', 'chat policy'],
  },
  {
    id: 'secLimits',
    pageSlug: 'settings',
    titleKey: 'secLimits',
    searchKeys: ['adminsIgnoreLimits', 'usageDisplay', 'usageDisplayAbsolute', 'usageDisplayRemaining', 'usageDisplayUsed'],
    keywords: ['配额与用量显示', '管理员免配额', '用量显示格式', '百分比显示', 'admins ignore limits', 'usage display'],
  },
  {
    id: 'secAttachments',
    pageSlug: 'settings',
    titleKey: 'secAttachments',
    searchKeys: ['attachmentMaxMB', 'attachmentRetain', 'attachmentsHint'],
    keywords: ['附件上传设置', '单文件大小上限', '保留源文件', 'attachment max size', 'retain attachments'],
  },
  {
    id: 'secCleanup',
    pageSlug: 'settings',
    titleKey: 'secCleanup',
    searchKeys: ['attachmentPurgeDays', 'attachmentPurgeDaily', 'attachmentOrphanMinutes', 'purgeNow', 'purgeNowConfirm'],
    keywords: ['自动清理', '附件保存天数', '定时清理时间', '孤立附件清理', '立即清理', 'auto cleanup', 'purge attachments', 'orphan files'],
  },
  {
    id: 'apiKeys',
    pageSlug: 'settings',
    titleKey: 'apiKeys',
    searchKeys: ['apiEnabled', 'apiEnabledHint'],
    keywords: ['开放 API Key', 'API接口开关', 'enable api keys', 'api access'],
  },
  {
    id: 'backupSettings',
    pageSlug: 'settings',
    titleKey: 'exportSettings',
    searchKeys: ['importSettings'],
    keywords: ['数据备份与迁移', '导出配置', '导入配置', '导出实例数据', 'backup', 'export settings', 'import settings'],
  },

  // --- Announcements
  {
    id: 'announcementsList',
    pageSlug: 'announcements',
    titleKey: 'announcements',
    keywords: ['公告列表', '公告管理', 'announcements list'],
  },
  {
    id: 'addAnnouncement',
    pageSlug: 'announcements',
    titleKey: 'addAnnouncement',
    keywords: ['发布新公告', '新建公告', '弹窗公告', '顶部条公告', 'new announcement', 'publish announcement'],
  },
];

/**
 * Searches across all pages and features for matching entries.
 *
 * Results are grouped by page so the rail maintains its structure while
 * exposing individual settings directly.
 */
export function searchAdminFeatures(
  query: string,
  pages: AdminPageSpec[],
): SearchGroup[] {
  const q = query.trim();
  if (!q) return [];

  const results: SearchGroup[] = [];

  for (const page of pages) {
    const pageTitle = t(page.label);
    const pageMatched = matchesSearch(q, pageTitle);
    const pageFeatures = ADMIN_FEATURES.filter((item) => item.pageSlug === page.slug);
    const matchedFeatures = pageFeatures.filter((item) => {
      const texts: string[] = [
        t(item.titleKey),
        ...(item.searchKeys ? item.searchKeys.map((k) => t(k)) : []),
        ...(item.keywords ?? []),
      ];
      return matchesSearch(q, ...texts);
    });

    if (pageMatched || matchedFeatures.length > 0) {
      results.push({
        page,
        matchedSelf: pageMatched,
        items: matchedFeatures.length > 0 ? matchedFeatures : (pageMatched ? pageFeatures : []),
      });
    }
  }

  return results;
}
