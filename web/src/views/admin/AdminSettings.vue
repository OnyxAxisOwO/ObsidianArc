<script setup lang="ts">
// Instance settings.
//
// A short list, because every entry here is a decision an operator has to
// understand before they change it. Anything with a sensible answer for every
// deployment is a constant, not a setting.

import { computed, onMounted, ref } from 'vue';
import { adminApi, type AdminModel, type HeldAttachments } from '@/admin/api';
import { pickJSONFile, saveAsFile } from '@/api/backup';
import { ApiError } from '@/api/client';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import AdminControlCard from './AdminControlCard.vue';
import AdminWorkbench from './AdminWorkbench.vue';
import type { WorkbenchGroup } from './workbench';
import { useSettingsDraft } from './settingsDraft';
import { IconHome, IconSpark, IconFile, IconKey, IconInfo, IconBell, IconMessage, IconSliders, IconTrash, IconImage, IconSun, IconMoon } from '@/icons';
import OaNumberField from '@/components/OaNumberField.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTextArea from '@/components/OaTextArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t, type StringKey } from '@/composables/useI18n';
import { formatBytes } from '@/lib/format';
import AdminFailure from './AdminFailure.vue';
import { useAdminView } from './adminView';
import { site } from '@/stores/session';

const view = useAdminView();
view.setTitle(t('adminSettingsTitle'), t('controlSettingsSubtitle'));

const SEARCH_GROUPS = {
  secIdentity: [
    'secIdentity', 'siteName', 'siteNameHint', 'signInNote', 'signInNoteHint', 'browserTitle', 'browserTitleHint',
    'siteLogo', 'siteLogoHint',
  ],
  secLoginBg: [
    'secLoginBg', 'loginBgHint', 'loginBgLandscape', 'loginBgPortrait', 'loginBgLandscapeLight',
    'loginBgLandscapeDark', 'loginBgPortraitLight', 'loginBgPortraitDark', 'loginBgFallbackNote',
  ],
  secPWA: [
    'secPWA', 'controlPWAHint', 'pwaName', 'pwaNameHint', 'pwaShortName', 'pwaShortNameHint', 'pwaDescription',
    'pwaDescriptionHint', 'pwaThemeColor', 'pwaThemeColorHint', 'pwaBackgroundColor', 'pwaBackgroundColorHint',
    'pwaIconUrl', 'pwaIconUrlHint',
  ],
  secAbout: ['controlAbout', 'aboutHeading', 'aboutHeadingHint', 'aboutText', 'aboutTextHint'],
  secHomeNotice: ['homeNotice', 'homeNoticeHint', 'homeNoticeDismissible', 'homeNoticeDismissibleHint'],
  secFeedback: ['navFeedback', 'feedbackShowStaffName', 'feedbackShowStaffNameHint'],
  secLanding: [
    'secLanding', 'landingMode', 'landingModeHint', 'landingLogin', 'landingIntro', 'landingChat',
    'landingSite', 'landingIntroHTML', 'landingIntroHTMLHint', 'trialEnabled', 'trialEnabledHint', 'trialTurns',
    'trialTurnsHint', 'trialModel', 'trialFirstAvailable',
  ],
  secChat: [
    'secChat', 'instanceSystemPrompt', 'instanceSystemPromptHint', 'turnsResent', 'turnsResentHint',
    'agentMaxRounds', 'agentMaxRoundsHint',
  ],
  secLimits: [
    'secLimits', 'adminsIgnoreLimits', 'adminsIgnoreLimitsHint', 'maxConcurrentPerUser', 'maxConcurrentPerUserHint',
    'usageDisplay', 'usageDisplayHint', 'usageDisplayAbsolute', 'usageDisplayRemaining', 'usageDisplayUsed',
  ],
  secAttachments: [
    'secAttachments', 'attachmentsHint', 'attachmentMaxMB', 'attachmentMaxMBHint', 'attachmentRetain',
    'attachmentRetainHint',
  ],
  secCleanup: [
    'secCleanup', 'cleanupHint', 'attachmentPurgeDays', 'attachmentPurgeDaysHint', 'attachmentPurgeDaily',
    'attachmentPurgeDailyHint', 'attachmentOrphanMinutes', 'attachmentOrphanMinutesHint', 'purgeNow',
    'purgeNowConfirm',
  ],
  apiKeys: [
    'apiKeys', 'apiEnabled', 'apiEnabledHint',
  ],
} satisfies Record<string, StringKey[]>;
const error = ref('');
const loaded = ref(false);
const models = ref<Pick<AdminModel, 'id' | 'display_name' | 'model_id' | 'enabled' | 'provider_name'>[]>([]);
const held = ref<HeldAttachments>({ held: 0, bytes: 0 });
const flash = ref('');
// The strip is coloured as a failure by default, because that is what it
// usually carries. An import and a purge both report success into it, and
// those read as errors unless the element is told otherwise.
const flashOK = ref(false);
const saveLabel = ref('');
const busy = ref(false);
const purging = ref(false);
const loginBackgrounds = ref<Record<string, string>>({});
const uploadingVariant = ref('');
const dragOverVariant = ref('');
const siteLogoUrl = ref('');
const uploadingLogo = ref(false);
const dragOverLogo = ref(false);

