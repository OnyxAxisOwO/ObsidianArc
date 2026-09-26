<script setup lang="ts">
// Everything between the front door and an account.
//
// Split out of the settings screen because these are one subject and it was
// not: whether registration is open, what a new account must supply, how fast
// addresses may open them, whether a browser has to prove itself, and whether
// a model is asked to look at the result. An operator dealing with a wave of
// junk accounts opens one page, not seven sections of another.

import { computed, onMounted, ref } from 'vue';
import { adminApi, type AdminMailSettings, type AdminModel, type AdminUserCheckSettings, type Group, type SecurityEvent, type SignInApplication, type TwoFactorAdoption } from '@/admin/api';
import { fetchSite } from '@/api/auth';
import { ApiError } from '@/api/client';
import OaPagination from '@/components/OaPagination.vue';
import type { PageState } from '@/components/table-types';
import OaBadge from '@/components/OaBadge.vue';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import AdminControlCard from './AdminControlCard.vue';
import AdminWorkbench from './AdminWorkbench.vue';
import type { WorkbenchGroup } from './workbench';
import { useSettingsDraft } from './settingsDraft';
import { IconUsers, IconLock, IconSpark, IconFile, IconSliders, IconKey, IconGithub, IconGoogle, IconCopy, IconCheck, IconShield, IconMessage } from '@/icons';
import OaNumberField from '@/components/OaNumberField.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTextArea from '@/components/OaTextArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t, type StringKey } from '@/composables/useI18n';
import { copyToClipboard } from '@/chat/markdown';
import { initials } from '@/lib/account';
import { absoluteTime } from '@/lib/format';
import { currentUser, site } from '@/stores/session';
import { maskUser, maskLog, maskCredential } from '@/admin/safeMode';
import AdminFailure from './AdminFailure.vue';
import { minutesLabel } from './shared';
import { useAdminView } from './adminView';

const view = useAdminView();
view.setTitle(t('navSecurity'), t('securitySubtitle'));

const error = ref('');
const loaded = ref(false);
const mailConfigured = ref(false);
const mailLoaded = ref(false);
const mailLoadError = ref<StringKey | null>(null);
const mailBusy = ref(false);
const mailTestBusy = ref(false);
const mailFlash = ref<StringKey | null>(null);
const mailFlashOK = ref(false);
const mailTestTo = ref('');
const savedMail = ref<AdminMailSettings>({
  host: '', port: 587, username: '', from: '', implicit_tls: false, public_url: '', password_set: false,
});
const mailForm = ref({ ...savedMail.value, password: '' });
const mailSettingsDirty = computed(() => {
  const form = mailForm.value;
  const saved = savedMail.value;
  return form.host.trim() !== saved.host || (form.port ?? 587) !== saved.port ||
    form.username.trim() !== saved.username || form.from.trim() !== saved.from ||
    form.implicit_tls !== saved.implicit_tls || form.public_url.trim() !== saved.public_url ||
    form.password !== '';
});
const userCheckLoaded = ref(false);
const userCheckLoadError = ref<StringKey | null>(null);
const userCheckBusy = ref(false);
const userCheckTestBusy = ref(false);
const userCheckFlash = ref<StringKey | null>(null);
const userCheckFlashOK = ref(false);
const userCheckTestEmail = ref('');
const savedUserCheck = ref<AdminUserCheckSettings>({
  enabled: false, exempt_domains: [], failure_mode: 'reject', api_key_set: false,
});
const userCheckForm = ref({
  enabled: false, exempt_domains: '', failure_mode: 'reject' as 'allow' | 'reject', api_key_set: false, api_key: '',
});
const groups = ref<Pick<Group, 'id' | 'name'>[]>([]);
const models = ref<Pick<AdminModel, 'id' | 'display_name' | 'model_id' | 'enabled' | 'provider_name'>[]>([]);
const flash = ref('');
const saveLabel = ref('');
const busy = ref(false);
const events = ref<SecurityEvent[]>([]);
const eventsTotal = ref(0);
const eventPage = ref<PageState>({ page: 1, pageSize: 20 });
let eventRequest = 0;
function changeEvents(next: PageState): void { eventPage.value = next; void loadEvents(); }
const eventsLoading = ref(false);

const form = ref({
  registration: false,
  defaultGroup: '',
  requireEmail: false,
  verifyEmail: false,
  emailDomains: '',
  qqRequirement: 'off',
  perMinute: 0 as number | null,
  perHour: 0 as number | null,
  perIP: 0 as number | null,
  ipWindow: 60 as number | null,
  turnstileSiteKey: '',
  turnstileSecret: '',
  turnstileSecretHint: '',
  turnstileOnLogin: false,
  turnstileOnSignup: false,
  turnstileOnAPIKey: false,
  turnstileOnRedeem: false,
  turnstileOnFeedback: false,
  chatChallengeRequests: 0 as number | null,
  chatChallengeWindowSecs: 60 as number | null,
  chatChallengeClearMins: 30 as number | null,
  reviewEnabled: false,
  reviewModel: '',
  reviewMode: 'normal',
  reviewRestrictHours: 24 as number | null,
  reviewRefusal: '',
  githubEnabled: false,
  githubClientID: '',
  githubSecret: '',
  githubSecretHint: '',
  googleEnabled: false,
  googleClientID: '',
  googleSecret: '',
  googleSecretHint: '',
  oauthAllowSignup: true,
  oauthLinkByEmail: true,
  twoFactorPolicy: 'optional',
  twoFactorIssuer: '',
  twoFactorRememberDays: 0 as number | null,
  backofficeMode: 'off',
  backofficeMinutes: 15 as number | null,
  backofficeNetwork: false,
  backofficeBrowser: false,
  newDeviceEmail: false,
});

// --- two-step verification ------------------------------------------------------
//
// The policy is a field like the others and saves with them. The adoption
// figures beside it are read-only, and are what an operator looks at before
// making the policy stricter: how many people it would stop at the door.

const adoption = ref<TwoFactorAdoption | null>(null);

async function loadAdoption(): Promise<void> {
  try {
    const result = await adminApi.twoFactorAdoption();
    // An older server answers with less; the list is what the card iterates.
    adoption.value = { ...result, admins_without: result.admins_without ?? [] };
  } catch {
    // The figures are context for the policy, not the policy; a failure here
    // should not take the settings form down with it.
  }
}

/** Anything above optional is refused for an operator without a second
 *  step of their own, so the form says so before they try. */
const selfWithout = computed(() => !(currentUser.value?.two_factor_at ?? 0));

const policyHint = computed(() => {
  switch (form.value.twoFactorPolicy) {
    case 'backoffice': return t('twoFactorPolicyBackofficeHint');
    case 'admins': return t('twoFactorPolicyAdminsHint');
    case 'everyone': return t('twoFactorPolicyEveryoneHint');
    default: return t('twoFactorPolicyOptionalHint');
  }
});

/** What the chosen mode does, and what the minutes mean in it — the same
 *  number is an idle limit in two modes and a period in the third. */
