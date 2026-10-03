// Turns a notification's kind + params into words and a glyph, in whichever
// language the reader has chosen.
//
// The one rule this file exists to keep true: the server never sends a
// sentence, only kind and params, so every string a reader sees for a
// notification is produced here, at render time — a new language, or a
// change of wording, is a change to this file alone.

import { t, tn } from '@/composables/useI18n';
import {
  IconBell, IconGift, IconImage, IconInfo, IconLayers, IconLock, IconMessage, IconPulse, IconRefresh, IconShield, IconUser,
  IconUsers, type OaIcon,
} from '@/icons';
import type { Notification } from '@/api/notifications';
import type { StringKey } from '@/i18n';
import { describeUserAgent } from '@/lib/ua';
import { absoluteTime } from '@/lib/format';
import { pluginNotification } from '@/plugins/registry';

/** A credit amount as it is shown: two places at most. */
function credits(value: unknown): string {
  return String(Math.round(Number(value ?? 0) * 100) / 100);
}

export interface NotificationText {
  title: string;
  body: string;
  icon: OaIcon;
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function count(value: unknown): number {
  return typeof value === 'number' ? value : 0;
}

// What an administrator changed, as the server names it. Keys rather than
// words on the wire, so the list reads in the reader's language; a key this
// build does not know is left out rather than shown raw.
const CHANGED: Record<string, StringKey> = {
  profile: 'notifyWhatProfile',
  role: 'notifyWhatRole',
  group: 'notifyWhatGroup',
  status: 'notifyWhatStatus',
  api: 'notifyWhatAPI',
  password: 'notifyWhatPassword',
};

const TWO_FACTOR: Record<string, StringKey> = {
  enabled: 'notifyBodyTwoFactorEnabled',
  disabled: 'notifyBodyTwoFactorDisabled',
  reset: 'notifyBodyTwoFactorReset',
  recovery_used: 'notifyBodyTwoFactorRecoveryUsed',
  recovery_regenerated: 'notifyBodyTwoFactorRecoveryRegenerated',
};

function changed(value: unknown): string {
  if (!Array.isArray(value)) return '';
  return value
    .map((key) => (typeof key === 'string' && CHANGED[key] ? t(CHANGED[key]) : ''))
    .filter(Boolean)
    .join(t('notifyWhatJoin'));
}

/** Describes one notification for the toast stack and the bell's list. */
export function describeNotification(n: Notification): NotificationText {
  const params = n.params ?? {};
  switch (n.kind) {
    case 'feedback_new':
      return {
        title: t('notifyTitleFeedbackNew'),
        body: t('notifyBodyFeedbackNew', { title: text(params['title']) }),
        icon: IconMessage,
      };
    case 'feedback_reply':
      return { title: t('notifyTitleFeedbackReply'), body: t('notifyBodyFeedbackReply'), icon: IconMessage };
    case 'announcement':
      return {
        title: t('notifyTitleAnnouncement'),
        body: t('notifyBodyAnnouncement', { title: text(params['title']) }),
        icon: IconInfo,
      };
    case 'cards_granted':
      return {
        title: t('notifyTitleCardsGranted'),
        body: tn(count(params['count']), 'notifyBodyCardsGrantedOne', 'notifyBodyCardsGrantedOther'),
        icon: IconLayers,
      };
    case 'bonus_granted':
      return {
        title: t('notifyTitleBonusGranted'),
        body: t('notifyBodyBonusGranted', { name: text(params['name']), amount: credits(params['amount']) }),
        icon: IconGift,
      };
    case 'bonus_expiring':
      return {
        title: t('notifyTitleBonusExpiring'),
        body: t('notifyBodyBonusExpiring', {
          name: text(params['name']), amount: credits(params['amount']), when: absoluteTime(Number(params['expires_at'] ?? 0)),
        }),
        icon: IconGift,
      };
    case 'quota_reset':
      return { title: t('notifyTitleQuotaReset'), body: t('notifyBodyQuotaReset'), icon: IconRefresh };
    case 'account_changed': {
      const what = changed(params['what']);
      return {
        title: t('notifyTitleAccountChanged'),
        body: what ? t('notifyBodyAccountChangedWhat', { what }) : t('notifyBodyAccountChanged'),
        icon: IconUser,
      };
    }
    case 'model_disabled':
      return {
        title: t('notifyTitleModelDisabled'),
        body: t('notifyBodyModelDisabled', { model: text(params['model']) }),
        icon: IconPulse,
      };
    case 'signup_flagged': {
      const username = text(params['username']);
      return {
        title: username ? t('notifyTitleSignupFlaggedUser', { username }) : t('notifyTitleSignupFlagged'),
        body: t('notifyBodySignupFlagged'),
        icon: IconShield,
      };
    }
    case 'new_device_login': {
      const ua = text(params['ua']);
      const ip = text(params['ip']);
      return {
        title: t('notifyTitleNewDeviceLogin'),
        body: ua || ip
          ? t('notifyBodyNewDeviceLoginFrom', { device: describeUserAgent(ua), ip: ip ? ` (${ip})` : '' })
          : t('notifyBodyNewDeviceLogin'),
        icon: IconShield,
      };
    }
    case 'invite_joined': {
      // Three shapes for one kind, picked by what this particular invite did:
      // it hit a milestone (cards > 0), it moved the count toward one
      // (remaining > 0, rewards are on), or rewards are off entirely
      // (remaining arrives as 0 for that reason too — cards already ruled out
      // the milestone case by then).
      const username = text(params['username']);
      const cards = count(params['cards']);
      const remaining = count(params['remaining']);
      const body = cards > 0
        ? tn(cards, 'notifyBodyInviteJoinedOne', 'notifyBodyInviteJoinedOther', {
            every: count(params['every']), cards,
          })
        : remaining > 0
          ? t('notifyBodyInviteJoinedProgress', { username, remaining })
          : t('notifyBodyInviteJoinedPlain', { username });
      return { title: t('notifyTitleInviteJoined'), body, icon: IconUsers };
    }
    case 'two_factor_changed': {
      const kind = text(params['kind']);
      return {
        title: t('notifyTitleTwoFactorChanged'),
        body: TWO_FACTOR[kind] ? t(TWO_FACTOR[kind]) : t('notifyBodyTwoFactorChanged'),
        icon: IconLock,
      };
    }
    case 'image_generation_complete': {
      const count = Number(params['count']) || 1;
      return {
        title: t('notifyTitleImageGenComplete'),
        body: t('notifyBodyImageGenComplete', { count }),
        icon: IconImage,
      };
    }
    case 'image_generation_failed': {
      const message = String(params['message'] || '');
      return {
        title: t('notifyTitleImageGenFailed'),
        body: message || t('notifyBodyImageGenFailed'),
        icon: IconImage,
      };
    }
    default: {
      // A plugin's own kind, worded by the plugin that raises it.
      const plugin = pluginNotification(n.kind, params);
      if (plugin) return { ...plugin, icon: IconBell };
      // A kind this build has never heard of — an older client after a
      // server adds one. Shown rather than dropped: a blank title reads as a
      // bug, a generic one reads as "something happened".
      return { title: t('notifyTitleGeneric'), body: n.kind, icon: IconBell };
    }
  }
}
