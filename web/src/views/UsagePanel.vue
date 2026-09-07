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
import { ApiError, api } from '@/api/client';
import { fetchUsage, type UsageSummary } from '@/api/usage';
import OaFormSection from '@/components/OaFormSection.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaStatGrid from '@/components/OaStatGrid.vue';
import OaTable from '@/components/OaTable.vue';
import OaUsageWindow from '@/components/OaUsageWindow.vue';
import type { Column } from '@/components/table-types';
import type { Stat } from '@/components/stat';
import { celebrate } from '@/composables/useConfetti';
import { t } from '@/composables/useI18n';
import { IconPlus } from '@/icons';
import { compactNumber, relativeTime } from '@/lib/format';

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
  status: 'ok' | 'error' | 'aborted' | 'rejected';
  error_code?: string;
  started_at: number;
  finished_at: number;
}

const router = useRouter();

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
const spending = ref('');

const enforced = computed(() => summary.value?.windows.filter((window) => window.enforced) ?? []);
const unlimited = computed(() => !!summary.value && (summary.value.unlimited || !enforced.value.length));

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
  { key: 'tokens', header: t('statTokens'), text: (row) => compactNumber(row.total_tokens), numeric: true, width: '80px' },
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

async function loadAllowance(): Promise<void> {
  try {
    summary.value = await fetchUsage();
  } catch {
    allowanceError.value = t('usageUnavailable');
  }
}

async function loadHistory(): Promise<void> {
  try {
    const payload = await api.get<{ totals: Totals; turns: Turn[] }>('/api/usage/me/history');
    totals.value = payload.totals;
    turns.value = payload.turns;
  } catch (error) {
    historyError.value = error instanceof ApiError ? error.message : t('usageUnavailable');
  }
}

async function loadCards(): Promise<void> {
  cardsError.value = '';
  try {
    const payload = await api.get<{ cards: Card[] }>('/api/usage/cards');
    cards.value = payload.cards;
  } catch {
    cardsError.value = t('usageUnavailable');
  }
}

async function redeem(): Promise<void> {
  const code = redeemCode.value.trim();
  if (!code) return;
  redeeming.value = true;
  try {
    await api.post<{ card: Card }>('/api/usage/redeem', { code });
    redeemCode.value = '';
    redeemOpen.value = false;
    cardFlash.value = t('redeemed');
    await loadCards();
  } catch (error) {
    cardFlash.value = error instanceof ApiError ? error.message : String(error);
  } finally {
    redeeming.value = false;
  }
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
</script>

<template>
  <OaPanel
    :title="t('navUsage')"
    :footer="false"
    :width="460"
    @close="router.push('/')"
  >
    <OaFormSection :title="t('secAllowance')" />
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
        <div v-for="card in cards" v-else :key="card.id" class="oa-card-row">
          <div>
            <span class="oa-card-title">{{ t('cardFullReset') }}</span>
            <span class="oa-card-sub">{{ t('cardExpires', { when: expiry(card.expires_at) }) }}</span>
          </div>
          <span class="oa-header-spacer" />
          <button
            type="button"
            class="oa-btn"
            :disabled="spending === card.id"
            @click="spend(card)"
          >{{ t('cardUse') }}</button>
        </div>
      </div>
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
</template>