const backofficeHint = computed(() => {
  switch (form.value.backofficeMode) {
    case 'visit': return t('backofficeVerifyVisitHint');
    case 'idle': return t('backofficeVerifyIdleHint');
    case 'interval': return t('backofficeVerifyIntervalHint');
    default: return t('backofficeVerifyOffHint');
  }
});
/** The minutes restated as hours or days, where they come out even — 1440
 *  is easier to trust as "1 day" — and nothing where they would only repeat. */
const backofficeMinutesHint = computed(() => {
  const minutes = form.value.backofficeMinutes ?? 15;
  const label = minutesLabel(minutes);
  return label === t('durationMinutes', { count: minutes }) ? undefined : t('backofficeMinutesEquals', { duration: label });
});
const backofficeMinutesLabel = computed(() => {
  switch (form.value.backofficeMode) {
    case 'idle': return t('backofficeMinutesIdle');
    case 'interval': return t('backofficeMinutesInterval');
    default: return t('backofficeMinutesVisit');
  }
});

function share(part: number, whole: number): string {
  return whole > 0 ? `${Math.round((part / whole) * 100)}%` : '—';
}



// --- the applications that may sign people in with an account here -------------
//
// Not a settings card: these are rows rather than fields, and they commit on
// their own buttons. They live on this screen anyway because they are the
// same subject as everything else on it — who gets in, and how.

const applications = ref<SignInApplication[]>([]);
const issuer = ref('');
const appFlash = ref('');
const appBusy = ref(false);
/** The client secret, for the one moment it exists. */
const freshSecret = ref('');
const draft = ref({ name: '', description: '', redirects: '', public: false, trusted: false });
/** The value that was last copied, so the control can say it landed. */
const copied = ref('');

function copy(value: string): void {
  void copyToClipboard(value).then((ok) => {
    if (!ok) return;
    copied.value = value;
    window.setTimeout(() => {
      if (copied.value === value) copied.value = '';
    }, 1500);
  });
}

/**
 * What to paste into the provider's own console.
 *
 * Built from the server's own idea of this instance's address, not from the
 * address bar. The two can differ — a proxy chain that loses the original
 * scheme leaves the server believing it is http where the browser knows it is
 * https — and when they differ it is the server's value that the provider is
 * shown at the exchange. A callback that differs from the registered one by a
 * scheme is refused with an error that names neither, so the screen has to
 * show the one that will actually be sent.
 *
 * The address bar is the fallback for the moment before the applications
 * list has answered.
 */
function callbackURL(provider: string): string {
  return `${issuer.value || window.location.origin}/api/auth/oauth/callback/${provider}`;
}

async function loadApplications(): Promise<void> {
  try {
    const result = await adminApi.applications();
    applications.value = result.applications ?? [];
    issuer.value = result.issuer;
  } catch (failure) {
    appFlash.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

function registerApplication(): void {
  if (appBusy.value) return;
  appBusy.value = true;
  appFlash.value = '';
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
      return loadApplications();
    })
    .catch((failure: unknown) => {
      appFlash.value = failure instanceof ApiError ? failure.message : String(failure);
    })
    .finally(() => { appBusy.value = false; });
}

function toggleApplication(app: SignInApplication, disabled: boolean): void {
  appBusy.value = true;
  appFlash.value = '';
  void adminApi.updateApplication(app.id, { disabled })
    .then(loadApplications)
    .catch((failure: unknown) => {
      appFlash.value = failure instanceof ApiError ? failure.message : String(failure);
    })
    .finally(() => { appBusy.value = false; });
}

function rotateApplication(app: SignInApplication): void {
  appBusy.value = true;
  appFlash.value = '';
  freshSecret.value = '';
  void adminApi.rotateApplicationSecret(app.id)
    .then((result) => { freshSecret.value = result.client_secret; })
    .catch((failure: unknown) => {
      appFlash.value = failure instanceof ApiError ? failure.message : String(failure);
    })
    .finally(() => { appBusy.value = false; });
}

function removeApplication(app: SignInApplication): void {
  appBusy.value = true;
  appFlash.value = '';
  void adminApi.deleteApplication(app.id)
    .then(loadApplications)
    .catch((failure: unknown) => {
      appFlash.value = failure instanceof ApiError ? failure.message : String(failure);
    })
    .finally(() => { appBusy.value = false; });
}

// Trying the reviewer on an account that is not being created.
const trial = ref({ username: '', email: '', qq: '', answer: '', running: false });

const enabledModels = computed(() => models.value.filter((entry) => entry.enabled));

/**
 * Only this page's keys. The settings endpoint writes what it is given and
 * leaves the rest alone, which is what lets two screens edit one store
 * without either one reverting the other's fields.
 */
function collect(): Record<string, string> {
  return {
    'registration.enabled': String(form.value.registration),
    'registration.default_group': form.value.defaultGroup,
    'registration.require_email': String(form.value.requireEmail),
    'registration.verify_email': String(form.value.verifyEmail),
    'registration.email_domains': form.value.emailDomains.trim(),
    'registration.qq_requirement': form.value.qqRequirement,
    'registration.per_minute': String(form.value.perMinute ?? 0),
    'registration.per_hour': String(form.value.perHour ?? 0),
    'registration.per_ip': String(form.value.perIP ?? 0),
    'registration.per_ip_window_minutes': String(form.value.ipWindow ?? 60),
    'turnstile.site_key': form.value.turnstileSiteKey.trim(),
    // Empty keeps what is stored: the field was never shown the secret, so
    // sending its emptiness back would erase it.
    'turnstile.secret_key': form.value.turnstileSecret.trim(),
    'turnstile.on_login': String(form.value.turnstileOnLogin),
    'turnstile.on_signup': String(form.value.turnstileOnSignup),
    'turnstile.on_api_key': String(form.value.turnstileOnAPIKey),
    'turnstile.on_redeem': String(form.value.turnstileOnRedeem),
    'turnstile.on_feedback': String(form.value.turnstileOnFeedback),
    'security.chat_challenge_requests': String(form.value.chatChallengeRequests ?? 0),
    'security.chat_challenge_window_seconds': String(form.value.chatChallengeWindowSecs ?? 60),
    'security.chat_challenge_clear_minutes': String(form.value.chatChallengeClearMins ?? 30),
    'security.signup_review': String(form.value.reviewEnabled),
    'security.signup_review_model': form.value.reviewModel,
    'security.signup_review_mode': form.value.reviewMode,
    'security.signup_review_restrict_hours': String(form.value.reviewRestrictHours ?? 24),
    'security.signup_review_refusal': form.value.reviewRefusal.trim(),
    'oauth.github_enabled': String(form.value.githubEnabled),
    'oauth.github_client_id': form.value.githubClientID.trim(),
    // Empty keeps what is stored, the same bargain the Turnstile secret
    // makes: the field was never shown the secret, so sending its emptiness
    // back would erase it.
    'oauth.github_client_secret': form.value.githubSecret.trim(),
    'oauth.google_enabled': String(form.value.googleEnabled),
    'oauth.google_client_id': form.value.googleClientID.trim(),
    'oauth.google_client_secret': form.value.googleSecret.trim(),
    'oauth.allow_signup': String(form.value.oauthAllowSignup),
    'oauth.link_by_email': String(form.value.oauthLinkByEmail),
    'security.two_factor_policy': form.value.twoFactorPolicy,
    'security.two_factor_issuer': form.value.twoFactorIssuer.trim(),
    'security.two_factor_remember_days': String(form.value.twoFactorRememberDays ?? 0),
    'security.two_factor_backoffice_mode': form.value.backofficeMode,
    'security.two_factor_backoffice_minutes': String(form.value.backofficeMinutes ?? 15),
    'security.two_factor_backoffice_network': String(form.value.backofficeNetwork),
    'security.two_factor_backoffice_browser': String(form.value.backofficeBrowser),
    'security.new_device_email': String(form.value.newDeviceEmail),
  };
}

