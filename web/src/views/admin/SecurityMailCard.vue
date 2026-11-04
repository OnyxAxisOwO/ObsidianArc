<script setup lang="ts">
// Outgoing mail, on the security page because verification links and
// new-device notices are what it carries. Its own endpoint and its own Save:
// the settings form beside it never sends these values, so nothing here waits
// on that form or is reverted by it.

import { computed, onMounted, ref } from 'vue';
import { adminApi, type AdminMailSettings } from '@/admin/api';
import { ApiError } from '@/api/client';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaNumberField from '@/components/OaNumberField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t, type StringKey } from '@/composables/useI18n';
import { IconMessage } from '@/icons';
import { isSuperAdmin } from '@/stores/session';
import AdminControlCard from './AdminControlCard.vue';
import AdminFailure from './AdminFailure.vue';

// The page decides whether the switches that depend on mail are live; a save
// here is what changes that answer, so the card reports it.
const emit = defineEmits<{ configured: [value: boolean] }>();

const loaded = ref(false);
const loadError = ref<StringKey | null>(null);
const busy = ref(false);
const testBusy = ref(false);
const flash = ref<StringKey | null>(null);
const flashOK = ref(false);
const testTo = ref('');
const saved = ref<AdminMailSettings>({
  host: '', port: 587, username: '', from: '', implicit_tls: false, public_url: '', password_set: false,
});
const form = ref({ ...saved.value, password: '' });
const dirty = computed(() => {
  const draft = form.value;
  const stored = saved.value;
  return draft.host.trim() !== stored.host || (draft.port ?? 587) !== stored.port ||
    draft.username.trim() !== stored.username || draft.from.trim() !== stored.from ||
    draft.implicit_tls !== stored.implicit_tls || draft.public_url.trim() !== stored.public_url ||
    draft.password !== '';
});

const ERRORS: Record<string, StringKey> = {
  mail_test_cooldown: 'mailTestCooldown',
  mail_unavailable: 'mailDeliveryUnavailable',
  mail_password_needed: 'mailPasswordNeeded',
};

function errorText(failure: unknown, action: 'load' | 'save' | 'test'): StringKey {
  if (!(failure instanceof ApiError)) return 'failed';
  if (Object.hasOwn(ERRORS, failure.code)) return ERRORS[failure.code]!;
  if (failure.status === 400) return action === 'test' ? 'mailTestInvalid' : 'mailSettingsInvalid';
  return 'failed';
}

async function load(): Promise<void> {
  loadError.value = null;
  loaded.value = false;
  try {
    const settings = await adminApi.mail();
    saved.value = settings;
    form.value = { ...settings, password: '' };
    loaded.value = true;
  } catch (failure) {
    loadError.value = errorText(failure, 'load');
  }
}

async function save(): Promise<void> {
  if (!loaded.value || busy.value) return;
  busy.value = true;
  flash.value = null;
  flashOK.value = false;
  try {
    const settings = await adminApi.saveMail({
      host: form.value.host.trim(),
      port: form.value.port ?? 587,
      username: form.value.username.trim(),
      from: form.value.from.trim(),
      implicit_tls: form.value.implicit_tls,
      public_url: form.value.public_url.trim(),
      // Empty keeps the existing credential; only the explicit action below clears it.
      password: form.value.password,
      clear_password: false,
    });
    saved.value = settings;
    form.value = { ...settings, password: '' };
    emit('configured', !!(settings.host && settings.port &&
      (settings.from || settings.username.includes('@')) && settings.public_url));
    flash.value = 'mailSaved';
    flashOK.value = true;
  } catch (failure) {
    flash.value = errorText(failure, 'save');
  } finally {
    busy.value = false;
  }
}