const BG_VARIANTS = [
  { id: 'landscape_light', label: 'loginBgLandscapeLight', modeIcon: IconSun },
  { id: 'landscape_dark', label: 'loginBgLandscapeDark', modeIcon: IconMoon },
  { id: 'portrait_light', label: 'loginBgPortraitLight', modeIcon: IconSun },
  { id: 'portrait_dark', label: 'loginBgPortraitDark', modeIcon: IconMoon },
] as const;

const form = ref({
  siteName: '',
  description: '',
  browserTitle: '',
  pwaName: '',
  pwaShortName: '',
  pwaDescription: '',
  pwaThemeColor: '#18181b',
  pwaBackgroundColor: '#18181b',
  pwaIconUrl: '',
  aboutHeading: '',
  aboutText: '',
  homeNotice: '',
  homeNoticeDismissible: true,
  feedbackShowStaffName: true,
  landingMode: 'login',
  landingIntro: '',
  trialEnabled: false,
  trialTurns: 3 as number | null,
  trialModel: '',
  systemPrompt: '',
  maxTurns: 40 as number | null,
  agentMaxRounds: 8 as number | null,
  adminBypass: false,
  maxConcurrent: 4 as number | null,
  usageDisplay: 'absolute',
  attachmentMaxMB: 6 as number | null,
  attachmentRetain: false,
  purgeAfterDays: 0 as number | null,
  purgeDailyAt: '',
  orphanMinutes: 60 as number | null,
  apiEnabled: false,
});

const enabledModels = computed(() => models.value.filter((entry) => entry.enabled));

// The three landing modes want different fields, and showing all of them at
// once invites setting a trial on a front door that is a sign-in card.
const showIntro = computed(() => form.value.landingMode === 'intro');
const showTrialSwitch = computed(() => form.value.landingMode === 'chat');
const showTrialDetail = computed(() => showTrialSwitch.value && form.value.trialEnabled);

const heldLabel = computed(() => (held.value.held
  ? t('attachmentsHeld', { count: held.value.held, size: formatBytes(held.value.bytes) })
  : t('attachmentsHeldNone')));

/**
 * The one description of what this form holds, so a save and an export cannot
 * come to disagree about it.
 */
function collect(): Record<string, string> {
  return {
    'site.name': form.value.siteName.trim(),
    'site.description': form.value.description.trim(),
    'site.browser_title': form.value.browserTitle.trim(),
    'pwa.name': form.value.pwaName.trim(),
    'pwa.short_name': form.value.pwaShortName.trim(),
    'pwa.description': form.value.pwaDescription.trim(),
    'pwa.theme_color': form.value.pwaThemeColor.trim(),
    'pwa.background_color': form.value.pwaBackgroundColor.trim(),
    'pwa.icon_url': form.value.pwaIconUrl.trim(),
    'about.title': form.value.aboutHeading.trim(),
    'about.body': form.value.aboutText.trim(),
    'home.notice': form.value.homeNotice.trim(),
    'home.notice_dismissible': String(form.value.homeNoticeDismissible),
    'feedback.show_staff_name': String(form.value.feedbackShowStaffName),
    'landing.mode': form.value.landingMode,
    'landing.intro': form.value.landingIntro.trim(),
    'landing.trial_enabled': String(form.value.trialEnabled),
    'landing.trial_turns': String(form.value.trialTurns ?? 3),
    'landing.trial_model': form.value.trialModel,
    'quota.admins_bypass': String(form.value.adminBypass),
    'quota.max_concurrent': String(form.value.maxConcurrent ?? 4),
    'quota.usage_display': form.value.usageDisplay,
    'chat.default_system_prompt': form.value.systemPrompt.trim(),
    'chat.max_turns': String(form.value.maxTurns ?? 40),
    'chat.agent_max_rounds': String(form.value.agentMaxRounds ?? 8),
    'api.enabled': String(form.value.apiEnabled),
    'attachments.max_mb': String(form.value.attachmentMaxMB ?? 6),
    'attachments.retain': String(form.value.attachmentRetain),
    'attachments.purge_after_days': String(form.value.purgeAfterDays ?? 0),
    'attachments.purge_daily_at': form.value.purgeDailyAt.trim(),
    'attachments.orphan_minutes': String(form.value.orphanMinutes ?? 60),
  };
}