const { dirty, accept } = useSettingsDraft(collect);

async function save(): Promise<void> {
  if (!loaded.value || error.value || busy.value) return;
  const values = collect();
  busy.value = true;
  saveLabel.value = t('saving');
  flash.value = '';
  try {
    await adminApi.saveSettings(values);
    accept(values);
    try {
      site.value = await fetchSite();
    } catch {
      // The setting is already saved. A failed public-settings refresh should
      // not report that write as failed; the next page load will fetch it.
    }
    saveLabel.value = t('saved');
    window.setTimeout(() => { saveLabel.value = ''; }, 1500);
    void loadAdoption();
  } catch (failure) {
    flash.value = failure instanceof ApiError && failure.code === 'two_factor_self'
      ? t('twoFactorPolicySelfRefused')
      : failure instanceof ApiError ? failure.message : String(failure);
    saveLabel.value = '';
  } finally {
    busy.value = false;
  }
}

/**
 * It exists because "the review is not working" and "the review is working
 * and being generous" look identical from outside: both are a registration
 * that went through. Type the account that got past it and read what the
 * model actually said — including that it could not be reached, which is the
 * state in which everything is allowed.
 */
function runTrial(): void {
  trial.value.running = true;
  trial.value.answer = t('reviewTrying');
  void adminApi.tryReview({
    username: trial.value.username.trim(),
    email: trial.value.email.trim(),
    qq: trial.value.qq.trim(),
    // What a browser would have sent, so the answer is about the details and
    // not about a missing user agent.
    user_agent: navigator.userAgent,
  })
    .then((result) => {
      trial.value.answer = !result.ran
        ? t('reviewTryBroken', { decision: reviewDecision(result.decision), reason: result.reason })
        : t(result.decision === 'allow'
          ? 'reviewTryAllowed'
          : result.decision === 'restrict' ? 'reviewTryRestricted' : 'reviewTryRefused',
        { reason: result.reason });
    })
    .catch((failure: unknown) => {
      trial.value.answer = failure instanceof ApiError ? failure.message : String(failure);
    })
    .finally(() => { trial.value.running = false; });
}

function reviewDecision(decision: string): string {
  if (decision === 'allow') return t('securityDecisionAllow');
  if (decision === 'refuse') return t('securityDecisionRefuse');
  if (decision === 'required') return t('securityDecisionRequired');
  if (decision === 'passed') return t('securityDecisionPassed');
  if (decision === 'failed') return t('securityDecisionFailed');
  if (decision === 'enabled') return t('securityDecisionEnabled');
  if (decision === 'disabled') return t('securityDecisionDisabled');
  if (decision === 'reset') return t('securityDecisionReset');
  if (decision === 'recovery_used') return t('securityDecisionRecoveryUsed');
  if (decision === 'recovery_regenerated') return t('securityDecisionRecoveryRegenerated');
  if (decision === 'restrict') return t('securityDecisionRestrict');
  // A decision this screen has no words for — a console command's outcome
  // code, say — is shown as it was recorded rather than as a restriction it
  // never was.
  return decision;
}

function eventLabel(event: string): string {
  if (event === 'signup_review') return t('securityEventSignupReview');
  if (event === 'api_restriction') return t('securityEventAPIRestriction');
  if (event === 'api_restriction_lifted') return t('securityEventAPIRestrictionLifted');
  if (event === 'chat_challenge') return t('securityEventChatChallenge');
  if (event === 'two_factor') return t('securityEventTwoFactor');
  if (event === 'new_device') return t('securityEventNewDevice');
  if (event === 'console_command') return t('securityEventConsoleCommand');
  return event;
}

async function loadEvents(): Promise<void> {
  const ticket = ++eventRequest;
  eventsLoading.value = true;
  try {
    const result = await adminApi.securityEvents(`?limit=${eventPage.value.pageSize}&offset=${(eventPage.value.page - 1) * eventPage.value.pageSize}`);
    if (ticket !== eventRequest) return;
    events.value = result.events ?? [];
    eventsTotal.value = result.total;
  } catch (failure) {
    flash.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    if (ticket === eventRequest) eventsLoading.value = false;
  }
}

async function loadMail(): Promise<void> {
  mailLoadError.value = null;
  mailLoaded.value = false;
  try {
    const settings = await adminApi.mail();
    savedMail.value = settings;
    mailForm.value = { ...settings, password: '' };
    mailLoaded.value = true;
  } catch (failure) {
    mailLoadError.value = mailErrorText(failure, 'load');
  }
}

function mailErrorText(failure: unknown, action: 'load' | 'save' | 'test'): StringKey {
  if (!(failure instanceof ApiError)) return 'failed';
  if (failure.code === 'mail_test_cooldown') return 'mailTestCooldown';
  if (failure.code === 'mail_unavailable') return 'mailDeliveryUnavailable';
  if (failure.status === 400) return action === 'test' ? 'mailTestInvalid' : 'mailSettingsInvalid';
  return 'failed';
}

async function saveMail(): Promise<void> {
  if (!mailLoaded.value || mailBusy.value) return;
  mailBusy.value = true;
  mailFlash.value = null;
  mailFlashOK.value = false;
  try {
    const settings = await adminApi.saveMail({
      host: mailForm.value.host.trim(),
      port: mailForm.value.port ?? 587,
      username: mailForm.value.username.trim(),
      from: mailForm.value.from.trim(),
      implicit_tls: mailForm.value.implicit_tls,
      public_url: mailForm.value.public_url.trim(),
      // Empty keeps the existing credential; only the explicit action below clears it.
      password: mailForm.value.password,
      clear_password: false,
    });
    savedMail.value = settings;
    mailForm.value = { ...settings, password: '' };
    mailConfigured.value = !!(settings.host && settings.port &&
      (settings.from || settings.username.includes('@')) && settings.public_url);
    mailFlash.value = 'mailSaved';
    mailFlashOK.value = true;
  } catch (failure) {
    mailFlash.value = mailErrorText(failure, 'save');
  } finally {
    mailBusy.value = false;
  }
}

async function clearMailPassword(): Promise<void> {
  if (!mailLoaded.value || mailBusy.value || !savedMail.value.password_set) return;
  mailBusy.value = true;
  mailFlash.value = null;
  mailFlashOK.value = false;
  try {
    const settings = await adminApi.saveMail({
      host: savedMail.value.host,
      port: savedMail.value.port,
      username: savedMail.value.username,
      from: savedMail.value.from,
      implicit_tls: savedMail.value.implicit_tls,
      public_url: savedMail.value.public_url,
      password: '',
      clear_password: true,
    });
    savedMail.value = settings;
    mailForm.value.password_set = settings.password_set;
    mailForm.value.password = '';
    mailFlash.value = 'mailPasswordCleared';
    mailFlashOK.value = true;
  } catch (failure) {
    mailFlash.value = mailErrorText(failure, 'save');
  } finally {
    mailBusy.value = false;
  }
}

