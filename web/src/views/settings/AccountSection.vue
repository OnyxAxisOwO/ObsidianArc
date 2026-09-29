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

const MARKS: Record<string, OaIcon> = { github: IconGithub, google: IconGoogle };
function mark(id: string): OaIcon {
  return MARKS[id] ?? IconKey;
}

const linked = computed(() => new Map(connections.value.map((item) => [item.provider, item])));
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
    connectionFlash.value = t(OAUTH_REFUSALS[failure] ?? 'oauthFailed');
  } else if (done === 'connected') {
    connectionFlash.value = t('oauthConnected');
  }
  if (failure || done) void router.replace({ path: route.path, query: {} });
  void loadConnections();
  void loadAuthorizations();
});

async function saveProfile(): Promise<void> {
  const fieldError = fieldProblem(fields.value, fieldPlan.value);
  if (fieldError) {
    profileFlash.value = fieldError;
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
    });
    adopt(user, currentPreferences.value);
    profileLabel.value = t('saved');
    window.setTimeout(() => { profileLabel.value = ''; }, 1500);
  } catch (error) {
    if (error instanceof ApiError) {
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
    <OaAccountFields v-model="fields" :plan="fieldPlan" />
    <OaTextArea v-model="bio" :label="t('bio')" :rows="3" />
    <OaTextField
      v-model="avatar"
      :label="t('avatar')"
      :placeholder="t('avatarPlaceholderUser')"
      :hint="t('avatarHint')"
    />
    <div class="oa-facts">
      <div class="oa-fact">
        <span class="oa-fact-label">{{ t('registrationUserAgent') }}</span>
        <span class="oa-fact-value mono">{{ account.signup_user_agent || '—' }}</span>
      </div>
    </div>
    <p class="oa-drawer-flash" :class="{ visible: !!profileFlash }">{{ profileFlash }}</p>
    <div class="oa-button-row">
      <button type="button" class="oa-btn primary" :disabled="profileBusy" @click="saveProfile">
        {{ profileLabel || t('save') }}
      </button>
    </div>
  </div>

  <div v-show="matchesSettings(props.query, 'connections')" class="oa-settings-panel">
    <h2 class="oa-admin-section-title">{{ t('secConnections') }}</h2>
    <p class="oa-field-hint">{{ t('connectionsHint') }}</p>
    <p v-if="!rows.length" class="oa-field-hint">{{ t('connectionsNone') }}</p>
    <div v-else class="oa-connections">
      <div v-for="provider in rows" :key="provider.id" class="oa-connection">
        <span class="oa-connection-mark"><component :is="mark(provider.id)" :size="16" /></span>
        <span class="oa-connection-body">
          <span class="oa-connection-name">{{ provider.name }}</span>
          <span class="oa-connection-meta">
            <template v-if="linked.get(provider.id)">
              {{ linked.get(provider.id)?.login || linked.get(provider.id)?.email || t('connected') }}
              <template v-if="linked.get(provider.id)?.created_at">
                · {{ t('connectedOn', { date: absoluteTime(linked.get(provider.id)!.created_at) }) }}
              </template>
            </template>
            <template v-else>{{ t('notConnected') }}</template>
          </span>
        </span>
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
        <span v-else-if="pinnedProvider(provider.id)" class="oa-connection-meta">
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
      </div>
    </div>
    <p class="oa-drawer-flash" :class="{ visible: !!connectionFlash }">{{ connectionFlash }}</p>
  </div>

  <div v-show="matchesSettings(props.query, 'authorizations')" class="oa-settings-panel">
    <h2 class="oa-admin-section-title">{{ t('secAuthorizations') }}</h2>
    <p class="oa-field-hint">{{ t('authorizationsHint') }}</p>
    <p v-if="!authorizations.length" class="oa-field-hint">{{ t('authorizationsNone') }}</p>
    <div v-else class="oa-connections">
      <div v-for="grant in authorizations" :key="grant.app_id" class="oa-connection">
        <span class="oa-connection-mark"><IconKey :size="16" /></span>
        <span class="oa-connection-body">
          <span class="oa-connection-name">{{ grant.name }}</span>
          <span class="oa-connection-meta">
            {{ t('connectedOn', { date: absoluteTime(grant.created_at) }) }}
            <template v-if="grant.last_used_at">
              · {{ t('lastUsedOn', { date: absoluteTime(grant.last_used_at) }) }}
            </template>
          </span>
        </span>
        <OaConfirmButton
          class="oa-btn"
          :label="t('withdraw')"
          :armed-label="t('confirmWord')"
          :armed-title="t('withdrawConfirm', { application: grant.name })"
          :resting-title="t('withdraw')"
          :disabled="authorizationsBusy"
          @confirm="withdraw(grant.app_id)"
        />
      </div>
    </div>
    <p class="oa-drawer-flash" :class="{ visible: !!authorizationFlash }">{{ authorizationFlash }}</p>
  </div>

  <div v-show="matchesSettings(props.query, 'password')" class="oa-settings-panel">
    <h2 class="oa-admin-section-title">{{ hasPassword ? t('secPassword') : t('setPassword') }}</h2>
    <p class="oa-field-hint">{{ hasPassword ? t('passwordSectionHint') : t('setPasswordHint') }}</p>
    <OaTextField
      v-if="hasPassword"
      v-model="currentPassword"
      :label="t('currentPassword')"
      type="password"
    />
    <OaTextField
      v-model="newPassword"
      :label="t('newPassword')"
      type="password"
      :hint="t('newPasswordHint')"
    />
    <p class="oa-drawer-flash" :class="{ visible: !!passwordFlash }">{{ passwordFlash }}</p>
    <div class="oa-button-row">
      <button type="button" class="oa-btn" :disabled="passwordBusy" @click="savePassword">
        {{ passwordLabel || (hasPassword ? t('changePassword') : t('setPassword')) }}
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
