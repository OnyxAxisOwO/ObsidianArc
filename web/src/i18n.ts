// Interface text, in the two languages the standalone build shipped.
//
// One dictionary for the whole application rather than one per module: the
// same sentence turns up in the chat and in settings, and two copies of it
// drift. Keys are grouped by screen only by naming convention.
//
// The language follows the browser unless the account has chosen one. It is
// read before any network call, so the sign-in page is already translated.

const LANGUAGE_KEY = 'obsidian-arc-language';

export type Language = 'en' | 'zh';

const en = {
  // --- shell
  brand: 'Obsidian Arc',
  theme: 'Theme',
  settings: 'Settings',
  administration: 'Administration',
  account: 'Account',
  signOut: 'Sign out',
  admin: 'Admin',
  railToggle: 'Conversation list',

  // --- auth
  signIn: 'Sign in',
  signingIn: 'Signing in…',
  createAccount: 'Create account',
  creatingAccount: 'Creating…',
  welcomeBack: 'Welcome back',
  welcomeBackBody: 'Sign in to pick up where you left off.',
  createAccountTitle: 'Create an account',
  createAccountBody: 'Pick a username and a password. An email address is optional.',
  firstAccountTitle: 'Create the first account',
  firstAccountBody: 'This server has no accounts yet. The first one becomes the administrator.',
  username: 'Username',
  usernameOrEmail: 'Username or email',
  emailOptional: 'Email (optional)',
  password: 'Password',
  passwordHint: 'At least 8 characters',
  fillBothFields: 'Fill in both fields.',
  haveAccount: 'Already have an account? ',
  noAccount: 'No account yet? ',
  createOne: 'Create one',
  registrationClosed: 'Registration is closed on this server.',

  // --- chat
  newChat: 'New chat',
  history: 'History',
  noHistory: 'No conversations yet.',
  deleteChat: 'Delete this conversation',
  confirmDelete: 'Delete this conversation?',
  clearAll: 'Clear all',
  confirmClearAll: 'Delete all conversations? This cannot be undone.',
  rename: 'Rename',
  renamePrompt: 'Conversation name',
  placeholder: 'Send a message…',
  placeholderFirst: 'What do you want to talk about?',
  send: 'Send',
  attach: 'Attach an image',
  removeImage: 'Remove this image',
  dropHint: 'Drop images here',
  tooManyImages: 'Up to {count} images per message.',
  imageTooLarge: 'That image is too large.',
  imageFailed: 'That image could not be read.',
  imagesUnsupported: 'This model cannot read images. Pick a vision-capable model, or edit the message and remove the picture.',
  streamFallback: 'Streaming was not available this time, so the whole answer arrived at once — {reason}.',
  statsStreamed: 'streamed',
  statsOneShot: 'one-shot',
  statsFirstToken: 'first token {seconds}s',
  statsTokens: '{input} in / {output} out',
  statsOutputOnly: '{output} out',
  statsSpeed: '{tps} tok/s',
  reasoning: 'Reasoning',
  reasoningLive: 'Thinking…',
  reasoningToggle: 'Extended thinking',
  reasoningEffort: 'Thinking effort',
  effortLow: 'Brief',
  effortMedium: 'Balanced',
  effortHigh: 'Thorough',
  stop: 'Stop',
  stopped: 'Stopped.',
  thinking: 'Thinking…',
  emptyTitle: 'Start a conversation',
  emptyBody: 'Pick a model and ask it something. Your conversations are saved to your account.',
  suggestionExplain: 'Explain a concept simply',
  suggestionBrainstorm: 'Brainstorm names for a project',
  suggestionWrite: 'Write a first draft of something',
  suggestionSummarize: 'Summarize a long piece of text',
  suggestionDebug: 'Help me debug an error',
  suggestionPlan: 'Plan out a weekend trip',
  suggestionCompare: 'Compare two options for me',
  suggestionRewrite: 'Rewrite this in a different tone',
  suggestionLearn: 'Teach me something new',
  suggestionDecide: 'Help me think through a decision',
  suggestionDraft: 'Draft an email for me',
  suggestionCritique: 'Give me honest feedback on an idea',
  setupTitle: 'No models available yet',
  setupBody: 'An administrator has to add a provider and at least one model before anyone can chat here.',
  setupAction: 'Open administration',
  setupBodyUser: 'Ask an administrator to give your group access to a model.',
  edit: 'Edit',
  copy: 'Copy',
  copied: 'Copied',
  cancel: 'Cancel',
  saveAndResend: 'Save & resend',
  regenerate: 'Regenerate',
  retry: 'Try again',
  failed: 'Something went wrong.',
  composerMenu: 'More',
  addImage: 'Add an image',
  addFile: 'Add a file',
  addFileHint: 'Text and code files',
  fileUnsupported: 'Only text and code files can be attached. Use "Add an image" for pictures.',
  fileTooLarge: 'That file is too large.',
  fileAdded: 'Added {name}.',
  quotaUnlimited: 'No usage limit on this account.',
  quota5h: '5 hours',
  quotaWeek: 'This week',
  quotaMonth: 'This month',
  quotaResets: 'Resets {when}',
  inMinutes: 'in {count} min',
  inHours: 'in {count} h',
  inDays: 'in {count} d',
  modelNone: 'Choose a model',
  modelsEmpty: 'No models available.',
  loading: 'Loading…',
} as const;

export type StringKey = keyof typeof en;