async function sendMailTest(): Promise<void> {
  if (mailBusy.value || mailTestBusy.value || mailSettingsDirty.value || !mailTestTo.value.trim()) return;
  mailTestBusy.value = true;
  mailFlash.value = null;
  mailFlashOK.value = false;
  try {
    await adminApi.testMail(mailTestTo.value.trim());
    mailFlash.value = 'mailTestSent';
    mailFlashOK.value = true;
  } catch (failure) {
    mailFlash.value = mailErrorText(failure, 'test');
  } finally {
    mailTestBusy.value = false;
  }
}

async function loadUserCheck(): Promise<void> {
  userCheckLoadError.value = null;
  userCheckLoaded.value = false;
  try {
    const settings = await adminApi.userCheck();
    savedUserCheck.value = settings;
    userCheckForm.value = {
      enabled: settings.enabled,
      exempt_domains: settings.exempt_domains.join('\n'),
      failure_mode: settings.failure_mode,
      api_key_set: settings.api_key_set,
      api_key: '',
    };
    userCheckLoaded.value = true;
  } catch (failure) {
    userCheckLoadError.value = userCheckErrorText(failure, 'load');
  }
}

function userCheckErrorText(failure: unknown, action: 'load' | 'save' | 'test'): StringKey {
  if (!(failure instanceof ApiError)) return 'failed';
  if (failure.code === 'usercheck_test_cooldown') return 'userCheckTestCooldown';
  if (failure.code === 'unavailable' || failure.code === 'email_screening_unavailable') {
    return 'emailScreeningUnavailable';
  }
  if (failure.status === 400) return action === 'test' ? 'userCheckTestInvalid' : 'userCheckSettingsInvalid';
  return 'failed';
}

function userCheckDomains(): string[] {
  return [...new Set(userCheckForm.value.exempt_domains
    .split(/[\n,]/)
    .map((domain) => domain.trim().toLowerCase())
    .filter(Boolean))];
}

async function saveUserCheck(): Promise<void> {
  if (!userCheckLoaded.value || userCheckBusy.value) return;
  if (userCheckForm.value.enabled && !userCheckForm.value.api_key_set && !userCheckForm.value.api_key.trim()) {
    userCheckFlash.value = 'userCheckAPIKeyRequired';
    userCheckFlashOK.value = false;
    return;
  }
  userCheckBusy.value = true;
  userCheckFlash.value = null;
  userCheckFlashOK.value = false;
  try {
    const settings = await adminApi.saveUserCheck({
      enabled: userCheckForm.value.enabled,
      exempt_domains: userCheckDomains(),
      failure_mode: userCheckForm.value.failure_mode,
      api_key: userCheckForm.value.api_key,
      clear_api_key: false,
    });
    savedUserCheck.value = settings;
    userCheckForm.value = {
      enabled: settings.enabled,
      exempt_domains: settings.exempt_domains.join('\n'),
      failure_mode: settings.failure_mode,
      api_key_set: settings.api_key_set,
      api_key: '',
    };
    userCheckFlash.value = 'userCheckSaved';
    userCheckFlashOK.value = true;
  } catch (failure) {
    userCheckFlash.value = userCheckErrorText(failure, 'save');
  } finally {
    userCheckBusy.value = false;
  }
}

async function clearUserCheckAPIKey(): Promise<void> {
  if (!userCheckLoaded.value || userCheckBusy.value || !savedUserCheck.value.api_key_set) return;
  userCheckBusy.value = true;
  userCheckFlash.value = null;
  userCheckFlashOK.value = false;
  try {
    const settings = await adminApi.saveUserCheck({
      exempt_domains: savedUserCheck.value.exempt_domains,
      failure_mode: savedUserCheck.value.failure_mode,
      enabled: false,
      api_key: '',
      clear_api_key: true,
    });
    savedUserCheck.value = settings;
    userCheckForm.value.enabled = false;
    userCheckForm.value.api_key_set = false;
    userCheckForm.value.api_key = '';
    userCheckFlash.value = 'userCheckAPIKeyCleared';
    userCheckFlashOK.value = true;
  } catch (failure) {
    userCheckFlash.value = userCheckErrorText(failure, 'save');
  } finally {
    userCheckBusy.value = false;
  }
}

async function sendUserCheckTest(): Promise<void> {
  if (userCheckTestBusy.value || !userCheckTestEmail.value.trim() || !userCheckForm.value.api_key_set) return;
  userCheckTestBusy.value = true;
  userCheckFlash.value = null;
  userCheckFlashOK.value = false;
  try {
    const result = await adminApi.testUserCheck(userCheckTestEmail.value.trim());
    userCheckFlash.value = result.skipped
      ? 'userCheckTestSkipped'
      : result.disposable ? 'userCheckTestDisposable' : 'userCheckTestClean';
    userCheckFlashOK.value = !result.disposable;
  } catch (failure) {
    userCheckFlash.value = userCheckErrorText(failure, 'test');
  } finally {
    userCheckTestBusy.value = false;
  }
}

