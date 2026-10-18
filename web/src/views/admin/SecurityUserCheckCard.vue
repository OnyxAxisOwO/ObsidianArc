<script setup lang="ts">
// The address screening service a sign-up's email is checked against. Like
// the mail card beside it, its own endpoint and its own Save, so it neither
// waits on the page's settings form nor is reverted by it.

import { computed, onMounted, ref } from 'vue';
import { adminApi, type AdminUserCheckSettings } from '@/admin/api';
import { ApiError } from '@/api/client';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTextArea from '@/components/OaTextArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t, type StringKey } from '@/composables/useI18n';
import { IconShield } from '@/icons';
import AdminControlCard from './AdminControlCard.vue';
import AdminFailure from './AdminFailure.vue';

const loaded = ref(false);
const loadError = ref<StringKey | null>(null);
const busy = ref(false);
const testBusy = ref(false);
const flash = ref<StringKey | null>(null);
const flashOK = ref(false);
const testEmail = ref('');
const saved = ref<AdminUserCheckSettings>({
  enabled: false, exempt_domains: [], failure_mode: 'reject', api_key_set: false,
});
const form = ref({
  enabled: false, exempt_domains: '', failure_mode: 'reject' as 'allow' | 'reject', api_key_set: false, api_key: '',
});
// Computed so a render does not hand the select a new array, and so the
// labels follow a change of language.
const failureModeChoices = computed(() => [
  { value: 'reject' as const, label: t('userCheckFailureReject') },
  { value: 'allow' as const, label: t('userCheckFailureAllow') },
]);

const ERRORS: Record<string, StringKey> = {
  usercheck_test_cooldown: 'userCheckTestCooldown',
  unavailable: 'emailScreeningUnavailable',
  email_screening_unavailable: 'emailScreeningUnavailable',
};

function errorText(failure: unknown, action: 'load' | 'save' | 'test'): StringKey {
  if (!(failure instanceof ApiError)) return 'failed';
  if (Object.hasOwn(ERRORS, failure.code)) return ERRORS[failure.code]!;
  if (failure.status === 400) return action === 'test' ? 'userCheckTestInvalid' : 'userCheckSettingsInvalid';
  return 'failed';
}

function adopt(settings: AdminUserCheckSettings): void {
  saved.value = settings;
  form.value = {
    enabled: settings.enabled,
    // The server sends null for an empty list; the form still needs a string.
    exempt_domains: (settings.exempt_domains ?? []).join('\n'),
    failure_mode: settings.failure_mode,
    api_key_set: settings.api_key_set,
    api_key: '',
  };
}

function domains(): string[] {
  return [...new Set(form.value.exempt_domains
    .split(/[\n,]/)
    .map((domain) => domain.trim().toLowerCase())
    .filter(Boolean))];
}

async function load(): Promise<void> {
  loadError.value = null;
  loaded.value = false;
  try {
    adopt(await adminApi.userCheck());
    loaded.value = true;
  } catch (failure) {
    loadError.value = errorText(failure, 'load');
  }
}

async function save(): Promise<void> {
  if (!loaded.value || busy.value) return;
  if (form.value.enabled && !form.value.api_key_set && !form.value.api_key.trim()) {
    flash.value = 'userCheckAPIKeyRequired';
    flashOK.value = false;
    return;
  }
  busy.value = true;
  flash.value = null;
  flashOK.value = false;
  try {
    adopt(await adminApi.saveUserCheck({
      enabled: form.value.enabled,
      exempt_domains: domains(),
      failure_mode: form.value.failure_mode,
      api_key: form.value.api_key,
      clear_api_key: false,
    }));
    flash.value = 'userCheckSaved';
    flashOK.value = true;
  } catch (failure) {
    flash.value = errorText(failure, 'save');
  } finally {
    busy.value = false;
  }
}

