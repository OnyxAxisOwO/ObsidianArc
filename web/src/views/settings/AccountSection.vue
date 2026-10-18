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
  connectProvider, disconnectProvider, fetchConnections,
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
import { apiErrorText } from '@/lib/api-error';
import { absoluteTime } from '@/lib/format';
import { connectRefusalText, refusalText } from '@/lib/refusal';
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
// A connection is started by a request, not a link, because the account proves
// itself first (see connectProvider). `connecting` is the provider whose connection
// is waiting for the password the server asked for, if there is one.
const connecting = ref('');
const connectPassword = ref('');
const connectBusy = ref(false);
/**
 * Whether this account also has a password: true, false, or null while the
 * connections have not loaded.
 *
 * The password forms are not offered until this is known. Assuming an answer
 * shows the wrong form for one of the two kinds of account: a password box for
 * an account that has none asks for a thing it cannot give. An account with no
 * password is one that only ever signed in through a provider, and this is
 * what lets it give itself a way in that does not depend on the operator
 * leaving that provider switched on.
 */
const hasPassword = ref<boolean | null>(null);

/** The server compares addresses case-insensitively, so this does too. */
const emailMoving = computed(() =>
  email.value.trim().toLowerCase() !== (currentUser.value?.email ?? '').trim().toLowerCase());
// Asked for unless the account is known to have no password. While the
// connections are loading or have failed to load, the state is null, and hiding
// the field then would leave the server's password_required refusal with
// nothing to type into.
const askForPassword = computed(() => emailMoving.value && hasPassword.value !== false);

const MARKS: Record<string, OaIcon> = { github: IconGithub, google: IconGoogle };
function mark(id: string): OaIcon {
  return MARKS[id] ?? IconKey;
}

const linked = computed(() => new Map(connections.value.map((item) => [item.provider, item])));

// Where a connection brings the browser back to. In the chat that is the settings
// screen, as it always was. Over the backoffice it is the address this panel is
// drawn at, panel parameter and all, so the page underneath stays where it was and
// the panel reopens with the outcome in it. The server returns to the page it was
// given, when it fails as well as when it works.
const connectBack = computed(() => (route.meta['admin'] ? route.fullPath : '/settings'));

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
  address_taken: 'oauthConnectAddressTaken',
};

// The message of the last read that failed, so a later read that works can take
// back its own failure from both places it was shown, and nothing else.
let failedLoad = '';

async function loadConnections(): Promise<void> {
  try {
    const result = await fetchConnections();
    connections.value = result.connections ?? [];
    providers.value = result.providers ?? [];
    hasPassword.value = result.has_password;
    if (failedLoad) {
      if (connectionFlash.value === failedLoad) connectionFlash.value = '';
      if (passwordFlash.value === failedLoad) passwordFlash.value = '';
      failedLoad = '';
    }
  } catch (error) {
    const message = apiErrorText(error);
    failedLoad = message;
    connectionFlash.value = message;
    // Until this loads the password section cannot say which form it is, so the
    // failure is reported there too, with a way to try again.
    passwordFlash.value = message;
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
          ? pinnedProvider(provider)?.refused() ?? apiErrorText(error)
          : apiErrorText(error);
    })
    .finally(() => { connectionsBusy.value = false; });
}

/** The name the password prompt gives the provider it is connecting. */
const connectingName = computed(() =>
  providers.value.find((item) => item.id === connecting.value)?.name ?? connecting.value);

function startConnect(provider: string): void {
  connecting.value = '';
  connectPassword.value = '';
  sendToProvider(provider, '');
}

function submitConnect(): void {
  sendToProvider(connecting.value, connectPassword.value);
}

function cancelConnect(): void {
  connecting.value = '';
  connectPassword.value = '';
}

/**
 * Asks the server to start the connection, and leaves for the provider with the
 * address it answers with. A password the server wants opens the field to type it
 * into, and a wrong one keeps the field open. Nothing is started until the proof is
 * right, so nothing has to be undone when it is not.
 */
