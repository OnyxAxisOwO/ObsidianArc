<script setup lang="ts">
// Redemption codes: a batch of usage resets behind a string somebody types.
//
// The cards themselves are not listed here. A code is the thing an operator
// creates and hands out; where its cards ended up is a question about
// accounts, and it is answered in the account panel.

import { computed, onMounted, ref } from 'vue';
import { adminApi, type RedemptionCode } from '@/admin/api';
import { ApiError } from '@/api/client';
import { copyToClipboard } from '@/chat/markdown';
import OaBadge from '@/components/OaBadge.vue';
import OaBadgeRow from '@/components/OaBadgeRow.vue';
import OaCellStack from '@/components/OaCellStack.vue';
import OaNumberField from '@/components/OaNumberField.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaTable from '@/components/OaTable.vue';
import OaTextField from '@/components/OaTextField.vue';
import type { Column } from '@/components/table-types';
import { t } from '@/composables/useI18n';
import { relativeTime } from '@/lib/format';
import AdminFailure from './AdminFailure.vue';
import { useAdminView } from './adminView';

const view = useAdminView();
view.setTitle(t('codesTitle'), t('codesSubtitle'));

const codes = ref<RedemptionCode[]>([]);
const error = ref('');
const loaded = ref(false);

const panelOpen = ref(false);
// An existing code is shown rather than edited. Changing how many cards a code
// carries after people have redeemed it is a decision with no honest answer —
// the ones already handed out do not come back — so the only action offered is
// withdrawing it.
const existing = ref<RedemptionCode | null>(null);
const busy = ref(false);
const panelError = ref('');
const minted = ref<RedemptionCode[] | null>(null);
const copyLabel = ref('');

const form = ref({
  count: 1 as number | null,
  code: '',
  cards: 10 as number | null,
  cardDays: 30 as number | null,
  expiresDays: null as number | null,
});

const creating = computed(() => existing.value === null);

const columns = computed<Array<Column<RedemptionCode>>>(() => [
  { key: 'code', header: t('colCode') },
  {
    key: 'claimed',
    header: t('colClaimed'),
    // Both halves, because "12" answers nothing without the ceiling it is
    // approaching.
    text: (row) => `${row.claimed} / ${row.cards}`,
    numeric: true,
    width: '100px',
    sort: (row) => row.claimed / Math.max(1, row.cards),
  },
  { key: 'life', header: t('colCardLife'), text: (row) => t('nDays', { count: row.card_days }), secondary: true, width: '90px' },
  { key: 'state', header: t('colState'), width: '110px' },
  { key: 'created', header: t('colUpdated'), text: (row) => relativeTime(row.created_at), secondary: true, width: '110px' },
]);

function open(row: RedemptionCode | null): void {
  existing.value = row;
  minted.value = null;
  panelError.value = '';
  copyLabel.value = '';
  form.value = { count: 1, code: '', cards: 10, cardDays: 30, expiresDays: null };
  panelOpen.value = true;
}

async function create(): Promise<void> {
  const batch = form.value.count ?? 1;
  const days = form.value.expiresDays;
  busy.value = true;
  panelError.value = '';
  try {
    const { codes: created } = await adminApi.createCode({
      code: batch > 1 ? '' : form.value.code.trim(),
      count: batch,
      cards: form.value.cards ?? 1,
      card_days: form.value.cardDays ?? 30,
      // Zero is "never", which is what an empty field means here.
      expires_at: days && days > 0 ? Date.now() + days * 24 * 3600 * 1000 : 0,
    });
    // Generated codes exist nowhere else until they are copied off this
    // screen, so the panel turns into the list rather than closing over them.
    minted.value = created;
    view.reload();
  } catch (failure) {
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    busy.value = false;
  }
}

