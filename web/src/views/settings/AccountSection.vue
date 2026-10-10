<script setup lang="ts">
// Who this account is, what its password is, and how to take its data out.
//
// Each part commits on its own button. There is no Save at the bottom of the
// settings panel, because one would be claiming to commit things it has
// nothing to do with.

import { computed, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { changePassword, updateProfile } from '@/api/auth';
import { exportAccount, importAccount, pickJSONFile, saveAsFile } from '@/api/backup';
import { ApiError } from '@/api/client';
import {
  fetchAuthorizations, withdrawAuthorization, type Authorization,
} from '@/api/consent';
import {
  disconnectProvider, fetchConnections, signInURL,
  type OAuthConnection, type OAuthProvider,
} from '@/api/oauth';
import OaAccountFields from '@/components/OaAccountFields.vue';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaGroup from '@/components/OaGroup.vue';
import OaRow from '@/components/OaRow.vue';
import OaTextArea from '@/components/OaTextArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t, type StringKey } from '@/composables/useI18n';
import { IconGithub, IconGoogle, IconKey, type OaIcon } from '@/icons';
import { allFields, fieldProblem, fieldValues } from '@/lib/account-fields';
import { absoluteTime } from '@/lib/format';
import { pinnedProvider, pluginRefusal } from '@/plugins/registry';
import { adopt, currentPreferences, currentUser, requireUser, siteInfo } from '@/stores/session';
import { matchesSettings } from './search';

const props = withDefaults(defineProps<{ query?: string }>(), { query: '' });

const account = requireUser();
const route = useRoute();
const router = useRouter();

const nickname = ref(account.nickname);
const email = ref(account.email);
// Every field a plugin added, whether or not sign-up asks for it: this is
// where an owner changes the value they gave. Required ones cannot be
// cleared, which is the rule the server holds them to as well.
const fieldPlan = computed(() => allFields(siteInfo.value));
const fields = ref<Record<string, string>>({ ...(account.fields ?? {}) });
const bio = ref(account.bio);
const avatar = ref(account.avatar);

const profileFlash = ref('');
const profileBusy = ref(false);
const profileLabel = ref('');
// Asked for only while the address is actually changing: the form sends the
// unchanged address with every save, and a nickname edit must not need it.
const profilePassword = ref('');

const currentPassword = ref('');
const newPassword = ref('');
const passwordFlash = ref('');
const passwordBusy = ref(false);
const passwordLabel = ref('');

const dataStatus = ref('');
const exporting = ref(false);
const importing = ref(false);

// --- accounts elsewhere that sign into this one -------------------------------

const connections = ref<OAuthConnection[]>([]);
const providers = ref<OAuthProvider[]>([]);
const connectionFlash = ref('');
const connectionsBusy = ref(false);
/**
 * Whether this account also has a password.
 *
 * Unknown until the connections load, and assumed true until then, because
 * that is the state every account created by the sign-up form is in — the
 * password box should not flicker from "set one" to "change it" on the way
 * past. An account with no password is one that only ever signed in through a
 * provider, and this is what lets it give itself a way in that does not
 * depend on the operator leaving that provider switched on.
 */
const hasPassword = ref(true);

/** The server compares addresses case-insensitively, so this does too. */
const emailMoving = computed(() =>
  email.value.trim().toLowerCase() !== (currentUser.value?.email ?? '').trim().toLowerCase());
const askForPassword = computed(() => emailMoving.value && hasPassword.value);

const MARKS: Record<string, OaIcon> = { github: IconGithub, google: IconGoogle };
function mark(id: string): OaIcon {
  return MARKS[id] ?? IconKey;
}

const linked = computed(() => new Map(connections.value.map((item) => [item.provider, item])));

/** The line under a provider's name: who it is here, and since when — or that it is not connected. */
function connectionMeta(id: string): string {
  const link = linked.value.get(id);
  if (!link) return t('notConnected');
  const who = link.login || link.email || t('connected');
  return link.created_at ? `${who} · ${t('connectedOn', { date: absoluteTime(link.created_at) })}` : who;
}

/** The line under an application's name: when it was let in, and when it was last used. */
function authorizationMeta(grant: Authorization): string {
  const since = t('connectedOn', { date: absoluteTime(grant.created_at) });
  return grant.last_used_at ? `${since} · ${t('lastUsedOn', { date: absoluteTime(grant.last_used_at) })}` : since;
}
// A provider this server has switched off is still listed while it is
// connected: it is a way into this account, and hiding it would hide the only
// control that can remove it.
const rows = computed(() => providers.value.filter(
  (provider) => provider.enabled || linked.value.has(provider.id),
));

const OAUTH_REFUSALS: Record<string, StringKey> = {
  denied: 'oauthDenied',
  state: 'oauthState',
  unavailable: 'oauthUnavailable',
  provider: 'oauthProviderFailed',
  already_linked: 'oauthAlreadyLinked',
};

async function loadConnections(): Promise<void> {
  try {
    const result = await fetchConnections();
    connections.value = result.connections ?? [];
    providers.value = result.providers ?? [];
    hasPassword.value = result.has_password;
  } catch (error) {
    connectionFlash.value = error instanceof ApiError ? error.message : String(error);
  }
}

function disconnect(provider: string): void {
  connectionsBusy.value = true;
  connectionFlash.value = '';
  void disconnectProvider(provider)
    .then(loadConnections)
    .catch((error: unknown) => {
      // The one refusal worth its own sentence: removing this would leave
      // nobody, including its owner, able to reach the account again.
      connectionFlash.value = error instanceof ApiError && error.code === 'last_way_in'
        ? t('oauthLastWayIn')
        : error instanceof ApiError && error.code === 'oidc_pinned'
          ? pinnedProvider(provider)?.refused() ?? error.message
          : error instanceof ApiError ? error.message : String(error);
    })
    .finally(() => { connectionsBusy.value = false; });
}

// --- the sites this account has let in ----------------------------------------

const authorizations = ref<Authorization[]>([]);
const authorizationFlash = ref('');
const authorizationsBusy = ref(false);

async function loadAuthorizations(): Promise<void> {
  try {
    const result = await fetchAuthorizations();
    authorizations.value = result.authorizations ?? [];
  } catch (error) {
    authorizationFlash.value = error instanceof ApiError ? error.message : String(error);
  }
}

/**
 * Withdrawing is not only removing a row from this list: the tokens that
 * application holds stop answering, so it is signed out of this account as
 * well as forgotten by it. It has to ask again next time, and the screen says
 * so rather than leaving somebody to wonder.
 */
function withdraw(appID: string): void {
  authorizationsBusy.value = true;
  authorizationFlash.value = '';
  void withdrawAuthorization(appID)
    .then(loadAuthorizations)
    .catch((error: unknown) => {
      authorizationFlash.value = error instanceof ApiError ? error.message : String(error);
    })
    .finally(() => { authorizationsBusy.value = false; });
}

// The connect flow leaves this page and comes back to it, so its outcome
// arrives in the query rather than in a response.
onMounted(() => {
  const failure = route.query['oauth_error'];
  const done = route.query['oauth'];
  if (typeof failure === 'string' && failure) {
    // Own keys only, as in AuthView: an inherited name is not a refusal.
    const known = Object.hasOwn(OAUTH_REFUSALS, failure) ? OAUTH_REFUSALS[failure] : undefined;
    connectionFlash.value = t(known ?? 'oauthFailed');
  } else if (done === 'connected') {
    connectionFlash.value = t('oauthConnected');
  }
  if (failure || done) {
    // Only the two keys this section reads. It can be open over the backoffice,
    // and the rest of that address belongs to the page underneath.
    const query = { ...route.query };
    delete query['oauth'];
    delete query['oauth_error'];
    void router.replace({ path: route.path, query });
  }
  void loadConnections();
  void loadAuthorizations();
});

async function saveProfile(): Promise<void> {
  const fieldError = fieldProblem(fields.value, fieldPlan.value);
  if (fieldError) {
    profileFlash.value = fieldError;
    return;
  }

  if (askForPassword.value && !profilePassword.value) {
    profileFlash.value = t('emailChangePasswordHint');
    return;
  }

  profileBusy.value = true;
  profileFlash.value = '';
  try {
    const { user } = await updateProfile({
      nickname: nickname.value.trim(),
      email: email.value.trim(),
      ...(fieldPlan.value.keys.length ? { fields: fieldValues(fields.value, fieldPlan.value) } : {}),
      bio: bio.value.trim(),
      avatar: avatar.value.trim(),
      ...(askForPassword.value ? { current_password: profilePassword.value } : {}),
    });
    adopt(user, currentPreferences.value);
    profilePassword.value = '';
    profileLabel.value = t('saved');
    window.setTimeout(() => { profileLabel.value = ''; }, 1500);
  } catch (error) {
    if (error instanceof ApiError && error.code === 'current_password_wrong') {
      profileFlash.value = t('currentPasswordWrong');
      profilePassword.value = '';
    } else if (error instanceof ApiError && error.code === 'password_required') {
      profileFlash.value = t('emailChangePasswordHint');
    } else if (error instanceof ApiError) {
      profileFlash.value = pluginRefusal(error.code ?? '') ?? error.message;
    } else {
      profileFlash.value = String(error);
    }
  } finally {
    profileBusy.value = false;
  }
}

async function savePassword(): Promise<void> {
  // An account that has never had a password has nothing to confirm, and the
  // field for it is not on screen.
  if ((hasPassword.value && !currentPassword.value) || !newPassword.value) {
    passwordFlash.value = t('fillBothFields');
    return;
  }
  passwordBusy.value = true;
  passwordFlash.value = '';
  try {
    await changePassword(currentPassword.value, newPassword.value);
    currentPassword.value = '';
    newPassword.value = '';
    // From here it is an ordinary password: the next change asks for it.
    hasPassword.value = true;
    passwordLabel.value = t('changed');
    window.setTimeout(() => { passwordLabel.value = ''; }, 1500);
  } catch (error) {
    if (error instanceof ApiError && error.code === 'reauth_required') {
      passwordFlash.value = t('reauthForPassword');
    } else {
      passwordFlash.value = error instanceof ApiError ? error.message : String(error);
    }
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
  <OaGroup v-show="matchesSettings(props.query, 'profile')" :title="t('secProfile')">
    <OaRow stacked>
      <OaTextField
        v-model="nickname"
        :label="t('nickname')"
        :placeholder="currentUser?.username"
        :hint="t('nicknameHint')"
        :max-length="32"
      />
    </OaRow>
    <OaRow stacked><OaTextField v-model="email" :label="t('email')" type="email" /></OaRow>
    <OaRow v-if="askForPassword" stacked>
      <OaTextField
        v-model="profilePassword"
        :label="t('currentPassword')"
        :hint="t('emailChangePasswordHint')"
        type="password"
        autocomplete="current-password"
      />
    </OaRow>
    <OaRow v-if="fieldPlan.keys.length" stacked><OaAccountFields v-model="fields" :plan="fieldPlan" /></OaRow>
    <OaRow stacked><OaTextArea v-model="bio" :label="t('bio')" :rows="3" /></OaRow>
    <OaRow stacked>
      <OaTextField
        v-model="avatar"
        :label="t('avatar')"
        :placeholder="t('avatarPlaceholderUser')"
        :hint="t('avatarHint')"
      />
    </OaRow>
    <OaRow :title="t('registrationUserAgent')">
      <template #text><span class="oa-group-row-meta mono">{{ account.signup_user_agent || '—' }}</span></template>
    </OaRow>
    <p v-if="profileFlash" class="oa-group-flash" role="alert">{{ profileFlash }}</p>
    <OaRow>
      <button type="button" class="oa-btn primary" :disabled="profileBusy" @click="saveProfile">
        {{ profileLabel || t('save') }}
      </button>
    </OaRow>
  </OaGroup>

  <OaGroup v-show="matchesSettings(props.query, 'connections')" :title="t('secConnections')" :hint="t('connectionsHint')">
    <p v-if="!rows.length" class="oa-group-note">{{ t('connectionsNone') }}</p>
    <OaRow
      v-for="provider in rows"
      :key="provider.id"
      class="oa-connection"
      :icon="mark(provider.id)"
      :title="provider.name"
      :meta="connectionMeta(provider.id)"
    >
      <!-- A link, not a button: connecting is a trip to the provider and
           back, which the browser has to navigate itself. -->
      <a
        v-if="!linked.has(provider.id)"
        class="oa-btn"
        :href="signInURL(provider.id, { link: true, next: '/settings' })"
      >{{ t('connect') }}</a>
      <!-- A connection a plugin made permanent proves a detail of the
           account, so it has no remove control at all — deleting the
           account is the only way it comes off. -->
      <span v-else-if="pinnedProvider(provider.id)" class="oa-group-row-meta">
        {{ pinnedProvider(provider.id)!.hint() }}
      </span>
      <OaConfirmButton
        v-else
        class="oa-btn"
        :label="t('disconnect')"
        :armed-label="t('confirmWord')"
        :armed-title="t('disconnectConfirm', { provider: provider.name })"
        :resting-title="t('disconnect')"
        :disabled="connectionsBusy"
        @confirm="disconnect(provider.id)"
      />
    </OaRow>
    <p v-if="connectionFlash" class="oa-group-flash" role="alert">{{ connectionFlash }}</p>
  </OaGroup>

  <OaGroup v-show="matchesSettings(props.query, 'authorizations')" :title="t('secAuthorizations')" :hint="t('authorizationsHint')">
    <p v-if="!authorizations.length" class="oa-group-note">{{ t('authorizationsNone') }}</p>
    <OaRow
      v-for="grant in authorizations"
      :key="grant.app_id"
      :icon="IconKey"
      :title="grant.name"
      :meta="authorizationMeta(grant)"
    >
      <OaConfirmButton
        class="oa-btn"
        :label="t('withdraw')"
        :armed-label="t('confirmWord')"
        :armed-title="t('withdrawConfirm', { application: grant.name })"
        :resting-title="t('withdraw')"
        :disabled="authorizationsBusy"
        @confirm="withdraw(grant.app_id)"
      />
    </OaRow>
    <p v-if="authorizationFlash" class="oa-group-flash" role="alert">{{ authorizationFlash }}</p>
  </OaGroup>

  <OaGroup
    v-show="matchesSettings(props.query, 'password')"
    :title="hasPassword ? t('secPassword') : t('setPassword')"
    :hint="hasPassword ? t('passwordSectionHint') : t('setPasswordHint')"
  >
    <OaRow v-if="hasPassword" stacked>
      <OaTextField v-model="currentPassword" :label="t('currentPassword')" type="password" />
    </OaRow>
    <OaRow stacked>
      <OaTextField v-model="newPassword" :label="t('newPassword')" type="password" :hint="t('newPasswordHint')" />
    </OaRow>
    <p v-if="passwordFlash" class="oa-group-flash" role="alert">{{ passwordFlash }}</p>
    <OaRow>
      <button type="button" class="oa-btn" :disabled="passwordBusy" @click="savePassword">
        {{ passwordLabel || (hasPassword ? t('changePassword') : t('setPassword')) }}
      </button>
    </OaRow>
  </OaGroup>

  <OaGroup v-show="matchesSettings(props.query, 'data')" :title="t('secData')" :hint="t('dataHint')">
    <OaRow>
      <button type="button" class="oa-btn" :disabled="exporting" @click="exportData">
        {{ t('exportData') }}
      </button>
      <button type="button" class="oa-btn" :disabled="importing" @click="importData">
        {{ t('importData') }}
      </button>
    </OaRow>
    <p v-if="dataStatus" class="oa-group-note">{{ dataStatus }}</p>
  </OaGroup>
</template>
