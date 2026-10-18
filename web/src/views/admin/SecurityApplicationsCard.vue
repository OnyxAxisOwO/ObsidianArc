<script setup lang="ts">
// The applications that may sign people in with an account here.
//
// Not a settings card: these are rows rather than fields, and they commit on
// their own buttons. They live on the security page anyway because they are
// the same subject as everything else on it — who gets in, and how.

import { onMounted, ref } from 'vue';
import { adminApi, type SignInApplication } from '@/admin/api';
import { ApiError } from '@/api/client';
import OaBadge from '@/components/OaBadge.vue';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTextArea from '@/components/OaTextArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t } from '@/composables/useI18n';
import { copyToClipboard } from '@/chat/markdown';
import { IconCheck, IconCopy, IconKey } from '@/icons';
import { initials } from '@/lib/account';
import { maskCredential } from '@/admin/safeMode';
import { isSuperAdmin } from '@/stores/session';
import AdminControlCard from './AdminControlCard.vue';
import { failureText } from './shared';
import { FEEDBACK_FLASH_MS } from '@/lib/flash';

// The server's own address arrives with this list, and the sign-in providers'
// callback hints on the page are built from it (see callbackURL there), so
// the card hands it up rather than the page asking for the list a second time.
const emit = defineEmits<{ issuer: [value: string] }>();

const applications = ref<SignInApplication[]>([]);
// Without it the empty-list line would show until the first answer, telling
// an operator who has registered applications that there are none.
const loaded = ref(false);
const issuer = ref('');
const flash = ref('');
const busy = ref(false);
/** The client secret, for the one moment it exists. */
const freshSecret = ref('');
const draft = ref({ name: '', description: '', redirects: '', public: false, trusted: false });
/** The value that was last copied, so the control can say it landed. */
const copied = ref('');

function failed(failure: unknown): void {
  flash.value = failureText(failure);
}

function copy(value: string): void {
  void copyToClipboard(value).then((ok) => {
    if (!ok) return;
    copied.value = value;
    window.setTimeout(() => {
      if (copied.value === value) copied.value = '';
    }, FEEDBACK_FLASH_MS);
  });
}

async function load(): Promise<void> {
  try {
    const result = await adminApi.applications();
    applications.value = result.applications ?? [];
    issuer.value = result.issuer;
    emit('issuer', result.issuer);
  } catch (failure) {
    failed(failure);
  } finally {
    loaded.value = true;
  }
}

function register(): void {
  if (busy.value) return;
  busy.value = true;
  flash.value = '';
  freshSecret.value = '';
  void adminApi.createApplication({
    name: draft.value.name.trim(),
    description: draft.value.description.trim(),
    redirect_uris: draft.value.redirects,
    public: draft.value.public,
    trusted: draft.value.trusted,
  })
    .then((result) => {
      freshSecret.value = result.client_secret;
      draft.value = { name: '', description: '', redirects: '', public: false, trusted: false };
      return load();
    })
    .catch((failure: unknown) => {
      if (failure instanceof ApiError && failure.code === 'too_many_applications') {
        flash.value = t('applicationsFull');
        return;
      }
      failed(failure);
    })
    .finally(() => { busy.value = false; });
}

function toggle(app: SignInApplication, disabled: boolean): void {
  busy.value = true;
  flash.value = '';
  void adminApi.updateApplication(app.id, { disabled })
    .then(load)
    .catch(failed)
    .finally(() => { busy.value = false; });
}

function rotate(app: SignInApplication): void {
  busy.value = true;
  flash.value = '';
  freshSecret.value = '';
  void adminApi.rotateApplicationSecret(app.id)
    .then((result) => { freshSecret.value = result.client_secret; })
    .catch(failed)
    .finally(() => { busy.value = false; });
}

function remove(app: SignInApplication): void {
  busy.value = true;
  flash.value = '';
  void adminApi.deleteApplication(app.id)
    .then(load)
    .catch(failed)
    .finally(() => { busy.value = false; });
}

/**
 * A trusted application is the super administrator's to change. The server
 * refuses anybody else; the controls are disabled so the refusal is not what
 * a security administrator learns first.
 */
function locked(app: SignInApplication): boolean {
  return app.trusted && !isSuperAdmin.value;
}

onMounted(load);
</script>

