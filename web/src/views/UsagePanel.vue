<script setup lang="ts">
// What this account has spent, and on what.
//
// A panel over the chat, like Settings and About, because it is the same kind
// of thing: somewhere you go to look at your own account and come back from.
// The composer's menu keeps its own short version of the allowance — that one
// answers "can I send this", which is a question asked mid-sentence and does
// not want a screen.

import { computed, nextTick, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useIntervalFn } from '@vueuse/core';
import { ApiError, api } from '@/api/client';
import { fetchUsage, type UsageSummary } from '@/api/usage';
import OaFormSection from '@/components/OaFormSection.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaMarkdown from '@/components/OaMarkdown.vue';
import OaOverlay from '@/components/OaOverlay.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaStatGrid from '@/components/OaStatGrid.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTable from '@/components/OaTable.vue';
import OaTurnstile from '@/components/OaTurnstile.vue';
import OaUsageWindow from '@/components/OaUsageWindow.vue';
import type { Column } from '@/components/table-types';
import type { Stat } from '@/components/stat';
import { celebrate } from '@/composables/useConfetti';
import { t } from '@/composables/useI18n';
import { IconClose, IconLock, IconPlus, IconRefresh } from '@/icons';
import { absoluteTime, compactNumber, relativeTime, tokenFigure } from '@/lib/format';
import { currentPreferences, currentUser, siteInfo, syncPreferences } from '@/stores/session';

interface Totals {
  requests: number;
  input_tokens: number;
  output_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  credits: number;
  errors: number;
}

/** One reset somebody was given, and until when they may spend it. */
interface Card {
  id: string;
  name?: string;
  windows?: string[];
  source: 'grant' | 'code';
  expires_at: number;
}

/** One turn, as narrow as the server will describe it. */
interface Turn {
  id: string;
  model_name: string;
  input_tokens: number;
  output_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  credits: number;
  /** The provider reported no usage; the tokens were estimated from the text. */
  estimated?: boolean;
  status: 'ok' | 'error' | 'aborted' | 'rejected';
  error_code?: string;
  started_at: number;
  finished_at: number;
}

const router = useRouter();

// The panel re-reads itself while it is open, so a turn finished in another
// tab or an API call arrives on its own rather than at the next visit. The
// figures are aggregates over the ledger and move in steps, so fifteen
// seconds is plenty — unlike the composer's allowance bar, nobody is waiting
// on this screen for permission to send.
const REFRESH_MS = 15000;

const summary = ref<UsageSummary | null>(null);
const allowanceError = ref('');
const totals = ref<Totals | null>(null);
const turns = ref<Turn[]>([]);
const historyError = ref('');

const cards = ref<Card[]>([]);
const cardsError = ref('');
const cardFlash = ref('');
const redeemOpen = ref(false);
const redeemCode = ref('');
const redeeming = ref(false);
const redeemInput = ref<HTMLInputElement | null>(null);
const redeemChallenge = ref(false);
const redeemChallengeError = ref('');
const redeemGuard = ref<InstanceType<typeof OaTurnstile> | null>(null);
const pendingRedeemCode = ref('');
const spending = ref('');

const enforced = computed(() => summary.value?.windows.filter((window) => window.enforced) ?? []);
const unlimited = computed(() => !!summary.value && (summary.value.unlimited || !enforced.value.length));
const groupName = computed(() => currentUser.value?.group_name.trim() ?? '');
const groupDescription = computed(() => currentUser.value?.group_description?.trim() ?? '');
const groupShowExpiry = computed(() => currentUser.value?.group_show_expiry !== false);
const groupExpiryText = computed(() => {
  if (!groupShowExpiry.value) return '';
  const expiresAt = currentUser.value?.group_expires_at ?? 0;
  if (expiresAt > 0) {
    return t('groupExpiryDate', { when: absoluteTime(expiresAt) });
  }
  return t('groupExpiryNever');
});
const autoUseResetCard = computed({
  get: () => currentPreferences.value['auto_use_reset_card'] === true,
  set: (enabled: boolean) => syncPreferences({ auto_use_reset_card: enabled }),
});

