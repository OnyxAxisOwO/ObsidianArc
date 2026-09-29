import { t, type StringKey } from '@/composables/useI18n';
import { matchesSearch } from '@/lib/search';

// Keys stay untranslated until a search runs, so changing language also updates matches.
const TERMS = {
  appearance: [
    'secAppearance', 'theme', 'themeAuto', 'themeLight', 'themeDark', 'accentColour', 'accentHint',
    'languageLabel', 'languageHint', 'chatBackground', 'controlColours', 'backgroundVanilla', 'backgroundMint', 'backgroundCloud', 'backgroundLilac', 'backgroundPeach',
  ],
  wallpaper: [
    'secWallpaper', 'uploadWallpaper', 'chooseImage', 'remove', 'dim', 'dimHint', 'blur', 'panelTranslucency',
    'panelTranslucencyHint', 'panelBlur', 'panelBlurHint',
  ],
  chat: [
    'secChatDefaults', 'chatDefaultsHint', 'defaultModel', 'defaultEffort', 'defaultEffortHint', 'effortLow',
    'effortMedium', 'effortHigh', 'showStats', 'showStatsHint',
  ],
  profile: [
    'secProfile', 'nickname', 'nicknameHint', 'email', 'bio', 'avatar',
    'avatarPlaceholderUser', 'avatarHint', 'registrationUserAgent', 'save',
  ],
  password: [
    'secPassword', 'passwordSectionHint', 'currentPassword', 'newPassword', 'newPasswordHint', 'changePassword',
    'setPassword', 'setPasswordHint',
  ],
  connections: [
    'secConnections', 'connectionsHint', 'connect', 'disconnect', 'connected', 'notConnected',
  ],
  authorizations: [
    'secAuthorizations', 'authorizationsHint', 'authorizationsNone', 'withdraw',
  ],
  data: [
    'secData', 'dataHint', 'exportData', 'importData',
  ],
  twofactor: [
    'secSecurity', 'secTwoFactor', 'twoFactorIntro', 'twoFactorSetUp', 'twoFactorRegenerate',
    'twoFactorTurnOff', 'twoFactorRecoveryLabel', 'twoFactorAppTitle',
  ],
  devices: [
    'secDevices', 'devicesIntro', 'deviceThisDevice', 'deviceSignOut', 'deviceSignOutOthers',
  ],
  invites: [
    'secInvites', 'inviteYourCode', 'inviteYourLink', 'inviteRegenerate', 'inviteesEmpty', 'haveInviteCode',
  ],
} satisfies Record<string, StringKey[]>;

export type SettingsGroup = keyof typeof TERMS;

export function matchesSettings(query: string, group: SettingsGroup): boolean {
  return matchesSearch(query, ...TERMS[group].map((key) => t(key)));
}
