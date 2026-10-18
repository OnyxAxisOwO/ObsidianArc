import { pluginStrings } from '@/plugins/registry';

// The shared pool's words on the bonus page. Not in i18n.ts for the reason
// audit-text.ts gives: the English dictionary rides in the first paint, whose
// budget (test/bundle.test.ts) these would push over, and only an
// administrator on this page reads them. What an account sees of a pool is in
// i18n.ts with the rest of the usage panel.

const en = {
  kindHint: 'A reserve is spent only after everything else is, and has no switch. A shared pool is one balance every account draws on, for the models it covers.',
  modeOnly: 'Pool only',
  modeHint: 'User chooses: each account decides whether the pool is spent first. Always first: spent before the allowance, which takes over once the pool is empty. Pool only: its models are paid by the pool alone, and refused when it is empty or the day’s share is used.',
  heading: 'Pool',
  reset: 'Refill',
  resetHint: 'Puts the balance back to the amount below at the start of each period, in the check-in time zone. A top-up lasts until the next refill.',
  resetNever: 'Never — top up by hand',
  resetDay: 'Every day',
  resetWeek: 'Every week (Monday)',
  resetMonth: 'Every month',
  resetAmount: 'Refill to',
  dailyLimit: 'Per account per day',
  dailyLimitHint: 'The most one account may draw in a day. Empty or 0 for no limit.',
  initial: 'Starting balance',
  initialHint: 'Empty starts at the refill amount.',
  balance: '{left} of {total} left · {count} accounts drew on it today',
  adjust: 'Change the balance',
  adjustOp: 'How',
  adjustAmount: 'Credits',
  adjustHint: 'Add puts credits in, and a negative amount takes them out. Set makes what is left exactly this.',
  add: 'Add',
  set: 'Set what is left',
  adjusted: 'The pool now has {left} left.',
  errSharedNoGrants: 'A shared pool is topped up for everyone; it is not granted to accounts.',
  errKindChange: 'A shared pool cannot become another kind of bonus, nor another kind a pool.',
  errNotShared: 'That bonus is not a shared pool.',
} as const;

const zh: Record<keyof typeof en, string> = {
  kindHint: '备用额度只在所有其他额度都用完之后才动用，用户没有开关。共享额度池是所有人共用的一份余额，只给它适用的模型用。',
  modeOnly: '只能用额度池',
  modeHint: '用户自选：每个用户自己决定是否优先用额度池。强制启用：先于个人额度使用，额度池用完后改用个人额度。只能用额度池：这些模型只从额度池扣，额度池用完或当天份额用完就不能再用。',
  heading: '额度池',
  reset: '重置',
  resetHint: '每个周期开始时把余额重置为下面的数额，按签到设置的时区算。手动充值的部分到下次重置为止。',
  resetNever: '不重置，手动充值',
  resetDay: '每天',
  resetWeek: '每周（周一）',
  resetMonth: '每月',
  resetAmount: '重置为',
  dailyLimit: '每人每天上限',
  dailyLimitHint: '一个账户一天最多从额度池用多少。留空或填 0 表示不限。',
  initial: '初始余额',
  initialHint: '留空则等于重置数额。',
  balance: '剩余 {left} / {total} · 今天有 {count} 个账户用过',
  adjust: '调整余额',
  adjustOp: '方式',
  adjustAmount: '积分',
  adjustHint: '增加：把积分加进去，填负数是扣掉。设为：把剩余额度直接设成这个数。',
  add: '增加',
  set: '设为剩余',
  adjusted: '额度池现在剩余 {left}。',
  errSharedNoGrants: '共享额度池是给所有人充值的，不能单独发给某个账户。',
  errKindChange: '共享额度池不能改成别的类型，别的类型也不能改成共享额度池。',
  errNotShared: '这个赠金不是共享额度池。',
};

export const poolText = pluginStrings(en, zh);

// The refusals only a pool's panel can meet, in the reader's language. The
// screen does not offer either move, so these are what a stale page sees.
const REFUSALS: Record<string, keyof typeof en> = {
  bonus_shared_no_grants: 'errSharedNoGrants',
  bonus_kind_change: 'errKindChange',
  bonus_not_shared: 'errNotShared',
};

export function poolRefusal(code: string): string | undefined {
  return Object.hasOwn(REFUSALS, code) ? poolText(REFUSALS[code]!) : undefined;
}