async function remove(): Promise<void> {
  const row = existing.value;
  if (!row) return;
  busy.value = true;
  try {
    await adminApi.deleteCode(row.id);
    panelOpen.value = false;
    view.reload();
  } catch (failure) {
    busy.value = false;
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

function copyAll(): void {
  const lines = (minted.value ?? []).map((entry) => entry.code).join('\n');
  void copyToClipboard(lines).then((ok) => {
    if (ok) copyLabel.value = t('copied');
  });
}

async function load(): Promise<void> {
  error.value = '';
  try {
    ({ codes: codes.value } = await adminApi.codes());
  } catch (failure) {
    error.value = failure instanceof Error ? failure.message : String(failure);
  } finally {
    loaded.value = true;
  }
}

onMounted(load);
</script>

<template>
  <Teleport :to="view.actionsHost">
    <button type="button" class="oa-btn primary" @click="open(null)">{{ t('addCode') }}</button>
  </Teleport>

  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!loaded" class="oa-table-empty">{{ t('loading') }}</p>

  <OaTable
    v-else
    :columns="columns"
    :rows="codes"
    :empty="t('noCodes')"
    :muted="(row) => row.claimed >= row.cards"
    selectable
    @select="open($event)"
  >
    <template #cell-code="{ row }">
      <OaCellStack :title="row.code" :sub="row.note || undefined" monospace />
    </template>
    <template #cell-state="{ row }">
      <OaBadgeRow>
        <OaBadge v-if="row.claimed >= row.cards" tone="muted">{{ t('codeEmptied') }}</OaBadge>
        <OaBadge
          v-if="row.expires_at > 0 && row.expires_at <= Date.now()"
          tone="danger"
        >{{ t('codeExpired') }}</OaBadge>
      </OaBadgeRow>
    </template>
  </OaTable>

  <OaPanel
    v-if="panelOpen"
    :title="minted ? t('codesMinted', { count: minted.length }) : creating ? t('addCode') : existing!.code"
    :footer="creating && !minted"
    :confirm-label="t('add')"
    :destructive-label="existing ? t('deleteLabel') : undefined"
    :destructive-confirm="existing ? t('confirmDeleteCode', { code: existing.code }) : undefined"
    :busy="busy"
    :error="panelError"
    @close="panelOpen = false"
    @confirm="create"
    @destructive="remove"
  >
    <!-- The batch, once, with a way to take it away in one piece. -->
    <template v-if="minted">
      <p class="oa-field-hint">{{ t('codesMintedHint') }}</p>
      <button type="button" class="oa-btn primary" @click="copyAll">
        {{ copyLabel || t('copyAll') }}
      </button>
      <div class="oa-code-list">
        <code v-for="entry in minted" :key="entry.id" class="oa-code-line">{{ entry.code }}</code>
      </div>
    </template>

    <p v-else-if="!creating" class="oa-field-hint">
      {{ t('codeClaimedSoFar', { claimed: existing!.claimed, cards: existing!.cards }) }}
    </p>

    <template v-else>
      <OaNumberField
        v-model="form.count"
        :label="t('codeCount')"
        :min="1"
        :max="200"
        :hint="t('codeCountHint')"
      />
      <!-- A batch is generated, so there is nothing to name. Hiding the field
           rather than disabling it, because a disabled box still looks like
           somewhere to type. -->
      <OaTextField
        v-if="(form.count ?? 1) <= 1"
        v-model="form.code"
        :label="t('colCode')"
        placeholder="WELCOME2026"
        :hint="t('codeHint')"
        monospace
      />
      <OaNumberField v-model="form.cards" :label="t('codeCards')" :min="1" :hint="t('codeCardsHint')" />
      <OaNumberField
        v-model="form.cardDays"
        :label="t('codeCardDays')"
        :min="1"
        :hint="t('codeCardDaysHint')"
      />
      <OaNumberField
        v-model="form.expiresDays"
        :label="t('codeExpiresDays')"
        :min="0"
        :placeholder="t('noLimit')"
        :hint="t('codeExpiresHint')"
      />
    </template>
  </OaPanel>
</template>