const { dirty, accept } = useSettingsDraft(collect);

async function save(): Promise<void> {
  if (!loaded.value || error.value || busy.value) return;
  const values = collect();
  busy.value = true;
  saveLabel.value = t('saving');
  flash.value = '';
  flashOK.value = false;
  try {
    await adminApi.saveSettings(values);
    accept(values);
    // App.vue's document.title watcher reads this same ref; without patching
    // it here, the tab this very form is open in would keep its old title
    // until the next full page load re-fetched /api/site.
    if (site.value) {
      const name = form.value.siteName.trim();
      site.value = { ...site.value, name, browser_title: form.value.browserTitle.trim() || name };
    }
    saveLabel.value = t('saved');
    window.setTimeout(() => { saveLabel.value = ''; }, 1500);
  } catch (failure) {
    flash.value = failure instanceof ApiError ? failure.message : String(failure);
    saveLabel.value = '';
  } finally {
    busy.value = false;
  }
}

// Export takes what the form is showing, not what was last saved: an operator
// who has just typed a value expects the file to contain it.
function exportSettings(): void {
  const stamp = new Date().toISOString().slice(0, 10);
  saveAsFile(`obsidian-arc-settings-${stamp}.json`, JSON.stringify(collect(), null, 2));
}

function importSettings(): void {
  void pickJSONFile(1024 * 1024)
    .then((document) => {
      if (document === null) return null;
      if (!document || typeof document !== 'object' || Array.isArray(document)) {
        throw new ApiError(0, 'malformed', t('importSettingsMalformed'));
      }
      // Everything is stored as a string; a file written by hand may well
      // carry numbers and booleans, and refusing those would be pedantry.
      const values: Record<string, string> = {};
      for (const [key, value] of Object.entries(document as Record<string, unknown>)) {
        if (value !== null && typeof value !== 'object') values[key] = String(value);
      }
      return adminApi.importSettings(values);
    })
    .then((result) => {
      if (!result) return;
      flashOK.value = true;
      flash.value = result.skipped.length
        ? t('importSettingsPartial', { count: result.applied, skipped: result.skipped.join(', ') })
        : t('importSettingsDone', { count: result.applied });
      // The form is now describing values that are no longer current.
      void load();
    })
    .catch((failure: unknown) => {
      flashOK.value = false;
      flash.value = failure instanceof ApiError ? failure.message : String(failure);
    });
}

/**
 * The same operation the schedule performs, so somebody who has just changed
 * the policy — or been asked to delete something now — does not have to wait
 * until three in the morning to find out whether it works.
 */
function purge(): void {
  purging.value = true;
  void adminApi.purgeAttachments()
    .then((result) => {
      held.value = result.attachments;
      flashOK.value = true;
      flash.value = t('purgeDone', { count: result.purged });
    })
    .catch((failure: unknown) => {
      flashOK.value = false;
      flash.value = failure instanceof ApiError ? failure.message : String(failure);
    })
    .finally(() => { purging.value = false; });
}

function detectFileType(file: File): string {
  const type = file.type.toLowerCase();
  if (type === 'image/jpeg' || type === 'image/jpg') return 'image/jpeg';
  if (type === 'image/png') return 'image/png';
  if (type === 'image/webp') return 'image/webp';
  if (type === 'image/avif') return 'image/avif';
  const name = file.name.toLowerCase();
  if (name.endsWith('.jpg') || name.endsWith('.jpeg')) return 'image/jpeg';
  if (name.endsWith('.png')) return 'image/png';
  if (name.endsWith('.webp')) return 'image/webp';
  if (name.endsWith('.avif')) return 'image/avif';
  return '';
}

async function uploadFile(variant: string, file: File): Promise<void> {
  const mime = detectFileType(file);
  if (!mime) {
    flashOK.value = false;
    flash.value = t('loginBgFormats');
    return;
  }
  if (file.size > 6 * 1024 * 1024) {
    flashOK.value = false;
    flash.value = t('loginBgFormats');
    return;
  }

  uploadingVariant.value = variant;
  flash.value = '';
  try {
    const base64 = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => {
        const res = String(reader.result ?? '');
        const comma = res.indexOf(',');
        resolve(comma !== -1 ? res.slice(comma + 1) : res);
      };
      reader.onerror = () => reject(new Error('Failed to read file'));
      reader.readAsDataURL(file);
    });

    const res = await adminApi.uploadLoginBackground(variant, mime, base64);
    loginBackgrounds.value = {
      ...loginBackgrounds.value,
      [variant]: res.url,
    };
    if (site.value) {
      site.value = {
        ...site.value,
        login_background: {
          ...(site.value.login_background ?? {}),
          [variant]: res.url,
        },
      };
    }
    flashOK.value = true;
    flash.value = t('loginBgUploaded');
  } catch (failure) {
    flashOK.value = false;
    flash.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    uploadingVariant.value = '';
    dragOverVariant.value = '';
  }
}

