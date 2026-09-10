<script setup lang="ts">
// Who this account is, what its password is, and how to take its data out.
//
// Each part commits on its own button. There is no Save at the bottom of the
// settings panel, because one would be claiming to commit things it has
// nothing to do with.

import { ref } from 'vue';
import { changePassword, updateProfile } from '@/api/auth';
import { exportAccount, importAccount, pickJSONFile, saveAsFile } from '@/api/backup';
import { ApiError } from '@/api/client';
import OaTextArea from '@/components/OaTextArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t } from '@/composables/useI18n';
import { adopt, currentPreferences, currentUser, requireUser } from '@/stores/session';
import { matchesSettings } from './search';

const props = withDefaults(defineProps<{ query?: string }>(), { query: '' });

const account = requireUser();

const nickname = ref(account.nickname);
const email = ref(account.email);
const qq = ref(account.qq ?? '');
const bio = ref(account.bio);
const avatar = ref(account.avatar);

const profileFlash = ref('');
const profileBusy = ref(false);
const profileLabel = ref('');

const currentPassword = ref('');
const newPassword = ref('');
const passwordFlash = ref('');
const passwordBusy = ref(false);
const passwordLabel = ref('');

const dataStatus = ref('');
const exporting = ref(false);
const importing = ref(false);

async function saveProfile(): Promise<void> {
  const qqValue = qq.value.trim();
  if (qqValue && !/^[1-9][0-9]{4,14}$/.test(qqValue)) {
    profileFlash.value = t('qqInvalid');
    return;
  }

  profileBusy.value = true;
  profileFlash.value = '';
  try {
    const { user } = await updateProfile({
      nickname: nickname.value.trim(),
      email: email.value.trim(),
      qq: qqValue,
      bio: bio.value.trim(),
      avatar: avatar.value.trim(),
    });
    adopt(user, currentPreferences.value);
    profileLabel.value = t('saved');
    window.setTimeout(() => { profileLabel.value = ''; }, 1500);
  } catch (error) {
    if (error instanceof ApiError) {
      profileFlash.value = error.code === 'invalid_qq'
        ? t('qqInvalid')
        : error.code === 'qq_taken' ? t('qqTaken') : error.message;
    } else {
      profileFlash.value = String(error);
    }
  } finally {
    profileBusy.value = false;
  }
}

async function savePassword(): Promise<void> {
  if (!currentPassword.value || !newPassword.value) {
    passwordFlash.value = t('fillBothFields');
    return;
  }
  passwordBusy.value = true;
  passwordFlash.value = '';
  try {
    await changePassword(currentPassword.value, newPassword.value);
    currentPassword.value = '';
    newPassword.value = '';
    passwordLabel.value = t('changed');
    window.setTimeout(() => { passwordLabel.value = ''; }, 1500);
  } catch (error) {
    passwordFlash.value = error instanceof ApiError ? error.message : String(error);
  } finally {
    passwordBusy.value = false;
  }
}

/**
 * One document holds the preferences and every conversation, because the two
 * are what an account is: keeping them in separate files would mean restoring
 * half of yourself and remembering to go back for the rest.
 */
function exportData(): void {
  exporting.value = true;
  dataStatus.value = t('exportWorking');
  void exportAccount()
    .then((document) => {
      const stamp = new Date().toISOString().slice(0, 10);
      saveAsFile(`obsidian-arc-${stamp}.json`, JSON.stringify(document, null, 2));
      dataStatus.value = t('exportDone', { count: document.conversations.length });
    })
    .catch((error: unknown) => {
      dataStatus.value = error instanceof ApiError ? error.message : t('failed');
    })
    .finally(() => { exporting.value = false; });
}

/**
 * Importing adds rather than replaces, which is stated on the hint rather
 * than discovered afterwards — a "restore" that ate the conversations it was
 * meant to protect is the one failure this feature cannot have.
 */
function importData(): void {
  dataStatus.value = '';
  void pickJSONFile()
    .then((document) => {
      if (document === null) return null;
      importing.value = true;
      dataStatus.value = t('importWorking');
      return importAccount(document);
    })
    .then((result) => {
      if (!result) return;
      dataStatus.value = t('importDone', {
        conversations: result.conversations,
        messages: result.messages,
      });
      // The imported conversations are not in the list behind this panel, and
      // the preferences may have changed the theme out from under it.
      window.setTimeout(() => window.location.reload(), 1200);
    })
    .catch((error: unknown) => {
      dataStatus.value = error instanceof ApiError ? error.message : t('failed');
    })
    .finally(() => { importing.value = false; });
}
</script>

<template>
  <div v-show="matchesSettings(props.query, 'profile')" class="oa-settings-panel">
    <h2 class="oa-admin-section-title">{{ t('secProfile') }}</h2>
    <OaTextField
      v-model="nickname"
      :label="t('nickname')"
      :placeholder="currentUser?.username"
      :hint="t('nicknameHint')"
      :max-length="32"
    />
    <OaTextField v-model="email" :label="t('email')" type="email" />
    <OaTextField v-model="qq" :label="t('qq')" :placeholder="t('qqPlaceholder')" :max-length="15" />
    <OaTextArea v-model="bio" :label="t('bio')" :rows="3" />
    <OaTextField
      v-model="avatar"
      :label="t('avatar')"
      :placeholder="t('avatarPlaceholderUser')"
      :hint="t('avatarHint')"
    />
    <p class="oa-drawer-flash" :class="{ visible: !!profileFlash }">{{ profileFlash }}</p>
    <div class="oa-button-row">
      <button type="button" class="oa-btn primary" :disabled="profileBusy" @click="saveProfile">
        {{ profileLabel || t('save') }}
      </button>
    </div>
  </div>

  <div v-show="matchesSettings(props.query, 'password')" class="oa-settings-panel">
    <h2 class="oa-admin-section-title">{{ t('secPassword') }}</h2>
    <p class="oa-field-hint">{{ t('passwordSectionHint') }}</p>
    <OaTextField v-model="currentPassword" :label="t('currentPassword')" type="password" />
    <OaTextField
      v-model="newPassword"
      :label="t('newPassword')"
      type="password"
      :hint="t('newPasswordHint')"
    />
    <p class="oa-drawer-flash" :class="{ visible: !!passwordFlash }">{{ passwordFlash }}</p>
    <div class="oa-button-row">
      <button type="button" class="oa-btn" :disabled="passwordBusy" @click="savePassword">
        {{ passwordLabel || t('changePassword') }}
      </button>
    </div>
  </div>

  <div v-show="matchesSettings(props.query, 'data')" class="oa-settings-panel">
    <h2 class="oa-admin-section-title">{{ t('secData') }}</h2>
    <p class="oa-field-hint">{{ t('dataHint') }}</p>
    <div class="oa-button-row">
      <button type="button" class="oa-btn" :disabled="exporting" @click="exportData">
        {{ t('exportData') }}
      </button>
      <button type="button" class="oa-btn" :disabled="importing" @click="importData">
        {{ t('importData') }}
      </button>
    </div>
    <p class="oa-field-hint">{{ dataStatus }}</p>
  </div>
</template>