const statCards = computed<Stat[]>(() => {
  const value = totals.value;
  if (!value) return [];
  return [
    {
      label: t('statRequests'),
      value: compactNumber(value.requests),
      note: value.errors ? t('nFailed', { count: value.errors }) : t('allFine'),
    },
    {
      label: t('statTokens'),
      value: compactNumber(value.total_tokens),
      note: t('tokensInOut', {
        input: compactNumber(value.input_tokens),
        output: compactNumber(value.output_tokens),
      }),
    },
    { label: t('statCredits'), value: compactNumber(value.credits) },
  ];
});

const columns = computed<Array<Column<Turn>>>(() => [
  { key: 'model', header: t('colModel'), text: (row) => row.model_name },
  { key: 'when', header: t('colWhen'), text: (row) => relativeTime(row.started_at), secondary: true, width: '110px' },
  { key: 'tokens', header: t('statTokens'), text: (row) => tokenFigure(row.total_tokens, row.estimated), numeric: true, width: '80px' },
  {
    key: 'credits',
    header: t('statCredits'),
    // Two decimals, not the compact form: these are small numbers and "0.1k"
    // for a tenth of a credit would be a worse answer than none.
    text: (row) => (Math.round(row.credits * 100) / 100).toFixed(2),
    numeric: true,
    width: '80px',
  },
  {
    key: 'state',
    header: t('colState'),
    text: (row) => (row.status === 'ok' ? '' : t(`turn_${row.status}` as 'turn_error')),
    width: '80px',
  },
]);

function expiry(at: number): string {
  return new Date(at).toLocaleString(undefined, {
    month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit',
  });
}

function expiryDay(at: number): string {
  return new Date(at).toLocaleDateString(undefined, { month: 'numeric', day: 'numeric' });
}

function formatCardScope(card: Card): string {
  const wins = card.windows ?? [];
  if (wins.length === 0 || wins.includes('full')) {
    return t('cardFullReset');
  }
  const order = ['5h', '1w', '1m'];
  const sorted = [...wins].sort((a, b) => order.indexOf(a) - order.indexOf(b));
  if (sorted.length === 1) {
    if (sorted[0] === '5h') return t('cardScope5H');
    if (sorted[0] === '1w') return t('cardScope1W');
    if (sorted[0] === '1m') return t('cardScope1M');
  }
  const labels = sorted.map((w) => {
    if (w === '5h') return t('cardScopeLabel5H');
    if (w === '1w') return t('cardScopeLabel1W');
    if (w === '1m') return t('cardScopeLabel1M');
    return w;
  });
  return t('cardScopeCombined', { windows: labels.join(' + ') });
}

function cardTitle(card: Card): string {
  if (card.name && card.name.trim()) {
    return card.name.trim();
  }
  return formatCardScope(card);
}

function cardSubtitle(stack: CardStack): string {
  const card = stack.first;
  const expiryText = stack.count > 1
    ? t('cardExpires', { when: expiryDay(card.expires_at) })
    : t('cardExpires', { when: expiry(card.expires_at) });
  const scope = formatCardScope(card);

  if (card.name && card.name.trim() && card.name.trim() !== scope) {
    return `${scope} · ${expiryText}`;
  }
  return expiryText;
}

/** A day's worth of matching cards, as one row. */
interface CardStack {
  key: string;
  count: number;
  /** The one a press spends: the soonest to expire, as the server would pick. */
  first: Card;
}

/**
 * Cards with the same name and reset windows that run out on the same day,
 * stacked into one row.
 */