<template>
  <AdminControlCard id="secApplications" :title="t('secApplications')" :icon="IconKey" :hint="t('applicationsHint')" class="oa-control-card-wide">
    <p class="oa-field-hint">{{ t('applicationsIssuer', { issuer }) }}</p>
    <p v-if="!isSuperAdmin && applications.some((app) => app.trusted)" class="oa-field-hint">{{ t('applicationTrustedLocked') }}</p>

    <p v-if="!loaded" class="oa-table-empty" role="status">{{ t('loading') }}</p>
    <p v-else-if="!applications.length" class="oa-table-empty">{{ t('applicationsEmpty') }}</p>
    <div v-else class="oa-apps">
      <div v-for="app in applications" :key="app.id" class="oa-app" :class="{ off: app.disabled }">
        <!-- Its initials, the same mark the consent screen draws it with,
             so a row here and the card a stranger sees are the same thing. -->
        <span class="oa-app-mark">{{ initials(app.name) }}</span>
        <div class="oa-app-body">
          <div class="oa-app-head">
            <span class="oa-app-name">{{ app.name }}</span>
            <OaBadge v-if="app.disabled" tone="danger">{{ t('disabled') }}</OaBadge>
            <OaBadge v-if="app.trusted" tone="muted">{{ t('applicationTrusted') }}</OaBadge>
            <OaBadge v-if="!app.confidential" tone="muted">{{ t('applicationPublic') }}</OaBadge>
          </div>
          <p v-if="app.description" class="oa-app-desc">{{ app.description }}</p>
          <!-- The client id is the one value an operator has to move to
               another program by hand, so the row is the copy control. -->
          <button type="button" class="oa-app-id mono" :title="t('copy')" @click="copy(app.client_id)">
            <span>{{ app.client_id }}</span>
            <IconCheck v-if="copied === app.client_id" :size="12" />
            <IconCopy v-else :size="12" />
          </button>
          <div class="oa-app-uris">
            <span v-for="uri in app.redirect_uris" :key="uri" class="oa-app-uri">{{ uri }}</span>
          </div>
        </div>
        <div class="oa-app-actions">
          <button type="button" class="oa-btn" :disabled="busy || locked(app)" @click="toggle(app, !app.disabled)">
            {{ app.disabled ? t('enable') : t('disable') }}
          </button>
          <OaConfirmButton
            v-if="app.confidential"
            class="oa-btn"
            :label="t('applicationRotate')"
            :armed-label="t('confirmWord')"
            :armed-title="t('applicationRotateConfirm', { application: app.name })"
            :resting-title="t('applicationRotate')"
            :disabled="busy || locked(app)"
            @confirm="rotate(app)"
          />
          <OaConfirmButton
            class="oa-btn"
            :label="t('deleteLabel')"
            :armed-label="t('confirmWord')"
            :armed-title="t('applicationDeleteConfirm', { application: app.name })"
            :resting-title="t('deleteLabel')"
            :disabled="busy || locked(app)"
            @confirm="remove(app)"
          />
        </div>
      </div>
    </div>

    <!-- The one moment this value exists. It gets a surface of its own
         rather than a line of hint text, because everything else on this
         screen can be read again tomorrow and this cannot. -->
    <div v-if="freshSecret" class="oa-app-secret">
      <span class="oa-app-secret-label">{{ t('applicationSecretOnce') }}</span>
      <button type="button" class="oa-app-secret-value mono" :title="t('copy')" @click="copy(freshSecret)">
        <span>{{ maskCredential(freshSecret) }}</span>
        <IconCheck v-if="copied === freshSecret" :size="13" />
        <IconCopy v-else :size="13" />
      </button>
    </div>

    <h3 class="oa-app-form-title">{{ t('applicationRegister') }}</h3>
    <OaTextField v-model="draft.name" :label="t('applicationName')" :hint="t('applicationNameHint')" />
    <OaTextField v-model="draft.description" :label="t('applicationDescription')" :hint="t('applicationDescriptionHint')" />
    <OaTextArea
      v-model="draft.redirects"
      :label="t('applicationRedirects')"
      :rows="2"
      placeholder="https://wiki.example.com/oidc/callback"
      :hint="t('applicationRedirectsHint')"
    />
    <OaSwitchField v-model="draft.public" :label="t('applicationPublicField')" :hint="t('applicationPublicHint')" />
    <OaSwitchField v-if="isSuperAdmin" v-model="draft.trusted" :label="t('applicationTrustedField')" :hint="t('applicationTrustedHint')" />
    <p v-else class="oa-field-hint">{{ t('applicationTrustedSuperAdminOnly') }}</p>
    <div class="oa-button-row">
      <button type="button" class="oa-btn primary" :disabled="busy" @click="register">
        {{ t('applicationRegister') }}
      </button>
    </div>
    <p class="oa-drawer-flash" :class="{ visible: !!flash }">{{ flash }}</p>
  </AdminControlCard>
</template>