async function clearAPIKey(): Promise<void> {
  if (!loaded.value || busy.value || !saved.value.api_key_set) return;
  busy.value = true;
  flash.value = null;
  flashOK.value = false;
  try {
    const settings = await adminApi.saveUserCheck({
      exempt_domains: saved.value.exempt_domains,
      failure_mode: saved.value.failure_mode,
      enabled: false,
      api_key: '',
      clear_api_key: true,
    });
    saved.value = settings;
    form.value.enabled = false;
    form.value.api_key_set = false;
    form.value.api_key = '';
    flash.value = 'userCheckAPIKeyCleared';
    flashOK.value = true;
  } catch (failure) {
    flash.value = errorText(failure, 'save');
  } finally {
    busy.value = false;
  }
}

async function sendTest(): Promise<void> {
  // Not while a save or a key clearing is in flight, as the mail card's test
  // waits too: the answer would be about a key that may be about to change.
  if (busy.value || testBusy.value || !testEmail.value.trim() || !form.value.api_key_set) return;
  testBusy.value = true;
  flash.value = null;
  flashOK.value = false;
  try {
    const result = await adminApi.testUserCheck(testEmail.value.trim());
    flash.value = result.skipped
      ? 'userCheckTestSkipped'
      : result.disposable ? 'userCheckTestDisposable' : 'userCheckTestClean';
    flashOK.value = !result.disposable;
  } catch (failure) {
    flash.value = errorText(failure, 'test');
  } finally {
    testBusy.value = false;
  }
}

onMounted(load);
</script>

<template>
  <AdminControlCard id="secUserCheck" :title="t('userCheckSettings')" :icon="IconShield" :hint="t('userCheckSettingsHint')">
    <AdminFailure v-if="loadError" :message="t(loadError)" @retry="load" />
    <p v-else-if="!loaded" class="oa-table-empty" role="status">{{ t('loading') }}</p>
    <template v-else>
      <OaSwitchField
        v-model="form.enabled"
        :label="t('userCheckEnabled')"
        :hint="t('userCheckEnabledHint')"
      />
      <OaTextField
        v-model="form.api_key"
        type="password"
        :label="t('userCheckAPIKey')"
        :placeholder="form.api_key_set ? t('userCheckAPIKeyKeepPlaceholder') : t('userCheckAPIKeyPlaceholder')"
        :hint="form.api_key_set ? t('userCheckAPIKeySetHint') : t('userCheckAPIKeyMissingHint')"
        autocomplete="new-password"
      />
      <div v-if="form.api_key_set" class="oa-control-actions">
        <OaConfirmButton
          class="oa-btn oa-btn-danger"
          :label="t('userCheckClearAPIKey')"
          :armed-label="t('userCheckClearAPIKeyConfirm')"
          :armed-title="t('userCheckClearAPIKeyConfirm')"
          :disabled="busy"
          @confirm="clearAPIKey"
        />
      </div>
      <OaTextArea
        v-model="form.exempt_domains"
        :label="t('userCheckExemptDomains')"
        :hint="t('userCheckExemptDomainsHint')"
        :placeholder="t('userCheckExemptDomainsPlaceholder')"
        :rows="5"
      />
      <OaSelectField
        v-model="form.failure_mode"
        :label="t('userCheckFailureMode')"
        :hint="t('userCheckFailureModeHint')"
        :options="failureModeChoices"
      />
      <div class="oa-control-actions">
        <button type="button" class="oa-btn primary" :disabled="busy" @click="save">
          {{ busy ? t('saving') : t('save') }}
        </button>
      </div>
      <OaTextField
        v-model="testEmail"
        :label="t('userCheckTestEmail')"
        type="email"
        autocomplete="email"
        :hint="t('userCheckTestHint')"
      />
      <div class="oa-control-actions">
        <button
          type="button"
          class="oa-btn"
          :disabled="busy || testBusy || !testEmail.trim() || !form.api_key_set"
          @click="sendTest"
        >{{ testBusy ? t('checking') : t('userCheckTest') }}</button>
      </div>
      <p
        v-if="flash"
        class="oa-drawer-flash visible oa-control-flash"
        :class="{ ok: flashOK }"
        :role="flashOK ? 'status' : 'alert'"
      >{{ t(flash) }}</p>
    </template>
  </AdminControlCard>
</template>