const cardStacks = computed<CardStack[]>(() => {
  const byGroup = new Map<string, CardStack>();
  for (const card of cards.value) {
    const wins = (card.windows ?? []).filter((w) => w && w !== 'full');
    const winsKey = wins.slice().sort().join(',');
    const dayKey = new Date(card.expires_at).toDateString();
    const key = `${card.name ?? ''}::${winsKey}::${dayKey}`;
    const stack = byGroup.get(key);
    if (!stack) {
      byGroup.set(key, { key, count: 1, first: card });
      continue;
    }
    stack.count += 1;
    if (card.expires_at < stack.first.expires_at) stack.first = card;
  }
  return [...byGroup.values()].sort((a, b) => a.first.expires_at - b.first.expires_at);
});

const cardsLeft = computed(() => cards.value.length);

async function loadAllowance(): Promise<void> {
  try {
    summary.value = await fetchUsage();
    allowanceError.value = '';
  } catch {
    allowanceError.value = t('usageUnavailable');
  }
}

const refreshingAllowance = ref(false);

// The button refreshes the screen, not the bars it sits above. Somebody who
// presses it has just spent something and wants to see it land; leaving the
// totals and the turn list at the figure they opened with made the refresh
// look like it had done nothing.
async function refreshAllowance(): Promise<void> {
  if (refreshingAllowance.value) return;
  refreshingAllowance.value = true;
  try {
    await Promise.all([loadAllowance(), loadHistory(), loadCards()]);
  } finally {
    refreshingAllowance.value = false;
  }
}

async function fetchHistory(): Promise<{ totals: Totals; turns: Turn[] }> {
  return api.get<{ totals: Totals; turns: Turn[] }>('/api/usage/me/history');
}

async function loadHistory(): Promise<void> {
  try {
    const payload = await fetchHistory();
    totals.value = payload.totals;
    turns.value = payload.turns;
  } catch (error) {
    historyError.value = error instanceof ApiError ? error.message : t('usageUnavailable');
  }
}

async function fetchCards(): Promise<{ cards: Card[] }> {
  return api.get<{ cards: Card[] }>('/api/usage/cards');
}

async function loadCards(): Promise<void> {
  cardsError.value = '';
  try {
    const payload = await fetchCards();
    cards.value = payload.cards;
  } catch {
    cardsError.value = t('usageUnavailable');
  }
}

// The same three reads without the error handling: a failed refresh leaves
// what is on screen, because the last good figures are a better answer than
// an error where they were. Success clears the errors, so a panel that
// opened while the server was unreachable recovers on its own.
function refreshQuietly(): void {
  fetchUsage().then((next) => {
    summary.value = next;
    allowanceError.value = '';
  }).catch(() => {});
  fetchHistory().then((payload) => {
    totals.value = payload.totals;
    turns.value = payload.turns;
    historyError.value = '';
  }).catch(() => {});
  fetchCards().then((payload) => {
    cards.value = payload.cards;
    cardsError.value = '';
  }).catch(() => {});
}

async function submitRedemption(code: string, turnstile = ''): Promise<void> {
  redeeming.value = true;
  try {
    await api.post<{ card: Card }>('/api/usage/redeem', {
      code,
      ...(turnstile ? { turnstile } : {}),
    });
    redeemCode.value = '';
    redeemOpen.value = false;
    redeemChallenge.value = false;
    cardFlash.value = t('redeemed');
    await loadCards();
  } catch (error) {
    if (turnstile && error instanceof ApiError &&
        (error.code === 'challenge_failed' || error.code === 'challenge_unavailable')) {
      redeemChallengeError.value = error.code === 'challenge_failed'
        ? t('challengeFailed') : t('challengeUnavailable');
      redeemGuard.value?.reset();
    } else {
      redeemChallenge.value = false;
      cardFlash.value = error instanceof ApiError ? error.message : String(error);
    }
  } finally {
    redeeming.value = false;
  }
}

