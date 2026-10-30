import { t, type StringKey } from '@/composables/useI18n';
import { matchesSearch } from '@/lib/search';

// Keys stay untranslated until a search runs, so changing language also updates matches.
const TERMS = {
  appearance: [
    'secAppearance', 'theme', 'themeAuto', 'themeLight', 'themeDark', 'accentColour', 'accentHint',
    'languageLabel', 'languageHint', 'asciiField', 'asciiFieldHint', 'chatBackground', 'controlColours', 'backgroundVanilla', 'backgroundMint', 'backgroundCloud', 'backgroundLilac', 'backgroundPeach',
  ],
  wallpaper: [
    'secWallpaper', 'uploadWallpaper', 'chooseImage', 'remove', 'dim', 'dimHint', 'blur', 'panelTranslucency',
    'panelTranslucencyHint', 'panelBlur', 'panelBlurHint',
  ],
  chat: [
    'secChatDefaults', 'chatDefaultsHint', 'defaultModel', 'defaultEffort', 'defaultEffortHint', 'effortLow',
    'effortMedium', 'effortHigh', 'showStats', 'showStatsHint',
    'sendShortcut', 'sendShortcutHint', 'sendWithShiftEnter', 'sendWithEnter',
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
    // The claim box is inside this group's panel, so a search for it has to
    // match the box or the box is hidden by the very search that asks for it.
    'inviteClaimTitle', 'inviteClaimHint', 'inviteCodePlaceholder',
  ],
} satisfies Record<string, StringKey[]>;

type SettingsGroup = keyof typeof TERMS;

export function matchesSettings(query: string, group: SettingsGroup): boolean {
  // Translating a group's terms is the expensive part, and every group asks on
  // every render, so an empty search skips it.
  if (!query.trim()) return true;
  return matchesSearch(query, ...TERMS[group].map((key) => t(key)));
}

export type SettingsSection = 'appearance' | 'chat' | 'account' | 'security' | 'invites';

// The section each group is shown in. The panel decides which sections to show from
// this table, and the object is checked against every group, so a new group cannot
// be searchable yet missing from the section that would show it.
const SECTION_OF = {
  appearance: 'appearance',
  wallpaper: 'appearance',
  chat: 'chat',
  profile: 'account',
  connections: 'account',
  authorizations: 'account',
  password: 'account',
  data: 'account',
  twofactor: 'security',
  devices: 'security',
  invites: 'invites',
} as const satisfies Record<SettingsGroup, SettingsSection>;

/** Whether a section has anything matching the query, which is what shows it while a search runs. */
export function sectionMatches(query: string, section: SettingsSection): boolean {
  return (Object.keys(SECTION_OF) as SettingsGroup[]).some(
    (group) => SECTION_OF[group] === section && matchesSettings(query, group),
  );
}