async function clearPassword(): Promise<void> {
  if (!loaded.value || busy.value || !saved.value.password_set) return;
  busy.value = true;
  flash.value = null;
  flashOK.value = false;
  try {
    const settings = await adminApi.saveMail({
      host: saved.value.host,
      port: saved.value.port,
      username: saved.value.username,
      from: saved.value.from,
      implicit_tls: saved.value.implicit_tls,
      public_url: saved.value.public_url,
      password: '',
      clear_password: true,
    });
    saved.value = settings;
    form.value.password_set = settings.password_set;
    form.value.password = '';
    flash.value = 'mailPasswordCleared';
    flashOK.value = true;
  } catch (failure) {
    flash.value = errorText(failure, 'save');
  } finally {
    busy.value = false;
  }
}

async function sendTest(): Promise<void> {
  if (busy.value || testBusy.value || dirty.value || !testTo.value.trim()) return;
  testBusy.value = true;
  flash.value = null;
  flashOK.value = false;
  try {
    await adminApi.testMail(testTo.value.trim());
    flash.value = 'mailTestSent';
    flashOK.value = true;
  } catch (failure) {
    flash.value = errorText(failure, 'test');
  } finally {
    testBusy.value = false;
  }
}

onMounted(load);
</script>

<template>
  <AdminControlCard id="secMail" :title="t('mailSettings')" :icon="IconMessage" :hint="t('mailSettingsHint')">
    <AdminFailure v-if="loadError" :message="t(loadError)" @retry="load" />
    <p v-else-if="!loaded" class="oa-table-empty" role="status">{{ t('loading') }}</p>
    <template v-else>
      <template v-if="isSuperAdmin">
        <OaTextField v-model="form.host" :label="t('mailHost')" autocomplete="off" />
        <OaNumberField v-model="form.port" :label="t('mailPort')" :min="1" :max="65535" />
        <OaTextField v-model="form.username" :label="t('mailUsername')" autocomplete="username" />
      </template>
      <!-- Shown as text rather than hidden, as the public URL below is: the relay mail goes through is the same kind of decision, and a security administrator still sees where it points. -->
      <p v-else class="oa-field-hint">{{ t('mailTransportLocked', { server: form.host ? form.host + ':' + form.port : '—' }) }}</p>
      <OaTextField v-model="form.from" :label="t('mailFrom')" autocomplete="email" />
      <OaSwitchField v-model="form.implicit_tls" :label="t('mailImplicitTLS')" :hint="t('mailImplicitTLSHint')" />
      <OaTextField
        v-if="isSuperAdmin"
        v-model="form.public_url"
        :label="t('mailPublicURL')"
        :hint="t('mailPublicURLHint')"
        autocomplete="url"
      />
      <!-- Shown rather than hidden: a security administrator can still see where mailed links point, and the save sends the value back unchanged, which the server accepts. -->
      <p v-else class="oa-field-hint">{{ t('mailPublicURLLocked', { url: form.public_url || '—' }) }}</p>
      <OaTextField
        v-model="form.password"
        type="password"
        :label="t('mailPassword')"
        :placeholder="form.password_set ? t('mailPasswordKeepPlaceholder') : t('mailPasswordPlaceholder')"
        :hint="form.password_set ? t('mailPasswordSetHint') : t('mailPasswordHint')"
        autocomplete="new-password"
      />
      <div class="oa-control-actions">
        <OaConfirmButton
          v-if="form.password_set"
          class="oa-btn oa-btn-danger"
          :label="t('mailClearPassword')"
          :armed-label="t('mailClearPasswordConfirm')"
          :armed-title="t('mailClearPasswordConfirm')"
          :disabled="busy"
          @confirm="clearPassword"
        />
        <button type="button" class="oa-btn primary" :disabled="busy" @click="save">
          {{ busy ? t('saving') : t('save') }}
        </button>
      </div>
      <OaTextField
        v-model="testTo"
        :label="t('mailTestTo')"
        :hint="dirty ? t('mailTestSaveFirst') : undefined"
        type="email"
        autocomplete="email"
      />
      <div class="oa-control-actions">
        <button type="button" class="oa-btn" :disabled="busy || testBusy || dirty || !testTo.trim()" @click="sendTest">
          {{ testBusy ? t('sending') : t('mailTestSend') }}
        </button>
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