function redeem(): void {
  const code = redeemCode.value.trim();
  if (!code || redeeming.value) return;
  cardFlash.value = '';
  if (siteInfo.value.turnstile_on_redeem) {
    pendingRedeemCode.value = code;
    redeemChallengeError.value = '';
    redeemChallenge.value = true;
    return;
  }
  void submitRedemption(code);
}

async function completeRedeemChallenge(): Promise<void> {
  const token = redeemGuard.value?.token() ?? '';
  if (!token || redeeming.value) return;
  redeemChallengeError.value = '';
  await submitRedemption(pendingRedeemCode.value, token);
}

function cancelRedeemChallenge(): void {
  redeemChallenge.value = false;
  redeemChallengeError.value = '';
  pendingRedeemCode.value = '';
}

async function spend(card: Card): Promise<void> {
  spending.value = card.id;
  try {
    await api.post<void>(`/api/usage/cards/${encodeURIComponent(card.id)}/use`, {});
    cardFlash.value = t('cardUsed');
    celebrate();
    // The whole screen, not just this list: the bars above are the reason
    // somebody spent it, and leaving them at yesterday's figure would make
    // the card look like it did nothing.
    await Promise.all([loadAllowance(), loadCards(), loadHistory()]);
  } catch (error) {
    cardFlash.value = error instanceof ApiError ? error.message : String(error);
  } finally {
    spending.value = '';
  }
}

function toggleRedeem(): void {
  redeemOpen.value = !redeemOpen.value;
  if (redeemOpen.value) void nextTick(() => redeemInput.value?.focus());
}

onMounted(() => {
  // Two requests rather than one endpoint returning everything: the allowance
  // is already served, cached and read by the composer every few seconds, and
  // folding a page of history into that hot path would make every menu open
  // carry fifty rows nobody opened it for.
  void loadAllowance();
  void loadHistory();
  void loadCards();
});

useIntervalFn(refreshQuietly, REFRESH_MS);
</script>