async function load(): Promise<void> {
  error.value = '';
  try {
    // The models come along because one of these settings is which model
    // reviews a sign-up, and a select needs its options. The groups arrive
    // with the settings already.
    const [data, modelsResult] = await Promise.all([
      adminApi.settings(), adminApi.modelOptions(), loadEvents(), loadApplications(), loadAdoption(), loadMail(), loadUserCheck(),
    ]);
    const values = data.settings;
    mailConfigured.value = data.mail_configured ?? false;
    groups.value = data.groups ?? [];
    models.value = modelsResult.models;


    form.value = {
      registration: values['registration.enabled'] === 'true',
      defaultGroup: values['registration.default_group'] ?? '',
      requireEmail: values['registration.require_email'] === 'true',
      verifyEmail: values['registration.verify_email'] === 'true',
      emailDomains: values['registration.email_domains'] ?? '',
      qqRequirement: values['registration.qq_requirement'] ?? 'off',
      perMinute: Number(values['registration.per_minute'] ?? 0),
      perHour: Number(values['registration.per_hour'] ?? 0),
      perIP: Number(values['registration.per_ip'] ?? 0),
      ipWindow: Number(values['registration.per_ip_window_minutes'] ?? 60),
      turnstileSiteKey: values['turnstile.site_key'] ?? '',
      turnstileSecret: '',
      turnstileSecretHint: values['turnstile.secret_key'] ?? '',
      turnstileOnLogin: values['turnstile.on_login'] === 'true',
      turnstileOnSignup: values['turnstile.on_signup'] === 'true',
      turnstileOnAPIKey: values['turnstile.on_api_key'] === 'true',
      turnstileOnRedeem: values['turnstile.on_redeem'] === 'true',
      turnstileOnFeedback: values['turnstile.on_feedback'] === 'true',
      chatChallengeRequests: Number(values['security.chat_challenge_requests'] ?? 0),
      chatChallengeWindowSecs: Number(values['security.chat_challenge_window_seconds'] ?? 60),
      chatChallengeClearMins: Number(values['security.chat_challenge_clear_minutes'] ?? 30),
      reviewEnabled: values['security.signup_review'] === 'true',
      reviewModel: values['security.signup_review_model'] ?? '',
      reviewMode: values['security.signup_review_mode'] ?? 'normal',
      reviewRestrictHours: Number(values['security.signup_review_restrict_hours'] ?? 24),
      reviewRefusal: values['security.signup_review_refusal'] ?? '',
      githubEnabled: values['oauth.github_enabled'] === 'true',
      githubClientID: values['oauth.github_client_id'] ?? '',
      githubSecret: '',
      githubSecretHint: values['oauth.github_client_secret'] ?? '',
      googleEnabled: values['oauth.google_enabled'] === 'true',
      googleClientID: values['oauth.google_client_id'] ?? '',
      googleSecret: '',
      googleSecretHint: values['oauth.google_client_secret'] ?? '',
      oauthAllowSignup: (values['oauth.allow_signup'] ?? 'true') === 'true',
      oauthLinkByEmail: (values['oauth.link_by_email'] ?? 'true') === 'true',
      twoFactorPolicy: values['security.two_factor_policy'] ?? 'optional',
      twoFactorIssuer: values['security.two_factor_issuer'] ?? '',
      twoFactorRememberDays: Number(values['security.two_factor_remember_days'] ?? 0),
      backofficeMode: values['security.two_factor_backoffice_mode'] ?? 'off',
      backofficeMinutes: Number(values['security.two_factor_backoffice_minutes'] ?? 15),
      backofficeNetwork: values['security.two_factor_backoffice_network'] === 'true',
      backofficeBrowser: values['security.two_factor_backoffice_browser'] === 'true',
      newDeviceEmail: values['security.new_device_email'] === 'true',
    };
    accept();
  } catch (failure) {
    error.value = failure instanceof Error ? failure.message : String(failure);
  } finally {
    loaded.value = true;
  }
}

const categories: WorkbenchGroup[] = [
  { id: 'accounts', label: 'controlAccounts', hint: 'controlAccountsHint', icon: IconUsers, sections: ['secAccounts', 'secRegistration', 'secRegistrationLimits'] },
  { id: 'verification', label: 'controlVerification', hint: 'controlVerificationHint', icon: IconLock, sections: ['secTurnstile', 'secVerificationScenes', 'secChatChallenge', 'secMail', 'secUserCheck'] },
  { id: 'review', label: 'controlReview', hint: 'controlReviewHint', icon: IconSpark, sections: ['secSignupReview', 'secReviewTrial'] },
  { id: 'signin', label: 'controlSignIn', hint: 'controlSignInHint', icon: IconGithub, sections: ['secOAuth', 'secApplications'] },
  { id: 'twofactor', label: 'controlTwoFactor', hint: 'controlTwoFactorHint', icon: IconShield, sections: ['secTwoFactorPolicy', 'secBackofficeVerify', 'secTwoFactorAdoption'] },
  { id: 'events', label: 'controlEvents', hint: 'controlEventsHint', icon: IconFile, sections: ['secSecurityLog'] },
];

