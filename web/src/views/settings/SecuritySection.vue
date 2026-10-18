<script setup lang="ts">
// The account's second sign-in step: whether it is on, the way to turn it on,
// and the two things done to it afterwards — new recovery codes, and turning
// it off.
//
// Both of those take a code, because a session somebody walked away from is
// exactly what they must not be enough for. The field for it opens under the
// row that asked for it, so the resting state is one card with a status line
// and two rows rather than a form nobody is filling in. The setup wizard opens
// inside the same card, so the thing being set up and the steps setting it up
// read as one object instead of a stack of loose parts.

import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import type { Account } from '@/api/auth';
import { ApiError } from '@/api/client';
import { apiErrorText } from '@/lib/api-error';
import { fetchSessions, revokeOtherSessions, revokeSession, type DeviceSession } from '@/api/sessions';
import {
  disableTwoFactor, fetchTwoFactor, regenerateRecovery, type TwoFactorStatus,
} from '@/api/twofactor';
import { copyToClipboard } from '@/chat/markdown';
import OaBadge from '@/components/OaBadge.vue';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import { t } from '@/composables/useI18n';
import { IconAuto, IconCheck, IconCopy, IconShield } from '@/icons';
import { absoluteTime, relativeTime } from '@/lib/format';
import { describeUserAgent } from '@/lib/ua';
import { adopt, currentPreferences } from '@/stores/session';
import { pendingRecovery } from './recovery';
import { matchesSettings } from './search';
import TwoFactorWizard from './TwoFactorWizard.vue';

const props = withDefaults(defineProps<{ query?: string }>(), { query: '' });

const status = ref<TwoFactorStatus | null>(null);
const flash = ref('');
const notice = ref('');
const wizard = ref(false);

/** Which code-taking action is open, if any. */
const action = ref<'' | 'disable' | 'recovery'>('');
const code = ref('');
const busy = ref(false);
// The codes a regeneration just returned, held in recovery.ts so that they
// survive this section being unmounted. Cleared by Finish, after the reader has
// said they are saved, or by turning two-step verification off, since the codes
// are then of no use.
const fresh = computed<string[]>(() => pendingRecovery.value?.codes ?? []);
const savedCodes = computed({
  get: (): boolean => pendingRecovery.value?.saved ?? false,
  set: (saved: boolean) => { if (pendingRecovery.value) pendingRecovery.value.saved = saved; },
});
const copied = ref(false);

/** Few enough left that running out is a real prospect. */
const running = computed(() => !!status.value?.enabled && status.value.recovery_remaining <= 3);

async function load(): Promise<void> {
  try {
    status.value = await fetchTwoFactor();
  } catch (failure) {
    flash.value = apiErrorText(failure);
  }
}

onMounted(load);

function enrolled(user: Account): void {
  adopt(user, currentPreferences.value);
  wizard.value = false;
  notice.value = t('twoFactorEnabledNotice');
  void load();
}

function open(next: 'disable' | 'recovery'): void {
  action.value = action.value === next ? '' : next;
  code.value = '';
  flash.value = '';
}

function finishFresh(): void {
  pendingRecovery.value = null;
  action.value = '';
}

function refusal(failure: unknown): string {
  if (!(failure instanceof ApiError)) return apiErrorText(failure);
  switch (failure.code) {
    case 'two_factor_code': return t('twoFactorCodeWrong');
    case 'two_factor_mandatory': return t('twoFactorMandatoryNote');
    default: return apiErrorText(failure);
  }
}

async function confirm(): Promise<void> {
  if (busy.value || !code.value.trim()) return;
  busy.value = true;
  flash.value = '';
  notice.value = '';
  try {
    if (action.value === 'disable') {
      const { user } = await disableTwoFactor(code.value.trim());
      adopt(user, currentPreferences.value);
      notice.value = t('twoFactorTurnedOff');
      // Codes from before this setup are of no use to a later one, and would
      // show again when it is switched back on.
      pendingRecovery.value = null;
    } else {
      const { recovery_codes: codes } = await regenerateRecovery(code.value.trim());
      pendingRecovery.value = { codes, saved: false };
    }
    action.value = action.value === 'recovery' ? 'recovery' : '';
    code.value = '';
    await load();
  } catch (failure) {
    flash.value = refusal(failure);
  } finally {
    busy.value = false;
  }
}

function copyFresh(): void {
  void copyToClipboard(fresh.value.join('\n')).then((ok) => {
    if (!ok) return;
    copied.value = true;
    window.setTimeout(() => { copied.value = false; }, 1500);
  });
}

// Closing or reloading the tab while regenerated codes are on screen leaves
// two-step verification on with codes nobody has seen, and the server keeps
// only their digests, so they cannot be shown again. The browser is asked
// before that happens. Leaving this page inside the app asks nothing: the codes
// stay in memory (see recovery.ts), so coming back shows them again.
function warnBeforeLeaving(event: BeforeUnloadEvent): void {
  if (!fresh.value.length || savedCodes.value) return;
  event.preventDefault();
  event.returnValue = '';
}