function sendToProvider(provider: string, password: string): void {
  connectBusy.value = true;
  connectionFlash.value = '';
  void connectProvider(provider, { password, next: connectBack.value })
    .then((answer) => {
      // Off this page for good: the provider's consent screen, then back here.
      window.location.assign(answer.redirect);
    })
    .catch((error: unknown) => {
      if (error instanceof ApiError && error.code === 'password_required') {
        // The field's own hint says what it is for, so there is no flash.
        connecting.value = provider;
        return;
      }
      if (error instanceof ApiError && error.code === 'current_password_wrong') {
        connecting.value = provider;
        connectPassword.value = '';
        connectionFlash.value = t('currentPasswordWrong');
        return;
      }
      connectionFlash.value = connectRefusalText(error);
    })
    .finally(() => { connectBusy.value = false; });
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
    authorizationFlash.value = apiErrorText(error);
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
      authorizationFlash.value = apiErrorText(error);
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
    // and the rest of that address, its hash included, belongs to the page
    // underneath.
    const query = { ...route.query };
    delete query['oauth'];
    delete query['oauth_error'];
    void router.replace({ path: route.path, query, hash: route.hash });
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
    } else if (error instanceof ApiError && error.code === 'reauth_required') {
      profileFlash.value = t('reauthForAddress');
    } else if (error instanceof ApiError && error.code === 'too_many_attempts') {
      // Addresses that have used this hour's lookups come back with the wait,
      // which refusalText words in the reader's language.
      profileFlash.value = refusalText(error);
    } else if (error instanceof ApiError) {
      profileFlash.value = pluginRefusal(error.code ?? '') ?? apiErrorText(error);
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
    } else if (error instanceof ApiError && error.code === 'current_password_wrong') {
      passwordFlash.value = t('currentPasswordWrong');
    } else {
      passwordFlash.value = apiErrorText(error);
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
      // Compact on purpose: indentation can push a file over the size the
      // server will import, and the file is read back by the same importer.
      saveAsFile(`obsidian-arc-${stamp}.json`, JSON.stringify(document));
      dataStatus.value = t('exportDone', { count: document.conversations.length });
    })
    .catch((error: unknown) => {
      dataStatus.value = apiErrorText(error);
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
      // Any failure after the import has started carries the counts of what it
      // wrote before stopping, whatever its code. What was written is in the
      // account already. Say how much, or the reader re-sends the same file and
      // gets a second copy of it. A failure that wrote nothing has no partial
      // import to describe, so it falls through to its own reason.
      if (error instanceof ApiError && typeof error.details['conversations'] === 'number') {
        const counts = {
          conversations: Number(error.details['conversations']),
          messages: Number(error.details['messages'] ?? 0),
        };
        if (counts.conversations > 0 || counts.messages > 0) {
          dataStatus.value = error.code === 'storage_full'
            ? t('importStorageFull', counts)
            : t('importPartialFailed', counts);
          return;
        }
      }
      dataStatus.value = apiErrorText(error);
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
      <!-- A request, not a link: the account proves itself first, and the browser
           leaves for the provider only with the address the server answers. -->
      <button
        v-if="!linked.has(provider.id)"
        type="button"
        class="oa-btn"
        :disabled="connectBusy"
        @click="startConnect(provider.id)"
      >{{ t('connect') }}</button>
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
    <template v-if="connecting">
      <OaRow stacked>
        <OaTextField
          v-model="connectPassword"
          :label="t('currentPassword')"
          type="password"
          :hint="t('connectPasswordHint', { provider: connectingName })"
        />
      </OaRow>
      <OaRow>
        <button type="button" class="oa-btn primary" :disabled="connectBusy" @click="submitConnect">
          {{ t('connect') }}
        </button>
        <button type="button" class="oa-btn" :disabled="connectBusy" @click="cancelConnect">
          {{ t('cancel') }}
        </button>
      </OaRow>
    </template>
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
    :title="hasPassword === false ? t('setPassword') : t('secPassword')"
    :hint="hasPassword === false ? t('setPasswordHint') : t('passwordSectionHint')"
  >
    <OaRow v-if="hasPassword === null" stacked>
      <button type="button" class="oa-btn" @click="loadConnections">{{ t('retry') }}</button>
    </OaRow>
    <OaRow v-if="hasPassword === true" stacked>
      <OaTextField v-model="currentPassword" :label="t('currentPassword')" type="password" />
    </OaRow>
    <OaRow v-if="hasPassword !== null" stacked>
      <OaTextField v-model="newPassword" :label="t('newPassword')" type="password" :hint="t('newPasswordHint')" />
    </OaRow>
    <p v-if="passwordFlash" class="oa-group-flash" role="alert">{{ passwordFlash }}</p>
    <OaRow v-if="hasPassword !== null">
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