const columns: [string[], string[]] = [['secAccounts', 'secRegistrationLimits', 'secTurnstile', 'secChatChallenge', 'secSignupReview', 'secTwoFactorPolicy', 'secBackofficeVerify'], ['secRegistration', 'secVerificationScenes', 'secReviewTrial', 'secTwoFactorAdoption', 'secMail', 'secUserCheck']];

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
  <AdminWorkbench v-else page="security" :groups="categories" :columns="columns">
    <template #left="{ visible }">
      <AdminControlCard id="secAccounts" v-show="visible('secAccounts')" :title="t('secAccounts')" :icon="IconUsers">
        <OaSwitchField
          v-model="form.registration"
          :label="t('anyoneCanRegister')"
          :hint="t('anyoneCanRegisterHint')"
        />
        <OaSelectField
          v-model="form.defaultGroup"
          :label="t('newAccountsJoin')"
          :options="[
            { value: '', label: t('theDefaultGroup') },
            ...groups.map((group) => ({ value: group.id, label: group.name })),
          ]"
        />
      </AdminControlCard>
      <AdminControlCard id="secRegistrationLimits" v-show="visible('secRegistrationLimits')" :title="t('controlRegistrationLimits')" :icon="IconSliders" :hint="t('controlRegistrationLimitsHint')">
        <OaNumberField v-model="form.perMinute" :label="t('signupsPerMinute')" :min="0" :max="1000" />
        <OaNumberField
          v-model="form.perHour"
          :label="t('signupsPerHour')"
          :min="0"
          :max="10000"
          :hint="t('signupThrottleHint')"
        />
        <OaNumberField v-model="form.perIP" :label="t('signupsPerIP')" :min="0" :hint="t('signupsPerIPHint')" />
        <OaNumberField
          v-model="form.ipWindow"
          :label="t('signupsIPWindow')"
          :min="1"
          :hint="t('signupsIPWindowHint')"
        />
      </AdminControlCard>
      <AdminControlCard id="secTurnstile" v-show="visible('secTurnstile')" :title="t('secTurnstile')" :icon="IconKey" :hint="t('turnstileHint')">
        <OaTextField
          v-model="form.turnstileSiteKey"
          :label="t('turnstileSiteKey')"
          placeholder="0x4AAAAAAA…"
          :hint="t('turnstileSiteKeyHint')"
          monospace
        />
        <OaTextField
          v-model="form.turnstileSecret"
          type="password"
          :label="t('turnstileSecretKey')"
          :placeholder="form.turnstileSecretHint || '0x4AAAAAAA…'"
          :hint="t('turnstileSecretHint')"
          monospace
        />
      </AdminControlCard>
      <AdminControlCard id="secChatChallenge" v-show="visible('secChatChallenge')" :title="t('controlChatChallenge')" :icon="IconSpark" :hint="t('controlChatChallengeHint')">
        <OaNumberField
          v-model="form.chatChallengeRequests"
          :label="t('chatChallengeRequests')"
          :hint="t('chatChallengeRequestsHint')"
          :min="0"
          :max="1000"
        />
        <template v-if="(form.chatChallengeRequests ?? 0) > 0">
          <OaNumberField
            v-model="form.chatChallengeWindowSecs"
            :label="t('chatChallengeWindow')"
            :hint="t('chatChallengeWindowHint')"
            :min="5"
            :max="3600"
          />
          <OaNumberField
            v-model="form.chatChallengeClearMins"
            :label="t('chatChallengeClearance')"
            :hint="t('chatChallengeClearanceHint')"
            :min="1"
            :max="1440"
          />
        </template>
      </AdminControlCard>
      <AdminControlCard id="secSignupReview" v-show="visible('secSignupReview')" :title="t('secSignupReview')" :icon="IconSpark" :hint="t('signupReviewIntro')">
        <OaSwitchField v-model="form.reviewEnabled" :label="t('signupReview')" :hint="t('signupReviewHint')" />
        <OaSelectField
          v-model="form.reviewModel"
          :label="t('signupReviewModel')"
          :hint="t('signupReviewModelHint')"
          :options="[
            { value: '', label: t('signupReviewNoModel') },
            ...enabledModels.map((entry) => ({ value: entry.id, label: entry.display_name })),
          ]"
        />
        <OaSelectField
          v-model="form.reviewMode"
          :label="t('signupReviewMode')"
          :hint="t('signupReviewModeHint')"
          :options="[
            { value: 'loose', label: t('reviewModeLoose') },
            { value: 'normal', label: t('reviewModeNormal') },
            { value: 'strict', label: t('reviewModeStrict') },
          ]"
        />
        <OaNumberField
          v-model="form.reviewRestrictHours"
          :label="t('signupReviewRestrictHours')"
          :hint="t('signupReviewRestrictHoursHint')"
          :min="0"
          :max="8760"
        />
        <OaTextArea
          v-model="form.reviewRefusal"
          :label="t('signupReviewRefusal')"
          :rows="3"
          :placeholder="t('signupReviewRefusalPlaceholder')"
          :hint="t('signupReviewRefusalHint')"
        />
      </AdminControlCard>
      <AdminControlCard id="secTwoFactorPolicy" v-show="visible('secTwoFactorPolicy')" :title="t('secTwoFactorPolicy')" :icon="IconShield" :hint="t('twoFactorPolicyHint')">
        <OaSelectField
          v-model="form.twoFactorPolicy"
          :label="t('twoFactorPolicyLabel')"
          :hint="policyHint"
          :options="[
            { value: 'optional', label: t('twoFactorPolicyOptional') },
            { value: 'backoffice', label: t('twoFactorPolicyBackoffice') },
            { value: 'admins', label: t('twoFactorPolicyAdmins') },
            { value: 'everyone', label: t('twoFactorPolicyEveryone') },
          ]"
        />
        <!-- Said before the save rather than after it: the server refuses a
             policy that would shut its author out of this screen. -->
        <p v-if="selfWithout" class="oa-field-hint oa-2fa-self">
          {{ t('twoFactorPolicySelfNote') }}
          <RouterLink to="/settings?tab=security">{{ t('twoFactorSetUpMine') }}</RouterLink>
        </p>
        <OaTextField
          v-model="form.twoFactorIssuer"
          :label="t('twoFactorIssuerLabel')"
          :placeholder="adoption?.issuer_fallback || site?.name || ''"
          :hint="t('twoFactorIssuerHint')"
          :max-length="64"
        />
        <OaNumberField
          v-model="form.twoFactorRememberDays"
          :label="t('twoFactorRememberLabel')"
          :hint="t('twoFactorRememberHint')"
          :min="0"
          :max="365"
        />
        <!-- Inert without SMTP for the reason verifyEmail is: the server
             checks for mail before sending, so the switch alone does nothing. -->
        <div :class="{ 'oa-field-inert': !mailConfigured }">
          <OaSwitchField
            v-model="form.newDeviceEmail"
            :label="t('newDeviceEmailLabel')"
            :hint="mailConfigured ? t('newDeviceEmailHint') : t('verifyEmailNoMail')"
          />
        </div>
      </AdminControlCard>
      <AdminControlCard id="secBackofficeVerify" v-show="visible('secBackofficeVerify')" :title="t('secBackofficeVerify')" :icon="IconLock" :hint="t('backofficeVerifyHint')">
        <OaSelectField
          v-model="form.backofficeMode"
          :label="t('backofficeVerifyMode')"
          :hint="backofficeHint"
          :options="[
            { value: 'off', label: t('backofficeVerifyOff') },
            { value: 'visit', label: t('backofficeVerifyVisit') },
            { value: 'idle', label: t('backofficeVerifyIdle') },
            { value: 'interval', label: t('backofficeVerifyInterval') },
          ]"
        />
        <OaNumberField
          v-if="form.backofficeMode !== 'off'"
          v-model="form.backofficeMinutes"
          :label="backofficeMinutesLabel"
          :hint="backofficeMinutesHint"
          :min="1"
          :max="10080"
        />
        <template v-if="form.backofficeMode !== 'off'">
          <OaSwitchField v-model="form.backofficeNetwork" :label="t('backofficeNetwork')" :hint="t('backofficeNetworkHint')" />
          <OaSwitchField v-model="form.backofficeBrowser" :label="t('backofficeBrowser')" :hint="t('backofficeBrowserHint')" />
        </template>
        <p v-if="selfWithout && form.backofficeMode !== 'off'" class="oa-field-hint oa-2fa-self">
          {{ t('twoFactorPolicySelfNote') }}
          <RouterLink to="/settings?tab=security">{{ t('twoFactorSetUpMine') }}</RouterLink>
        </p>
      </AdminControlCard>
    </template>
    <template #right="{ visible }">
      <AdminControlCard id="secRegistration" v-show="visible('secRegistration')" :title="t('secRegistration')" :icon="IconUsers">
        <OaSwitchField v-model="form.requireEmail" :label="t('requireEmail')" :hint="t('requireEmailHint')" />
        <!-- Offered but inert without SMTP, and the hint says so. The server
             ignores it in that state too, so an operator cannot lock every new
             account out of an instance that cannot send the link. -->
        <div :class="{ 'oa-field-inert': !mailConfigured }">
          <OaSwitchField
            v-model="form.verifyEmail"
            :label="t('verifyEmail')"
            :hint="mailConfigured ? t('verifyEmailHint') : t('verifyEmailNoMail')"
          />
        </div>
        <OaTextArea
          v-model="form.emailDomains"
          :label="t('emailDomains')"
          :rows="2"
          :placeholder="t('emailDomainsPlaceholder')"
          :hint="t('emailDomainsHint')"
        />
        <OaSelectField
          v-model="form.qqRequirement"
          :label="t('qqRequirement')"
          :hint="t('qqRequirementHint')"
          :options="[
            { value: 'off', label: t('qqRequirementOff') },
            { value: 'optional', label: t('qqRequirementOptional') },
            { value: 'required', label: t('qqRequirementRequired') },
          ]"
        />
      </AdminControlCard>
      <AdminControlCard id="secVerificationScenes" v-show="visible('secVerificationScenes')" :title="t('controlVerificationScenes')" :icon="IconLock" :hint="t('controlVerificationScenesHint')">
        <OaSwitchField
          v-model="form.turnstileOnLogin"
          :label="t('turnstileOnLogin')"
          :hint="t('turnstileOnLoginHint')"
        />
        <OaSwitchField
          v-model="form.turnstileOnSignup"
          :label="t('turnstileOnSignup')"
          :hint="t('turnstileOnSignupHint')"
        />
        <OaSwitchField
          v-model="form.turnstileOnAPIKey"
          :label="t('turnstileOnAPIKey')"
          :hint="t('turnstileOnAPIKeyHint')"
        />
        <OaSwitchField
          v-model="form.turnstileOnRedeem"
          :label="t('turnstileOnRedeem')"
          :hint="t('turnstileOnRedeemHint')"
        />
        <OaSwitchField
          v-model="form.turnstileOnFeedback"
          :label="t('turnstileOnFeedback')"
          :hint="t('turnstileOnFeedbackHint')"
        />
      </AdminControlCard>
      <AdminControlCard id="secMail" v-show="visible('secMail')" :title="t('mailSettings')" :icon="IconMessage" :hint="t('mailSettingsHint')">
        <AdminFailure v-if="mailLoadError" :message="t(mailLoadError)" @retry="loadMail" />
        <p v-else-if="!mailLoaded" class="oa-table-empty" role="status">{{ t('loading') }}</p>
        <template v-else>
          <OaTextField v-model="mailForm.host" :label="t('mailHost')" autocomplete="off" />
          <OaNumberField v-model="mailForm.port" :label="t('mailPort')" :min="1" :max="65535" />
          <OaTextField v-model="mailForm.username" :label="t('mailUsername')" autocomplete="username" />
          <OaTextField v-model="mailForm.from" :label="t('mailFrom')" autocomplete="email" />
          <OaSwitchField v-model="mailForm.implicit_tls" :label="t('mailImplicitTLS')" :hint="t('mailImplicitTLSHint')" />
          <OaTextField
            v-model="mailForm.public_url"
            :label="t('mailPublicURL')"
            :hint="t('mailPublicURLHint')"
            autocomplete="url"
          />
          <OaTextField
            v-model="mailForm.password"
            type="password"
            :label="t('mailPassword')"
            :placeholder="mailForm.password_set ? t('mailPasswordKeepPlaceholder') : t('mailPasswordPlaceholder')"
            :hint="mailForm.password_set ? t('mailPasswordSetHint') : t('mailPasswordHint')"
            autocomplete="new-password"
          />
          <OaConfirmButton
            v-if="mailForm.password_set"
            class="oa-btn oa-btn-danger"
            :label="t('mailClearPassword')"
            :armed-label="t('mailClearPasswordConfirm')"
            :armed-title="t('mailClearPasswordConfirm')"
            :disabled="mailBusy"
            @confirm="clearMailPassword"
          />
          <button type="button" class="oa-btn primary" :disabled="mailBusy" @click="saveMail">
            {{ mailBusy ? t('saving') : t('save') }}
          </button>
          <OaTextField
            v-model="mailTestTo"
            :label="t('mailTestTo')"
            :hint="mailSettingsDirty ? t('mailTestSaveFirst') : undefined"
            type="email"
            autocomplete="email"
          />
          <button type="button" class="oa-btn" :disabled="mailBusy || mailTestBusy || mailSettingsDirty || !mailTestTo.trim()" @click="sendMailTest">
            {{ mailTestBusy ? t('sending') : t('mailTestSend') }}
          </button>
          <p
            v-if="mailFlash"
            class="oa-drawer-flash visible oa-control-flash"
            :class="{ ok: mailFlashOK }"
            :role="mailFlashOK ? 'status' : 'alert'"
          >{{ t(mailFlash) }}</p>
        </template>
      </AdminControlCard>
      <AdminControlCard id="secUserCheck" v-show="visible('secUserCheck')" :title="t('userCheckSettings')" :icon="IconShield" :hint="t('userCheckSettingsHint')">
        <AdminFailure v-if="userCheckLoadError" :message="t(userCheckLoadError)" @retry="loadUserCheck" />
        <p v-else-if="!userCheckLoaded" class="oa-table-empty" role="status">{{ t('loading') }}</p>
        <template v-else>
          <OaSwitchField
            v-model="userCheckForm.enabled"
            :label="t('userCheckEnabled')"
            :hint="t('userCheckEnabledHint')"
          />
          <OaTextField
            v-model="userCheckForm.api_key"
            type="password"
            :label="t('userCheckAPIKey')"
            :placeholder="userCheckForm.api_key_set ? t('userCheckAPIKeyKeepPlaceholder') : t('userCheckAPIKeyPlaceholder')"
            :hint="userCheckForm.api_key_set ? t('userCheckAPIKeySetHint') : t('userCheckAPIKeyMissingHint')"
            autocomplete="new-password"
          />
          <OaConfirmButton
            v-if="userCheckForm.api_key_set"
            class="oa-btn oa-btn-danger"
            :label="t('userCheckClearAPIKey')"
            :armed-label="t('userCheckClearAPIKeyConfirm')"
            :armed-title="t('userCheckClearAPIKeyConfirm')"
            :disabled="userCheckBusy"
            @confirm="clearUserCheckAPIKey"
          />
          <OaTextArea
            v-model="userCheckForm.exempt_domains"
            :label="t('userCheckExemptDomains')"
            :hint="t('userCheckExemptDomainsHint')"
            :placeholder="t('userCheckExemptDomainsPlaceholder')"
            :rows="5"
          />
          <OaSelectField
            v-model="userCheckForm.failure_mode"
            :label="t('userCheckFailureMode')"
            :hint="t('userCheckFailureModeHint')"
            :options="[
              { value: 'reject', label: t('userCheckFailureReject') },
              { value: 'allow', label: t('userCheckFailureAllow') },
            ]"
          />
          <button type="button" class="oa-btn primary" :disabled="userCheckBusy" @click="saveUserCheck">
            {{ userCheckBusy ? t('saving') : t('save') }}
          </button>
          <OaTextField
            v-model="userCheckTestEmail"
            :label="t('userCheckTestEmail')"
            type="email"
            autocomplete="email"
            :hint="t('userCheckTestHint')"
          />
          <button
            type="button"
            class="oa-btn"
            :disabled="userCheckTestBusy || !userCheckTestEmail.trim() || !userCheckForm.api_key_set"
            @click="sendUserCheckTest"
          >{{ userCheckTestBusy ? t('checking') : t('userCheckTest') }}</button>
          <p
            v-if="userCheckFlash"
            class="oa-drawer-flash visible oa-control-flash"
            :class="{ ok: userCheckFlashOK }"
            :role="userCheckFlashOK ? 'status' : 'alert'"
          >{{ t(userCheckFlash) }}</p>
        </template>
      </AdminControlCard>
      <AdminControlCard id="secReviewTrial" v-show="visible('secReviewTrial')" :title="t('reviewTry')" :icon="IconSliders" :hint="t('reviewTryHint')">
        <div class="oa-field">
          <OaTextField v-model="trial.username" :label="t('username')" placeholder="123123123123" />
          <OaTextField v-model="trial.email" :label="t('email')" placeholder="123123123123@qq.com" />
          <OaTextField v-model="trial.qq" :label="t('qq')" placeholder="123123123123" />
          <button type="button" class="oa-btn" :disabled="trial.running" @click="runTrial">
            {{ t('reviewTryRun') }}
          </button>
          <p class="oa-field-hint">{{ trial.answer }}</p>
        </div>
      </AdminControlCard>
      <AdminControlCard id="secTwoFactorAdoption" v-show="visible('secTwoFactorAdoption')" :title="t('secTwoFactorAdoption')" :icon="IconUsers" :hint="t('twoFactorAdoptionHint')">
        <template v-if="adoption">
          <div class="oa-2fa-adoption">
            <div v-for="row in [
              { label: t('twoFactorAdoptionAccounts'), part: adoption.enabled, whole: adoption.accounts },
              { label: t('twoFactorAdoptionAdmins'), part: adoption.admins_enabled, whole: adoption.admins },
            ]" :key="row.label" class="oa-2fa-adoption-row">
              <div class="oa-2fa-adoption-head">
                <span>{{ row.label }}</span>
                <span class="oa-2fa-adoption-figure">
                  {{ t('twoFactorAdoptionOf', { enabled: row.part, total: row.whole }) }} · {{ share(row.part, row.whole) }}
                </span>
              </div>
              <div class="oa-2fa-meter" role="presentation">
                <span :style="{ width: row.whole > 0 ? `${(row.part / row.whole) * 100}%` : '0%' }" />
              </div>
            </div>
          </div>
          <h4 class="oa-2fa-subhead">{{ t('twoFactorAdminsWithout') }}</h4>
          <p v-if="!adoption.admins_without.length" class="oa-field-hint">{{ t('twoFactorAdminsAllSet') }}</p>
          <div v-else class="oa-badge-row">
            <OaBadge v-for="member in adoption.admins_without" :key="member.id" tone="warning">
              @{{ maskUser(member.username) }}
            </OaBadge>
          </div>
        </template>
        <p v-else class="oa-table-empty">{{ t('loading') }}</p>
      </AdminControlCard>
    </template>
    <template #default="{ visible }">
      <!-- Both halves of the sign-in tab are full-width rows: one narrow card
           beside one wide one reads as a mistake, and these two are the same
           kind of thing pointing in opposite directions. -->
      <AdminControlCard id="secOAuth" v-show="visible('secOAuth')" :title="t('secOAuth')" :icon="IconGithub" :hint="t('oauthHint')" class="oa-control-card-wide">
        <div class="oa-providers">
          <div class="oa-provider">
            <span class="oa-provider-mark"><IconGithub :size="15" /></span>
            <OaSwitchField v-model="form.githubEnabled" :label="t('oauthGitHub')" :hint="t('oauthGitHubHint')" />
            <OaTextField
              v-model="form.githubClientID"
              :label="t('oauthClientID')"
              placeholder="Iv1.…"
              monospace
            />
            <OaTextField
              v-model="form.githubSecret"
              type="password"
              :label="t('oauthClientSecret')"
              :placeholder="form.githubSecretHint || '••••'"
              :hint="t('oauthCallback', { url: callbackURL('github') })"
              monospace
            />
          </div>
          <div class="oa-provider">
            <span class="oa-provider-mark"><IconGoogle :size="15" /></span>
            <OaSwitchField v-model="form.googleEnabled" :label="t('oauthGoogle')" :hint="t('oauthGoogleHint')" />
            <OaTextField
              v-model="form.googleClientID"
              :label="t('oauthClientID')"
              placeholder="…apps.googleusercontent.com"
              monospace
            />
            <OaTextField
              v-model="form.googleSecret"
              type="password"
              :label="t('oauthClientSecret')"
              :placeholder="form.googleSecretHint || '••••'"
              :hint="t('oauthCallback', { url: callbackURL('google') })"
              monospace
            />
          </div>
        </div>
        <OaSwitchField
          v-model="form.oauthAllowSignup"
          :label="t('oauthAllowSignup')"
          :hint="t('oauthAllowSignupHint')"
        />
        <OaSwitchField
          v-model="form.oauthLinkByEmail"
          :label="t('oauthLinkByEmail')"
          :hint="t('oauthLinkByEmailHint')"
        />
      </AdminControlCard>
      <AdminControlCard id="secApplications" v-show="visible('secApplications')" :title="t('secApplications')" :icon="IconKey" :hint="t('applicationsHint')" class="oa-control-card-wide">
        <p class="oa-field-hint">{{ t('applicationsIssuer', { issuer }) }}</p>

        <p v-if="!applications.length" class="oa-table-empty">{{ t('applicationsEmpty') }}</p>
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
              <button type="button" class="oa-btn" :disabled="appBusy" @click="toggleApplication(app, !app.disabled)">
                {{ app.disabled ? t('enable') : t('disable') }}
              </button>
              <button v-if="app.confidential" type="button" class="oa-btn" :disabled="appBusy" @click="rotateApplication(app)">
                {{ t('applicationRotate') }}
              </button>
              <OaConfirmButton
                class="oa-btn"
                :label="t('deleteLabel')"
                :armed-label="t('confirmWord')"
                :armed-title="t('applicationDeleteConfirm', { application: app.name })"
                :resting-title="t('deleteLabel')"
                :disabled="appBusy"
                @confirm="removeApplication(app)"
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
        <OaSwitchField v-model="draft.trusted" :label="t('applicationTrustedField')" :hint="t('applicationTrustedHint')" />
        <div class="oa-button-row">
          <button type="button" class="oa-btn primary" :disabled="appBusy" @click="registerApplication">
            {{ t('applicationRegister') }}
          </button>
        </div>
        <p class="oa-drawer-flash" :class="{ visible: !!appFlash }">{{ appFlash }}</p>
      </AdminControlCard>
      <AdminControlCard id="secSecurityLog" v-show="visible('secSecurityLog')" :title="t('secSecurityLog')" :icon="IconFile" :hint="t('securityLogHint')" class="oa-control-card-wide">
        <template #actions>
          <button type="button" class="oa-btn" :disabled="eventsLoading" @click="loadEvents">
            {{ t('refresh') }}
          </button>
        </template>
        <p v-if="eventsLoading && !events.length" class="oa-table-empty">{{ t('loading') }}</p>
        <p v-else-if="!events.length" class="oa-table-empty">{{ t('securityLogEmpty') }}</p>
        <!-- Its own rows, not the request log's: those are a six-column grid,
             and one block dropped into it lands in the 64px time column and
             wraps a word to a line. A decision is a sentence, not a table
             row, so it gets a heading, a line of who and where, and room to
             say why. -->
        <ol v-else class="oa-event-list" :class="{ busy: eventsLoading }">
          <li v-for="event in events" :key="event.id" class="oa-event" :class="`is-${event.severity}`">
            <div class="oa-event-head">
              <span class="oa-event-title">{{ eventLabel(event.event) }}</span>
              <OaBadge
                v-if="event.decision"
                :tone="event.severity === 'danger'
                  ? 'danger' : event.severity === 'warning' ? 'warning' : 'muted'"
              >{{ reviewDecision(event.decision) }}</OaBadge>
              <time class="oa-event-time">{{ absoluteTime(event.at) }}</time>
            </div>
            <div v-if="event.username || event.ip || event.actor_username" class="oa-event-meta">
              <span v-if="event.username">@{{ maskUser(event.username) }}</span>
              <span v-if="event.ip" class="mono">{{ maskLog(event.ip) }}</span>
              <span v-if="event.actor_username">
                {{ t('securityLogActor', { name: `@${maskUser(event.actor_username)}` }) }}
              </span>
            </div>
            <p v-if="event.reason" class="oa-event-reason">{{ event.reason }}</p>
          </li>
        </ol>
        <OaPagination v-bind="eventPage" :total="eventsTotal" :busy="eventsLoading" @change="changeEvents" />
      </AdminControlCard>
    </template>
  </AdminWorkbench>
  <p v-if="flash" class="oa-drawer-flash visible oa-control-flash" role="status">{{ flash }}</p>
</template>