onMounted(() => window.addEventListener('beforeunload', warnBeforeLeaving));
onBeforeUnmount(() => window.removeEventListener('beforeunload', warnBeforeLeaving));

// --- signed-in devices --------------------------------------------------------
//
// A card of its own below the two-step one: they are both "how somebody
// signs in", but neither controls the other, and a caller here is choosing
// between "add a lock" and "check who is already inside" — two different
// questions that happen to share a page.

const devices = ref<DeviceSession[] | null>(null);
const devicesFlash = ref('');
const devicesNotice = ref('');
const revokingID = ref('');
const revokingOthers = ref(false);

async function loadDevices(): Promise<void> {
  try {
    const { sessions } = await fetchSessions();
    devices.value = sessions;
  } catch (failure) {
    devicesFlash.value = apiErrorText(failure);
  }
}

onMounted(loadDevices);

async function signOutDevice(id: string): Promise<void> {
  revokingID.value = id;
  devicesFlash.value = '';
  devicesNotice.value = '';
  try {
    await revokeSession(id);
    devices.value = (devices.value ?? []).filter((entry) => entry.id !== id);
  } catch (failure) {
    devicesFlash.value = apiErrorText(failure);
  } finally {
    revokingID.value = '';
  }
}

async function signOutOthers(): Promise<void> {
  revokingOthers.value = true;
  devicesFlash.value = '';
  devicesNotice.value = '';
  try {
    await revokeOtherSessions();
    devices.value = (devices.value ?? []).filter((entry) => entry.current);
    devicesNotice.value = t('deviceSignOutOthersDone');
  } catch (failure) {
    devicesFlash.value = apiErrorText(failure);
  } finally {
    revokingOthers.value = false;
  }
}
</script>

