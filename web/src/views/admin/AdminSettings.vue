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
import OaFormSection from '@/components/OaFormSection.vue';
import OaSearchField from '@/components/OaSearchField.vue';
import { matchesSearch } from '@/lib/search';
import OaNumberField from '@/components/OaNumberField.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTextArea from '@/components/OaTextArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t, type StringKey } from '@/composables/useI18n';
import { formatBytes } from '@/lib/format';
import AdminFailure from './AdminFailure.vue';
import { useAdminView } from './adminView';

const view = useAdminView();
view.setTitle(t('adminSettingsTitle'));

const query = ref('');
const SEARCH_GROUPS = {
  secIdentity: [
    'secIdentity', 'siteName', 'siteNameHint', 'signInNote', 'signInNoteHint', 'aboutHeading',
    'aboutHeadingHint', 'aboutText', 'aboutTextHint', 'homeNotice', 'homeNoticeHint', 'homeNoticeDismissible',
    'homeNoticeDismissibleHint',
  ],
  secLanding: [
    'secLanding', 'landingMode', 'landingModeHint', 'landingLogin', 'landingIntro', 'landingChat',
    'landingIntroHTML', 'landingIntroHTMLHint', 'trialEnabled', 'trialEnabledHint', 'trialTurns',
    'trialTurnsHint', 'trialModel', 'trialFirstAvailable',
  ],
  secChat: [
    'secChat', 'instanceSystemPrompt', 'instanceSystemPromptHint', 'turnsResent', 'turnsResentHint',
  ],
  secLimits: [
    'secLimits', 'adminsIgnoreLimits', 'adminsIgnoreLimitsHint', 'usageDisplay', 'usageDisplayHint',
    'usageDisplayAbsolute', 'usageDisplayRemaining', 'usageDisplayUsed',
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
const visibleGroups = computed(() => Object.fromEntries(
  Object.entries(SEARCH_GROUPS).map(([group, terms]) => [group, matchesSearch(query.value, ...terms.map((key) => t(key)))]),
));

const error = ref('');
const loaded = ref(false);
const models = ref<AdminModel[]>([]);
const held = ref<HeldAttachments>({ held: 0, bytes: 0 });
const flash = ref('');
const saveLabel = ref('');
const busy = ref(false);
const purging = ref(false);

const form = ref({
  siteName: '',
  description: '',
  aboutHeading: '',
  aboutText: '',
  homeNotice: '',
  homeNoticeDismissible: true,
  landingMode: 'login',
  landingIntro: '',
  trialEnabled: false,
  trialTurns: 3 as number | null,
  trialModel: '',
  systemPrompt: '',
  maxTurns: 40 as number | null,
  adminBypass: false,
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
    'about.title': form.value.aboutHeading.trim(),
    'about.body': form.value.aboutText.trim(),
    'home.notice': form.value.homeNotice.trim(),
    'home.notice_dismissible': String(form.value.homeNoticeDismissible),
    'landing.mode': form.value.landingMode,
    'landing.intro': form.value.landingIntro.trim(),
    'landing.trial_enabled': String(form.value.trialEnabled),
    'landing.trial_turns': String(form.value.trialTurns ?? 3),
    'landing.trial_model': form.value.trialModel,
    'quota.admins_bypass': String(form.value.adminBypass),
    'quota.usage_display': form.value.usageDisplay,
    'chat.default_system_prompt': form.value.systemPrompt.trim(),
    'chat.max_turns': String(form.value.maxTurns ?? 40),
    'api.enabled': String(form.value.apiEnabled),
    'attachments.max_mb': String(form.value.attachmentMaxMB ?? 6),
    'attachments.retain': String(form.value.attachmentRetain),
    'attachments.purge_after_days': String(form.value.purgeAfterDays ?? 0),
    'attachments.purge_daily_at': form.value.purgeDailyAt.trim(),
    'attachments.orphan_minutes': String(form.value.orphanMinutes ?? 60),
  };
}

async function save(): Promise<void> {
  busy.value = true;
  saveLabel.value = t('saving');
  flash.value = '';
  try {
    await adminApi.saveSettings(collect());
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
      flash.value = result.skipped.length
        ? t('importSettingsPartial', { count: result.applied, skipped: result.skipped.join(', ') })
        : t('importSettingsDone', { count: result.applied });
      // The form is now describing values that are no longer current.
      void load();
    })
    .catch((failure: unknown) => {
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
      flash.value = t('purgeDone', { count: result.purged });
    })
    .catch((failure: unknown) => {
      flash.value = failure instanceof ApiError ? failure.message : String(failure);
    })
    .finally(() => { purging.value = false; });
}

async function load(): Promise<void> {
  error.value = '';
  try {
    // The models come along because one of these settings is which model
    // answers a trial, and a select needs its options.
    const [data, modelsResult] = await Promise.all([adminApi.settings(), adminApi.models()]);
    const values = data.settings;
    models.value = modelsResult.models;
    held.value = data.attachments;

    form.value = {
      siteName: values['site.name'] ?? '',
      description: values['site.description'] ?? '',
      aboutHeading: values['about.title'] ?? '',
      aboutText: values['about.body'] ?? '',
      homeNotice: values['home.notice'] ?? '',
      homeNoticeDismissible: (values['home.notice_dismissible'] ?? 'true') === 'true',
      landingMode: values['landing.mode'] ?? 'login',
      landingIntro: values['landing.intro'] ?? '',
      trialEnabled: values['landing.trial_enabled'] === 'true',
      trialTurns: Number(values['landing.trial_turns'] ?? 3),
      trialModel: values['landing.trial_model'] ?? '',
      systemPrompt: values['chat.default_system_prompt'] ?? '',
      maxTurns: Number(values['chat.max_turns'] ?? 40),
      adminBypass: values['quota.admins_bypass'] === 'true',
      usageDisplay: values['quota.usage_display'] ?? 'absolute',
      attachmentMaxMB: Number(values['attachments.max_mb'] ?? 6),
      attachmentRetain: values['attachments.retain'] === 'true',
      purgeAfterDays: Number(values['attachments.purge_after_days'] ?? 0),
      purgeDailyAt: values['attachments.purge_daily_at'] ?? '',
      orphanMinutes: Number(values['attachments.orphan_minutes'] ?? 60),
      apiEnabled: values['api.enabled'] === 'true',
    };
  } catch (failure) {
    error.value = failure instanceof Error ? failure.message : String(failure);
  } finally {
    loaded.value = true;
  }
}

onMounted(load);
</script>

<template>
  <Teleport :to="view.actionsHost">
    <button type="button" class="oa-btn" @click="exportSettings">{{ t('exportSettings') }}</button>
    <button type="button" class="oa-btn" @click="importSettings">{{ t('importSettings') }}</button>
    <button type="button" class="oa-btn primary" :disabled="busy" @click="save">
      {{ saveLabel || t('save') }}
    </button>
  </Teleport>

  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!loaded" class="oa-table-empty">{{ t('loading') }}</p>

  <div v-else class="oa-drawer-body" style="padding: 0; overflow: visible">
    <OaSearchField v-model="query" :label="t('searchSettings')" />
    <p v-if="!Object.values(visibleGroups).some(Boolean)" class="oa-search-empty" role="status">{{ t('noSearchResults') }}</p>

    <section v-show="visibleGroups.secIdentity" class="oa-settings-group">
      <OaFormSection :title="t('secIdentity')" />
      <OaTextField v-model="form.siteName" :label="t('siteName')" :hint="t('siteNameHint')" :max-length="60" />
      <OaTextArea v-model="form.description" :label="t('signInNote')" :rows="2" :hint="t('signInNoteHint')" />
      <OaTextField
        v-model="form.aboutHeading"
        :label="t('aboutHeading')"
        :hint="t('aboutHeadingHint')"
        :max-length="60"
      />
      <OaTextArea v-model="form.aboutText" :label="t('aboutText')" :rows="4" :hint="t('aboutTextHint')" />
      <OaTextArea v-model="form.homeNotice" :label="t('homeNotice')" :rows="3" :hint="t('homeNoticeHint')" />
      <OaSwitchField
        v-model="form.homeNoticeDismissible"
        :label="t('homeNoticeDismissible')"
        :hint="t('homeNoticeDismissibleHint')"
      />
    </section>

    <section v-show="visibleGroups.secLanding" class="oa-settings-group">
      <OaFormSection :title="t('secLanding')" />
      <OaSelectField
        v-model="form.landingMode"
        :label="t('landingMode')"
        :hint="t('landingModeHint')"
        :options="[
          { value: 'login', label: t('landingLogin') },
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
    </section>

    <section v-show="visibleGroups.secChat" class="oa-settings-group">
      <OaFormSection :title="t('secChat')" />
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
    </section>

    <section v-show="visibleGroups.secLimits" class="oa-settings-group">
      <OaFormSection :title="t('secLimits')" />
      <OaSwitchField
        v-model="form.adminBypass"
        :label="t('adminsIgnoreLimits')"
        :hint="t('adminsIgnoreLimitsHint')"
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
    </section>

    <section v-show="visibleGroups.secAttachments" class="oa-settings-group">
      <OaFormSection :title="t('secAttachments')" :hint="t('attachmentsHint')" />
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
    </section>

    <section v-show="visibleGroups.secCleanup" class="oa-settings-group">
      <OaFormSection :title="t('secCleanup')" :hint="t('cleanupHint')" />
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
    </section>

    <section v-show="visibleGroups.apiKeys" class="oa-settings-group">
      <OaFormSection :title="t('apiKeys')" />
      <OaSwitchField v-model="form.apiEnabled" :label="t('apiEnabled')" :hint="t('apiEnabledHint')" />
    </section>

    <p class="oa-drawer-flash" :class="{ visible: !!flash }">{{ flash }}</p>
  </div>
</template>