function chooseAndUpload(variant: string): void {
  const input = document.createElement('input');
  input.type = 'file';
  input.accept = 'image/png,image/jpeg,image/webp,image/avif,.jpg,.jpeg,.png,.webp,.avif';
  input.onchange = () => {
    const file = input.files?.[0];
    if (file) {
      void uploadFile(variant, file);
    }
  };
  input.click();
}

async function onDrop(variant: string, event: DragEvent): Promise<void> {
  dragOverVariant.value = '';
  const file = event.dataTransfer?.files?.[0];
  if (file) {
    await uploadFile(variant, file);
  }
}

async function clearLoginBg(variant: string): Promise<void> {
  uploadingVariant.value = variant;
  flash.value = '';
  try {
    await adminApi.deleteLoginBackground(variant);
    const updated = { ...loginBackgrounds.value };
    delete updated[variant];
    loginBackgrounds.value = updated;
    if (site.value) {
      const siteBgs = { ...(site.value.login_background ?? {}) };
      delete siteBgs[variant];
      site.value = { ...site.value, login_background: siteBgs };
    }
    flashOK.value = true;
    flash.value = t('loginBgDeleted');
  } catch (failure) {
    flashOK.value = false;
    flash.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    uploadingVariant.value = '';
  }
}

function detectLogoType(file: File): string {
  const type = file.type.toLowerCase();
  if (type === 'image/jpeg' || type === 'image/jpg') return 'image/jpeg';
  if (type === 'image/png') return 'image/png';
  if (type === 'image/webp') return 'image/webp';
  if (type === 'image/avif') return 'image/avif';
  if (type === 'image/gif') return 'image/gif';
  if (type === 'image/x-icon' || type === 'image/vnd.microsoft.icon') return 'image/x-icon';
  if (type === 'image/svg+xml') return 'image/svg+xml';
  const name = file.name.toLowerCase();
  if (name.endsWith('.jpg') || name.endsWith('.jpeg')) return 'image/jpeg';
  if (name.endsWith('.png')) return 'image/png';
  if (name.endsWith('.webp')) return 'image/webp';
  if (name.endsWith('.avif')) return 'image/avif';
  if (name.endsWith('.gif')) return 'image/gif';
  if (name.endsWith('.ico')) return 'image/x-icon';
  if (name.endsWith('.svg')) return 'image/svg+xml';
  return '';
}

async function uploadLogoFile(file: File): Promise<void> {
  const mime = detectLogoType(file);
  if (!mime) {
    flashOK.value = false;
    flash.value = t('siteLogoFormats');
    return;
  }
  if (file.size > 4 * 1024 * 1024) {
    flashOK.value = false;
    flash.value = t('siteLogoFormats');
    return;
  }

  uploadingLogo.value = true;
  flash.value = '';
  try {
    const base64 = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => {
        const res = String(reader.result ?? '');
        const comma = res.indexOf(',');
        resolve(comma !== -1 ? res.slice(comma + 1) : res);
      };
      reader.onerror = () => reject(new Error('Failed to read file'));
      reader.readAsDataURL(file);
    });

    const res = await adminApi.uploadSiteLogo(mime, base64);
    siteLogoUrl.value = res.url;
    if (site.value) {
      site.value = {
        ...site.value,
        logo_url: res.url,
      };
    }
    flashOK.value = true;
    flash.value = t('siteLogoUploaded');
  } catch (failure) {
    flashOK.value = false;
    flash.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    uploadingLogo.value = false;
    dragOverLogo.value = false;
  }
}

function chooseAndUploadLogo(): void {
  const input = document.createElement('input');
  input.type = 'file';
  input.accept = 'image/png,image/jpeg,image/webp,image/svg+xml,image/x-icon,image/avif,image/gif,.jpg,.jpeg,.png,.webp,.svg,.ico,.avif,.gif';
  input.onchange = () => {
    const file = input.files?.[0];
    if (file) {
      void uploadLogoFile(file);
    }
  };
  input.click();
}

async function onDropLogo(event: DragEvent): Promise<void> {
  dragOverLogo.value = false;
  const file = event.dataTransfer?.files?.[0];
  if (file) {
    await uploadLogoFile(file);
  }
}