<template>
  <div v-show="matchesSettings(props.query, 'twofactor')" class="oa-settings-panel">
    <h2 class="oa-admin-section-title">{{ t('secTwoFactor') }}</h2>

    <section v-if="status" class="oa-2fa-panel">
      <header class="oa-2fa-head">
        <span class="oa-2fa-head-mark"><IconShield :size="18" /></span>
        <div class="oa-2fa-head-text">
          <div class="oa-2fa-head-title">
            <span>{{ t('twoFactorAuthenticator') }}</span>
            <OaBadge :tone="status.enabled ? 'default' : 'muted'">
              {{ status.enabled ? t('twoFactorOn') : t('twoFactorOff') }}
            </OaBadge>
          </div>
          <p class="oa-2fa-head-meta">
            {{ status.enabled ? t('twoFactorOnSince', { date: absoluteTime(status.enabled_at) }) : t('twoFactorIntro') }}
          </p>
        </div>
        <button
          v-if="!status.enabled && status.available && !wizard"
          type="button"
          class="oa-btn primary"
          @click="wizard = true; notice = ''"
        >
          {{ t('twoFactorTurnOn') }}
        </button>
      </header>

      <p v-if="!status.available" class="oa-2fa-note">{{ t('twoFactorUnavailable') }}</p>
      <p v-if="status.mandatory" class="oa-2fa-note">
        {{ status.enabled ? t('twoFactorMandatoryNote') : t('twoFactorMandatoryOff') }}
      </p>

      <div v-if="wizard && !status.enabled" class="oa-2fa-panel-body">
        <TwoFactorWizard cancellable @done="enrolled" @cancel="wizard = false" />
      </div>

      <template v-if="status.enabled">
        <div class="oa-2fa-row">
          <div class="oa-2fa-row-text">
            <span class="oa-2fa-row-title">{{ t('twoFactorRecoveryRow') }}</span>
            <span class="oa-2fa-row-meta" :class="{ warn: running }">
              {{ running
                ? t('twoFactorRecoveryLow', { count: status.recovery_remaining })
                : t('twoFactorRecoveryRowHint', { count: status.recovery_remaining }) }}
            </span>
          </div>
          <button type="button" class="oa-btn" :class="{ primary: action === 'recovery' }" @click="open('recovery')">
            {{ t('twoFactorRegenerateShort') }}
          </button>
        </div>
        <form v-if="action === 'recovery' && !fresh.length" class="oa-2fa-confirm" novalidate @submit.prevent="confirm">
          <p class="oa-2fa-desc">{{ t('twoFactorRegenerateHint') }} {{ t('twoFactorConfirmWithCode') }}</p>
          <div class="oa-2fa-confirm-row oa-field">
            <input
              v-model="code"
              class="oa-2fa-code"
              type="text"
              autocomplete="one-time-code"
              spellcheck="false"
              maxlength="16"
              placeholder="000000"
              :aria-label="t('twoFactorCodeLabel')"
            >
            <button type="button" class="oa-btn" :disabled="busy" @click="action = ''">{{ t('cancel') }}</button>
            <button type="submit" class="oa-btn primary" :disabled="busy || !code.trim()">
              {{ t('twoFactorRegenerateShort') }}
            </button>
          </div>
        </form>
        <div v-if="fresh.length" class="oa-2fa-confirm">
          <p class="oa-2fa-desc">{{ t('twoFactorSaveBody') }}</p>
          <div class="oa-2fa-codes-box">
            <ol class="oa-2fa-codes">
              <li v-for="entry in fresh" :key="entry">{{ entry }}</li>
            </ol>
            <label class="oa-checkbox-field">
              <input v-model="savedCodes" type="checkbox">
              <span>{{ t('twoFactorSavedConfirm') }}</span>
            </label>
            <div class="oa-2fa-codes-actions">
              <button type="button" class="oa-btn" @click="copyFresh">
                <IconCheck v-if="copied" :size="14" />
                <IconCopy v-else :size="14" />
                {{ copied ? t('copied') : t('copy') }}
              </button>
              <button type="button" class="oa-btn primary" :disabled="!savedCodes" @click="finishFresh">
                {{ t('twoFactorFinish') }}
              </button>
            </div>
          </div>
        </div>

        <div v-if="!status.mandatory" class="oa-2fa-row">
          <div class="oa-2fa-row-text">
            <span class="oa-2fa-row-title">{{ t('twoFactorTurnOff') }}</span>
            <span class="oa-2fa-row-meta">{{ t('twoFactorTurnOffHint') }}</span>
          </div>
          <button type="button" class="oa-btn" :class="{ primary: action === 'disable' }" @click="open('disable')">
            {{ t('twoFactorTurnOffShort') }}
          </button>
        </div>
        <form v-if="action === 'disable'" class="oa-2fa-confirm" novalidate @submit.prevent="confirm">
          <p class="oa-2fa-desc">{{ t('twoFactorConfirmWithCode') }}</p>
          <div class="oa-2fa-confirm-row oa-field">
            <input
              v-model="code"
              class="oa-2fa-code"
              type="text"
              autocomplete="one-time-code"
              spellcheck="false"
              maxlength="16"
              placeholder="000000"
              :aria-label="t('twoFactorCodeLabel')"
            >
            <button type="button" class="oa-btn" :disabled="busy" @click="action = ''">{{ t('cancel') }}</button>
            <button type="submit" class="oa-btn primary" :disabled="busy || !code.trim()">
              {{ t('twoFactorTurnOffShort') }}
            </button>
          </div>
        </form>
      </template>

      <p v-if="notice" class="oa-2fa-flash ok" role="status">{{ notice }}</p>
      <p v-if="flash" class="oa-2fa-flash" role="alert">{{ flash }}</p>
    </section>
    <p v-else-if="flash" class="oa-drawer-flash visible" role="alert">{{ flash }}</p>
  </div>

  <div v-show="matchesSettings(props.query, 'devices')" class="oa-settings-panel">
    <h2 class="oa-admin-section-title">{{ t('secDevices') }}</h2>

    <section class="oa-2fa-panel">
      <header class="oa-2fa-head">
        <span class="oa-2fa-head-mark"><IconAuto :size="18" /></span>
        <div class="oa-2fa-head-text">
          <div class="oa-2fa-head-title"><span>{{ t('secDevices') }}</span></div>
          <p class="oa-2fa-head-meta">{{ t('devicesIntro') }}</p>
        </div>
      </header>

      <p v-if="devices === null && !devicesFlash" class="oa-2fa-note">{{ t('loading') }}</p>

      <template v-else-if="devices">
        <div v-for="device in devices" :key="device.id" class="oa-2fa-row">
          <div class="oa-2fa-row-text">
            <span class="oa-2fa-row-title">
              {{ describeUserAgent(device.user_agent) }}
              <OaBadge v-if="device.current" tone="default">{{ t('deviceThisDevice') }}</OaBadge>
            </span>
            <span class="oa-2fa-row-meta">
              {{ device.ip }} · {{ t('deviceLastActive', { when: relativeTime(device.last_seen_at) }) }}
            </span>
          </div>
          <OaConfirmButton
            v-if="!device.current"
            class="oa-btn"
            :label="t('deviceSignOut')"
            :armed-label="t('confirmWord')"
            :armed-title="t('deviceSignOutConfirm')"
            :resting-title="t('deviceSignOut')"
            :disabled="revokingID === device.id"
            @confirm="signOutDevice(device.id)"
          />
        </div>

        <div v-if="devices.length > 1" class="oa-2fa-row">
          <div class="oa-2fa-row-text">
            <span class="oa-2fa-row-title">{{ t('deviceSignOutOthers') }}</span>
          </div>
          <OaConfirmButton
            class="oa-btn"
            :label="t('deviceSignOutOthers')"
            :armed-label="t('confirmWord')"
            :armed-title="t('deviceSignOutOthersConfirm')"
            :resting-title="t('deviceSignOutOthers')"
            :disabled="revokingOthers"
            @confirm="signOutOthers"
          />
        </div>
      </template>

      <p v-if="devicesNotice" class="oa-2fa-flash ok" role="status">{{ devicesNotice }}</p>
      <p v-if="devicesFlash" class="oa-2fa-flash" role="alert">{{ devicesFlash }}</p>
    </section>
  </div>
</template>