const zh: Record<StringKey, string> = {
  brand: 'Obsidian Arc',
  theme: '主题',
  settings: '设置',
  administration: '管理后台',
  account: '账户',
  signOut: '退出登录',
  admin: '管理员',
  railToggle: '对话列表',

  signIn: '登录',
  signingIn: '正在登录…',
  createAccount: '创建账户',
  creatingAccount: '正在创建…',
  welcomeBack: '欢迎回来',
  welcomeBackBody: '登录后继续之前的对话。',
  createAccountTitle: '创建账户',
  createAccountBody: '设置用户名和密码，邮箱可以不填。',
  firstAccountTitle: '创建第一个账户',
  firstAccountBody: '这台服务器还没有任何账户，第一个注册的人将成为管理员。',
  username: '用户名',
  usernameOrEmail: '用户名或邮箱',
  emailOptional: '邮箱（可选）',
  password: '密码',
  passwordHint: '至少 8 个字符',
  fillBothFields: '请填写两个字段。',
  haveAccount: '已经有账户了？',
  noAccount: '还没有账户？',
  createOne: '创建一个',
  registrationClosed: '这台服务器已关闭注册。',

  newChat: '新对话',
  history: '历史记录',
  noHistory: '还没有对话记录。',
  deleteChat: '删除这个对话',
  confirmDelete: '删除这个对话？',
  clearAll: '清空全部',
  confirmClearAll: '删除全部对话？此操作无法撤销。',
  rename: '重命名',
  renamePrompt: '对话名称',
  placeholder: '发送消息…',
  placeholderFirst: '想聊点什么？',
  send: '发送',
  attach: '添加图片',
  removeImage: '移除这张图片',
  dropHint: '把图片拖到这里',
  tooManyImages: '每条消息最多 {count} 张图片。',
  imageTooLarge: '这张图片太大了。',
  imageFailed: '这张图片读不出来。',
  imagesUnsupported: '这个模型看不了图片。请换一个支持看图的模型，或者编辑这条消息把图片去掉。',
  streamFallback: '这次没能流式返回，整段答案一次性到达——{reason}。',
  statsStreamed: '流式',
  statsOneShot: '一次性',
  statsFirstToken: '首字 {seconds}s',
  statsTokens: '输入 {input} / 输出 {output}',
  statsOutputOnly: '输出 {output}',
  statsSpeed: '{tps} tok/s',
  reasoning: '思考过程',
  reasoningLive: '正在思考…',
  reasoningToggle: '深度思考',
  reasoningEffort: '思考强度',
  effortLow: '简略',
  effortMedium: '均衡',
  effortHigh: '充分',
  stop: '停止',
  stopped: '已停止。',
  thinking: '正在思考…',
  emptyTitle: '开始一段对话',
  emptyBody: '选一个模型，问它点什么。对话会保存在你的账户里。',
  suggestionExplain: '用简单的话解释一个概念',
  suggestionBrainstorm: '帮项目想几个名字',
  suggestionWrite: '写一段初稿',
  suggestionSummarize: '总结一大段文字',
  suggestionDebug: '帮我调试一个报错',
  suggestionPlan: '规划一次周末出行',
  suggestionCompare: '帮我比较两个选项',
  suggestionRewrite: '换一种语气重写',
  suggestionLearn: '教我一些新东西',
  suggestionDecide: '帮我理清一个决定',
  suggestionDraft: '帮我起草一封邮件',
  suggestionCritique: '给我一些坦率的反馈',
  setupTitle: '还没有可用的模型',
  setupBody: '需要管理员先添加一个服务商和至少一个模型，才能开始对话。',
  setupAction: '打开管理后台',
  setupBodyUser: '请管理员给你所在的用户组开放一个模型。',
  edit: '编辑',
  copy: '复制',
  copied: '已复制',
  cancel: '取消',
  saveAndResend: '保存并重新发送',
  regenerate: '重新生成',
  retry: '重试',
  failed: '出错了。',
  composerMenu: '更多',
  addImage: '添加图片',
  addFile: '添加文件',
  addFileHint: '文本与代码文件',
  fileUnsupported: '只能添加文本和代码文件。图片请用“添加图片”。',
  fileTooLarge: '这个文件太大了。',
  fileAdded: '已添加 {name}。',
  quotaUnlimited: '当前账户没有用量限制。',
  quota5h: '5 小时',
  quotaWeek: '本周',
  quotaMonth: '本月',
  quotaResets: '{when}重置',
  inMinutes: '{count} 分钟后',
  inHours: '{count} 小时后',
  inDays: '{count} 天后',
  modelNone: '选择模型',
  modelsEmpty: '没有可用的模型。',
  loading: '加载中…',
};

const dictionaries: Record<Language, Record<StringKey, string>> = { en, zh };

let active: Language = detect();

function detect(): Language {
  try {
    const stored = localStorage.getItem(LANGUAGE_KEY);
    if (stored === 'en' || stored === 'zh') return stored;
  } catch {
    // Storage disabled; fall through to the browser's preference.
  }
  return (navigator.language || 'en').toLowerCase().startsWith('zh') ? 'zh' : 'en';
}

export function language(): Language {
  return active;
}

export function setLanguage(next: Language): void {
  active = next;
  try {
    localStorage.setItem(LANGUAGE_KEY, next);
  } catch {
    // Best effort; the choice still applies for this page load.
  }
}

/** Looks up a string, substituting {name} placeholders. */
export function t(key: StringKey, vars?: Record<string, string | number>): string {
  const value = dictionaries[active][key] ?? en[key] ?? key;
  if (!vars) return value;
  return value.replace(/\{(\w+)\}/g, (match, name: string) =>
    name in vars ? String(vars[name]) : match,
  );
}