async function clearSiteLogo(): Promise<void> {
  uploadingLogo.value = true;
  flash.value = '';
  try {
    await adminApi.deleteSiteLogo();
    siteLogoUrl.value = '';
    if (site.value) {
      site.value = {
        ...site.value,
        logo_url: '',
      };
    }
    flashOK.value = true;
    flash.value = t('siteLogoDeleted');
  } catch (failure) {
    flashOK.value = false;
    flash.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    uploadingLogo.value = false;
  }
}

async function load(): Promise<void> {
  error.value = '';
  try {
    // The models come along because one of these settings is which model
    // answers a trial, and a select needs its options.
    const [data, modelsResult] = await Promise.all([adminApi.settings(), adminApi.modelOptions()]);
    const values = data.settings;
    models.value = modelsResult.models;
    held.value = data.attachments ?? { held: 0, bytes: 0 };
    loginBackgrounds.value = data.login_background ?? {};
    siteLogoUrl.value = data.logo_url ?? site.value?.logo_url ?? '';

    form.value = {
      siteName: values['site.name'] ?? '',
      description: values['site.description'] ?? '',
      browserTitle: values['site.browser_title'] ?? '',
      pwaName: values['pwa.name'] ?? '',
      pwaShortName: values['pwa.short_name'] ?? '',
      pwaDescription: values['pwa.description'] ?? '',
      pwaThemeColor: values['pwa.theme_color'] ?? '#18181b',
      pwaBackgroundColor: values['pwa.background_color'] ?? '#18181b',
      pwaIconUrl: values['pwa.icon_url'] ?? '',
      aboutHeading: values['about.title'] ?? '',
      aboutText: values['about.body'] ?? '',
      homeNotice: values['home.notice'] ?? '',
      homeNoticeDismissible: (values['home.notice_dismissible'] ?? 'true') === 'true',
      feedbackShowStaffName: (values['feedback.show_staff_name'] ?? 'true') === 'true',
      landingMode: values['landing.mode'] ?? 'login',
      landingIntro: values['landing.intro'] ?? '',
      trialEnabled: values['landing.trial_enabled'] === 'true',
      trialTurns: Number(values['landing.trial_turns'] ?? 3),
      trialModel: values['landing.trial_model'] ?? '',
      systemPrompt: values['chat.default_system_prompt'] ?? '',
      maxTurns: Number(values['chat.max_turns'] ?? 40),
      agentMaxRounds: Number(values['chat.agent_max_rounds'] ?? 8),
      adminBypass: values['quota.admins_bypass'] === 'true',
      maxConcurrent: values['quota.max_concurrent'] !== undefined ? Number(values['quota.max_concurrent']) : 4,
      usageDisplay: values['quota.usage_display'] ?? 'absolute',
      attachmentMaxMB: Number(values['attachments.max_mb'] ?? 6),
      attachmentRetain: values['attachments.retain'] === 'true',
      purgeAfterDays: Number(values['attachments.purge_after_days'] ?? 0),
      purgeDailyAt: values['attachments.purge_daily_at'] ?? '',
      orphanMinutes: Number(values['attachments.orphan_minutes'] ?? 60),
      apiEnabled: values['api.enabled'] === 'true',
    };
    accept();
  } catch (failure) {
    error.value = failure instanceof Error ? failure.message : String(failure);
  } finally {
    loaded.value = true;
  }
}

const categories: WorkbenchGroup[] = [
  { id: 'site', label: 'controlSite', hint: 'controlSiteHint', icon: IconHome, sections: ['secIdentity', 'secLoginBg', 'secPWA', 'secLanding', 'secAbout', 'secHomeNotice', 'secFeedback'] },
  { id: 'chat', label: 'controlChat', hint: 'controlChatHint', icon: IconSpark, sections: ['secChat', 'secLimits'] },
  { id: 'files', label: 'controlFiles', hint: 'controlFilesHint', icon: IconFile, sections: ['secAttachments', 'secCleanup'] },
  { id: 'integrations', label: 'controlIntegrations', hint: 'controlIntegrationsHint', icon: IconKey, sections: ['apiKeys', 'backupSettings'] },
];

const columns: [string[], string[]] = [['secIdentity', 'secLoginBg', 'secPWA', 'secAbout', 'secChat', 'secAttachments', 'apiKeys'], ['secLanding', 'secHomeNotice', 'secFeedback', 'secLimits', 'secCleanup', 'backupSettings']];

onMounted(load);
</script>