<template>
  <OaPanel
    :title="t('navUsage')"
    :footer="false"
    :width="460"
    @close="router.push('/')"
  >
    <section v-if="groupName" class="oa-usage-group">
      <h1 class="oa-usage-group-name">{{ groupName }}</h1>
      <!-- Group copy is operator-authored, so it uses the same node-building
           renderer and XSS boundary as announcements and model output. -->
      <OaMarkdown
        v-if="groupDescription"
        class="ai-answer oa-usage-group-description"
        :text="groupDescription"
      />
      <p v-if="groupShowExpiry && groupExpiryText" class="oa-usage-group-expiry">
        {{ groupExpiryText }}
      </p>
    </section>

    <div class="oa-usage-head">
      <h3 class="oa-drawer-subhead">{{ t('secAllowance') }}</h3>
      <span class="oa-header-spacer" />
      <OaIconButton
        class="oa-icon-btn"
        :label="t('refresh')"
        :disabled="refreshingAllowance"
        @click="refreshAllowance"
      >
        <IconRefresh :size="14" :class="{ 'is-refreshing': refreshingAllowance }" />
      </OaIconButton>
    </div>
    <div class="oa-usage-list">
      <p v-if="allowanceError" class="oa-field-hint">{{ allowanceError }}</p>
      <span v-else-if="unlimited" class="oa-usage-reset">{{ t('quotaUnlimited') }}</span>
      <!-- The large form here, the compact one in the composer: this screen is
           where somebody has come to look at exactly this, and a 4px hairline
           is what you draw when the reader is halfway through a sentence. -->
      <OaUsageWindow
        v-for="window in enforced"
        :key="window.kind"
        :window="window"
        :display="summary?.display ?? 'absolute'"
        size="large"
      />
    </div>

    <div>
      <!-- The plus is beside the heading rather than under the list, because
           it is the thing to reach for when the list is empty — which is when
           somebody with a code in their hand is looking at this screen. -->
      <div class="oa-card-head">
        <h3 class="oa-drawer-subhead">{{ t('secCards') }}</h3>
        <!-- The total beside the name, because "how many do I have" is the
             question this section is opened with and the list below only
             answered it by counting. -->
        <span v-if="cardsLeft" class="oa-card-total">{{ t('cardsHeldCount', { count: cardsLeft }) }}</span>
        <span class="oa-header-spacer" />
        <OaIconButton class="oa-icon-btn" :label="t('redeemAdd')" @click="toggleRedeem">
          <IconPlus :size="15" />
        </OaIconButton>
      </div>

      <div class="oa-redeem-row oa-input-row" :hidden="!redeemOpen">
        <input
          ref="redeemInput"
          v-model="redeemCode"
          type="text"
          spellcheck="false"
          :placeholder="t('redeemPlaceholder')"
          @keydown.enter.prevent="redeem"
        >
        <button type="button" class="oa-btn" :disabled="redeeming" @click="redeem">
          {{ t('redeemAction') }}
        </button>
      </div>

      <p class="oa-field-hint">{{ cardFlash }}</p>

      <div class="oa-card-list">
        <p v-if="cardsError" class="oa-field-hint">{{ cardsError }}</p>
        <p v-else-if="!cards.length" class="oa-field-hint">{{ t('cardNone') }}</p>
        <div v-for="stack in cardStacks" v-else :key="stack.key" class="oa-card-row">
          <div>
            <span class="oa-card-title">
              {{ cardTitle(stack.first) }}
              <!-- Only when there is more than one. "× 1" is noise on the
                   row it was added to make legible. -->
              <em v-if="stack.count > 1" class="oa-card-times">&times; {{ stack.count }}</em>
            </span>
            <!-- A stack spans a day, so it is dated to the day. A lone card
                 keeps its time: that is the one somebody is deciding whether
                 to spend before it runs out this evening. -->
            <span class="oa-card-sub">{{ cardSubtitle(stack) }}</span>
          </div>
          <span class="oa-header-spacer" />
          <button
            type="button"
            class="oa-btn"
            :disabled="spending === stack.first.id"
            @click="spend(stack.first)"
          >{{ t('cardUse') }}</button>
        </div>
      </div>

      <OaSwitchField
        v-model="autoUseResetCard"
        :label="t('autoUseResetCard')"
        :hint="t('autoUseResetCardHint')"
      />
    </div>

    <OaFormSection :title="t('secTotals')" />
    <OaStatGrid :stats="statCards" />

    <OaFormSection :title="t('secRecentTurns')" />
    <p v-if="historyError" class="oa-field-hint">{{ historyError }}</p>
    <OaTable
      v-else
      :columns="columns"
      :rows="turns"
      :empty="t('noTurnsYet')"
      :muted="(row) => row.status !== 'ok'"
    />
  </OaPanel>

  <OaOverlay
    v-if="redeemChallenge"
    v-slot="{ close }"
    overlay-class="oa-modal-overlay"
    :dismissible="!redeeming"
    @close="cancelRedeemChallenge"
  >
    <div class="oa-auth-card oa-modal-card">
      <OaIconButton class="oa-icon-btn oa-modal-close" :label="t('close')" @click="close">
        <IconClose :size="16" />
      </OaIconButton>

      <div class="oa-auth-brand">
        <span class="oa-auth-mark"><IconLock :size="15" /></span>
        <span>{{ siteInfo.name }}</span>
      </div>
      <h1 class="oa-auth-title">{{ t('redeemChallengeTitle') }}</h1>
      <p class="oa-auth-sub">{{ t('redeemChallengeBody') }}</p>

      <div class="oa-auth-form">
        <OaTurnstile
          ref="redeemGuard"
          :site-key="siteInfo.turnstile_site_key ?? ''"
          @solved="completeRedeemChallenge"
        />
        <p v-if="redeemChallengeError" class="oa-auth-error" role="alert">
          {{ redeemChallengeError }}
        </p>
      </div>
    </div>
  </OaOverlay>
</template>