<template>
  <Teleport :to="view.actionsHost">
    <span v-if="loaded && !error" class="oa-control-save-state" :class="{ dirty }" role="status">
      <span class="oa-dashboard-dot" />{{ dirty ? t('controlUnsaved') : t('controlSaved') }}
    </span>
    <button type="button" class="oa-btn primary" :disabled="busy || !loaded || !!error" @click="save">
      {{ saveLabel || t('save') }}
    </button>
  </Teleport>
  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!loaded" class="oa-table-empty">{{ t('loading') }}</p>
  <AdminWorkbench v-else page="settings" :groups="categories" :terms="SEARCH_GROUPS" :columns="columns">
    <template #left="{ visible }">
      <AdminControlCard id="secIdentity" v-show="visible('secIdentity')" :title="t('secIdentity')" :icon="IconHome" :hint="t('controlIdentityHint')">
        <OaTextField v-model="form.siteName" :label="t('siteName')" :hint="t('siteNameHint')" :max-length="60" />
        <OaTextField v-model="form.browserTitle" :label="t('browserTitle')" :hint="t('browserTitleHint')" :max-length="60" />
        <OaTextArea v-model="form.description" :label="t('signInNote')" :rows="2" :hint="t('signInNoteHint')" />
        <div class="oa-field">
          <label class="oa-field-label">{{ t('siteLogo') }}</label>
          <p class="oa-field-hint">{{ t('siteLogoHint') }}</p>
          <div class="oa-logo-setting-row">
            <div
              class="oa-logo-preview"
              :class="{ empty: !siteLogoUrl, dragover: dragOverLogo }"
              tabindex="0"
              role="button"
              :aria-label="t('siteLogo')"
              @dragover.prevent="dragOverLogo = true"
              @dragleave="dragOverLogo = false"
              @drop.prevent="onDropLogo($event)"
              @click="chooseAndUploadLogo"
              @keydown.enter.prevent="chooseAndUploadLogo"
              @keydown.space.prevent="chooseAndUploadLogo"
            >
              <img v-if="siteLogoUrl" :src="siteLogoUrl" class="oa-logo-preview-img" alt="" />
              <div v-else class="oa-logo-empty">
                <IconSpark :size="20" class="oa-logo-empty-icon" />
              </div>
            </div>
            <div class="oa-logo-actions">
              <button
                type="button"
                class="oa-btn small"
                :disabled="uploadingLogo"
                @click="chooseAndUploadLogo"
              >
                {{ siteLogoUrl ? t('siteLogoReplace') : t('siteLogoUpload') }}
              </button>
              <OaConfirmButton
                v-if="siteLogoUrl"
                class="oa-btn small oa-btn-danger"
                :label="t('siteLogoClear')"
                :armed-label="t('siteLogoClearConfirm')"
                :armed-title="t('siteLogoClear')"
                :resting-title="t('siteLogoClear')"
                :disabled="uploadingLogo"
                @confirm="clearSiteLogo"
              />
            </div>
          </div>
        </div>
      </AdminControlCard>
      <AdminControlCard id="secLoginBg" v-show="visible('secLoginBg')" :title="t('secLoginBg')" :icon="IconImage" :hint="t('loginBgHint')">
        <p class="oa-field-hint">{{ t('loginBgFallbackNote') }}</p>
        <div class="oa-login-bg-grid">
          <div
            v-for="v in BG_VARIANTS"
            :key="v.id"
            class="oa-login-bg-card"
            :class="{ 'has-image': !!loginBackgrounds[v.id] }"
          >
            <div class="oa-login-bg-card-head">
              <span class="oa-login-bg-card-title">
                <component :is="v.modeIcon" :size="13" class="oa-login-bg-mode-icon" />
                {{ t(v.label) }}
              </span>
            </div>
            <div
              class="oa-login-bg-preview"
              :class="{
                empty: !loginBackgrounds[v.id],
                portrait: v.id.startsWith('portrait'),
                landscape: v.id.startsWith('landscape'),
                dragover: dragOverVariant === v.id,
              }"
              tabindex="0"
              role="button"
              :aria-label="t(v.label)"
              @dragover.prevent="dragOverVariant = v.id"
              @dragleave="dragOverVariant = ''"
              @drop.prevent="onDrop(v.id, $event)"
              @click="chooseAndUpload(v.id)"
              @keydown.enter.prevent="chooseAndUpload(v.id)"
              @keydown.space.prevent="chooseAndUpload(v.id)"
            >
              <img
                v-if="loginBackgrounds[v.id]"
                :src="loginBackgrounds[v.id]"
                :alt="t(v.label)"
                class="oa-login-bg-img"
              />
              <div v-else class="oa-login-bg-empty">
                <IconImage :size="22" class="oa-login-bg-empty-icon" />
                <span class="oa-login-bg-drop-hint">{{ t('loginBgDropHint') }}</span>
                <span class="oa-login-bg-formats-hint">{{ t('loginBgFormats') }}</span>
              </div>
            </div>
            <div class="oa-login-bg-actions">
              <button
                type="button"
                class="oa-btn small"
                :disabled="uploadingVariant === v.id"
                @click="chooseAndUpload(v.id)"
              >
                {{ loginBackgrounds[v.id] ? t('loginBgReplace') : t('loginBgUpload') }}
              </button>
              <OaConfirmButton
                v-if="loginBackgrounds[v.id]"
                class="oa-btn small oa-btn-danger"
                :label="t('loginBgClear')"
                :armed-label="t('loginBgClearConfirm')"
                :armed-title="t('loginBgClear')"
                :resting-title="t('loginBgClear')"
                :disabled="uploadingVariant === v.id"
                @confirm="clearLoginBg(v.id)"
              />
            </div>
          </div>
        </div>
      </AdminControlCard>
      <AdminControlCard id="secPWA" v-show="visible('secPWA')" :title="t('secPWA')" :icon="IconImage" :hint="t('controlPWAHint')">
        <OaTextField v-model="form.pwaName" :label="t('pwaName')" :hint="t('pwaNameHint')" :max-length="60" />
        <OaTextField v-model="form.pwaShortName" :label="t('pwaShortName')" :hint="t('pwaShortNameHint')" :max-length="32" />
        <OaTextArea v-model="form.pwaDescription" :label="t('pwaDescription')" :rows="2" :hint="t('pwaDescriptionHint')" />
        <OaTextField
          v-model="form.pwaThemeColor"
          :label="t('pwaThemeColor')"
          :hint="t('pwaThemeColorHint')"
          placeholder="#18181b"
          monospace
          :max-length="7"
        />
        <OaTextField
          v-model="form.pwaBackgroundColor"
          :label="t('pwaBackgroundColor')"
          :hint="t('pwaBackgroundColorHint')"
          placeholder="#18181b"
          monospace
          :max-length="7"
        />
        <OaTextField
          v-model="form.pwaIconUrl"
          :label="t('pwaIconUrl')"
          :hint="t('pwaIconUrlHint')"
          placeholder="/icon.png"
          monospace
        />
      </AdminControlCard>
      <AdminControlCard id="secAbout" v-show="visible('secAbout')" :title="t('controlAbout')" :icon="IconInfo" :hint="t('controlAboutHint')">
        <OaTextField
          v-model="form.aboutHeading"
          :label="t('aboutHeading')"
          :hint="t('aboutHeadingHint')"
          :max-length="60"
        />
        <OaTextArea v-model="form.aboutText" :label="t('aboutText')" :rows="5" :hint="t('aboutTextHint')" />
      </AdminControlCard>
      <AdminControlCard id="secChat" v-show="visible('secChat')" :title="t('secChat')" :icon="IconSpark">
        <OaTextArea
          v-model="form.systemPrompt"
          :label="t('instanceSystemPrompt')"
          :rows="4"
          :hint="t('instanceSystemPromptHint')"
        />
        <OaNumberField
          v-model="form.maxTurns"
          :label="t('turnsResent')"
          :min="2"
          :max="200"
          :hint="t('turnsResentHint')"
        />
        <OaNumberField
          v-model="form.agentMaxRounds"
          :label="t('agentMaxRounds')"
          :min="1"
          :max="50"
          :hint="t('agentMaxRoundsHint')"
        />
      </AdminControlCard>
      <AdminControlCard id="secAttachments" v-show="visible('secAttachments')" :title="t('secAttachments')" :icon="IconFile" :hint="t('attachmentsHint')">
        <OaNumberField
          v-model="form.attachmentMaxMB"
          :label="t('attachmentMaxMB')"
          :min="1"
          :max="64"
          :hint="t('attachmentMaxMBHint')"
        />
        <OaSwitchField
          v-model="form.attachmentRetain"
          :label="t('attachmentRetain')"
          :hint="t('attachmentRetainHint')"
        />
      </AdminControlCard>
      <AdminControlCard id="apiKeys" v-show="visible('apiKeys')" :title="t('apiKeys')" :icon="IconKey">
        <OaSwitchField v-model="form.apiEnabled" :label="t('apiEnabled')" :hint="t('apiEnabledHint')" />
      </AdminControlCard>
    </template>
    <template #right="{ visible }">
      <AdminControlCard id="secLanding" v-show="visible('secLanding')" :title="t('secLanding')" :icon="IconSliders" :hint="t('controlLandingHint')">
        <OaSelectField
          v-model="form.landingMode"
          :label="t('landingMode')"
          :hint="t('landingModeHint')"
          :options="[
            { value: 'login', label: t('landingLogin') },
            { value: 'site', label: t('landingSite') },
            { value: 'intro', label: t('landingIntro') },
            { value: 'chat', label: t('landingChat') },
          ]"
        />
        <OaTextArea
          v-if="showIntro"
          v-model="form.landingIntro"
          :label="t('landingIntroHTML')"
          :rows="8"
          :hint="t('landingIntroHTMLHint')"
        />
        <OaSwitchField
          v-if="showTrialSwitch"
          v-model="form.trialEnabled"
          :label="t('trialEnabled')"
          :hint="t('trialEnabledHint')"
        />
        <OaNumberField
          v-if="showTrialDetail"
          v-model="form.trialTurns"
          :label="t('trialTurns')"
          :min="1"
          :max="20"
          :hint="t('trialTurnsHint', { max: 20 })"
        />
        <OaSelectField
          v-if="showTrialDetail"
          v-model="form.trialModel"
          :label="t('trialModel')"
          :options="[
            { value: '', label: t('trialFirstAvailable') },
            ...enabledModels.map((entry) => ({ value: entry.id, label: entry.display_name })),
          ]"
        />
      </AdminControlCard>
      <AdminControlCard id="secHomeNotice" v-show="visible('secHomeNotice')" :title="t('homeNotice')" :icon="IconBell" :hint="t('controlNoticeHint')">
        <OaTextArea v-model="form.homeNotice" :label="t('homeNotice')" :rows="3" :hint="t('homeNoticeHint')" />
        <OaSwitchField
          v-model="form.homeNoticeDismissible"
          :label="t('homeNoticeDismissible')"
          :hint="t('homeNoticeDismissibleHint')"
        />
      </AdminControlCard>
      <AdminControlCard id="secFeedback" v-show="visible('secFeedback')" :title="t('navFeedback')" :icon="IconMessage">
        <OaSwitchField
          v-model="form.feedbackShowStaffName"
          :label="t('feedbackShowStaffName')"
          :hint="t('feedbackShowStaffNameHint')"
        />
      </AdminControlCard>
      <AdminControlCard id="secLimits" v-show="visible('secLimits')" :title="t('secLimits')" :icon="IconSliders">
        <OaSwitchField
          v-model="form.adminBypass"
          :label="t('adminsIgnoreLimits')"
          :hint="t('adminsIgnoreLimitsHint')"
        />
        <OaNumberField
          v-model="form.maxConcurrent"
          :label="t('maxConcurrentPerUser')"
          :min="0"
          :max="100"
          :hint="t('maxConcurrentPerUserHint')"
        />
        <OaSelectField
          v-model="form.usageDisplay"
          :label="t('usageDisplay')"
          :hint="t('usageDisplayHint')"
          :options="[
            { value: 'absolute', label: t('usageDisplayAbsolute') },
            { value: 'remaining', label: t('usageDisplayRemaining') },
            { value: 'used', label: t('usageDisplayUsed') },
          ]"
        />
      </AdminControlCard>
      <AdminControlCard id="secCleanup" v-show="visible('secCleanup')" :title="t('secCleanup')" :icon="IconTrash" :hint="t('cleanupHint')">
        <OaNumberField
          v-model="form.purgeAfterDays"
          :label="t('attachmentPurgeDays')"
          :min="0"
          :max="3650"
          :hint="t('attachmentPurgeDaysHint')"
        />
        <OaTextField
          v-model="form.purgeDailyAt"
          :label="t('attachmentPurgeDaily')"
          placeholder="03:00"
          :hint="t('attachmentPurgeDailyHint')"
          :max-length="5"
        />
        <OaNumberField
          v-model="form.orphanMinutes"
          :label="t('attachmentOrphanMinutes')"
          :min="5"
          :max="1440"
          :hint="t('attachmentOrphanMinutesHint')"
        />
        <!-- The figure matters more than it looks: without it an operator has to
             trust that their cleanup is working rather than watch it work. -->
        <div class="oa-field">
          <p class="oa-field-hint">{{ heldLabel }}</p>
          <OaConfirmButton
            class="oa-btn"
            :label="t('purgeNow')"
            :armed-label="t('purgeNowConfirm')"
            :armed-title="t('purgeNow')"
            :resting-title="t('purgeNow')"
            :disabled="purging"
            @confirm="purge"
          />
        </div>
      </AdminControlCard>
      <AdminControlCard id="backupSettings" v-show="visible('backupSettings')" :title="t('controlBackup')" :icon="IconFile" :hint="t('controlBackupHint')">
        <div class="oa-control-actions">
          <button type="button" class="oa-btn" @click="exportSettings">{{ t('exportSettings') }}</button>
          <button type="button" class="oa-btn" @click="importSettings">{{ t('importSettings') }}</button>
        </div>
      </AdminControlCard>
    </template>
  </AdminWorkbench>
  <p v-if="flash" class="oa-drawer-flash visible oa-control-flash" :class="{ ok: flashOK }" role="status">{{ flash }}</p>
</template>
