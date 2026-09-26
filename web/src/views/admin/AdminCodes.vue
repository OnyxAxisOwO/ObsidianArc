<script setup lang="ts">
// Redemption codes: a batch of usage resets behind a string somebody types.
//
// A code is the thing an operator creates and hands out. Its detail panel
// names the accounts that redeemed it; the account panel answers the inverse
// question, which cards one person still holds.

import { computed, onMounted, ref } from 'vue';
import { adminApi, type CodeRedemption, type RedemptionCode } from '@/admin/api';
import { ApiError } from '@/api/client';
import { saveAsFile } from '@/api/backup';
import { copyToClipboard } from '@/chat/markdown';
import OaBadge from '@/components/OaBadge.vue';
import OaBadgeRow from '@/components/OaBadgeRow.vue';
import OaCellStack from '@/components/OaCellStack.vue';
import OaCheckList from '@/components/OaCheckList.vue';
import OaFormSection from '@/components/OaFormSection.vue';
import OaNumberField from '@/components/OaNumberField.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaTable from '@/components/OaTable.vue';
import OaTextField from '@/components/OaTextField.vue';
import type { Column } from '@/components/table-types';
import { t } from '@/composables/useI18n';
import { relativeTime } from '@/lib/format';
import { maskCredential, maskUser } from '@/admin/safeMode';
import AdminFailure from './AdminFailure.vue';
import { useAdminView } from './adminView';

const view = useAdminView();
view.setTitle(t('codesTitle'), t('codesSubtitle'));

const codes = ref<RedemptionCode[]>([]);
const error = ref('');
const loaded = ref(false);
const exporting = ref(false);
const createdFrom = ref('');
const createdThrough = ref('');
const exportError = computed(() => {
  const from = createdFrom.value ? new Date(createdFrom.value).getTime() : 0;
  const through = createdThrough.value ? new Date(createdThrough.value).getTime() : Infinity;
  return Number.isNaN(from) || Number.isNaN(through) || from > through ? t('codeExportInvalidRange') : '';
});
const exportCodes = computed(() => {
  if (exportError.value) return [];
  const from = createdFrom.value ? new Date(createdFrom.value).getTime() : 0;
  // Include the whole final minute: a code minted at 12:30:45 belongs to an
  // end time the minute-precision picker displays as 12:30.
  const end = createdThrough.value ? new Date(createdThrough.value).getTime() + 60_000 : Infinity;
  return codes.value.filter((code) => code.created_at >= from && code.created_at < end);
});

function exportFile(): void {
  if (exportError.value || !exportCodes.value.length) return;
  saveAsFile(`obsidian-arc-codes-${new Date().toISOString().slice(0, 10)}.txt`,
    exportCodes.value.map((code) => code.code).join('\n') + '\n', 'text/plain;charset=utf-8');
  exporting.value = false;
}

function openExport(): void {
  panelOpen.value = false;
  createdFrom.value = '';
  createdThrough.value = '';
  exporting.value = true;
}

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
const redemptions = ref<CodeRedemption[]>([]);
const redemptionsLoading = ref(false);
const redemptionsError = ref('');

const form = ref({
  count: 1 as number | null,
  code: '',
  name: '',
  scopePreset: 'full' as 'full' | '5h' | '1w' | '1m' | 'custom',
  customWindows: ['5h'] as string[],
  cards: 10 as number | null,
  cardDays: 30 as number | null,
  expiresDays: null as number | null,
});

function formatCodeScope(windows?: string[]): string {
  const wins = windows ?? [];
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

function selectedWindows(): string[] {
  if (form.value.scopePreset === 'custom') {
    return form.value.customWindows;
  }
  if (form.value.scopePreset === 'full') {
    return [];
  }
  return [form.value.scopePreset];
}

const creating = computed(() => existing.value === null);

const columns = computed<Array<Column<RedemptionCode>>>(() => [
  { key: 'code', header: t('colCode'), text: (row) => maskCredential(row.code) },
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
  { key: 'created', header: t('colCreated'), text: (row) => relativeTime(row.created_at), secondary: true, width: '110px' },
]);

function open(row: RedemptionCode | null): void {
  exporting.value = false;
  existing.value = row;
  minted.value = null;
  panelError.value = '';
  copyLabel.value = '';
  redemptions.value = [];
  redemptionsError.value = '';
  redemptionsLoading.value = row !== null;
  form.value = {
    count: 1,
    code: '',
    name: '',
    scopePreset: 'full',
    customWindows: ['5h'],
    cards: 10,
    cardDays: 30,
    expiresDays: null,
  };
  panelOpen.value = true;
  if (row) void loadRedemptions(row.id);
}

async function loadRedemptions(codeID: string): Promise<void> {
  try {
    const result = await adminApi.codeRedemptions(codeID);
    if (existing.value?.id === codeID) redemptions.value = result.redemptions ?? [];
  } catch (failure) {
    if (existing.value?.id === codeID) {
      redemptionsError.value = failure instanceof ApiError ? failure.message : String(failure);
    }
  } finally {
    if (existing.value?.id === codeID) redemptionsLoading.value = false;
  }
}

async function create(): Promise<void> {
  if (form.value.scopePreset === 'custom' && form.value.customWindows.length === 0) {
    panelError.value = t('cardResetScopeRequired');
    return;
  }
  const batch = form.value.count ?? 1;
  const days = form.value.expiresDays;
  busy.value = true;
  panelError.value = '';
  try {
    const { codes: created } = await adminApi.createCode({
      code: batch > 1 ? '' : form.value.code.trim(),
      name: form.value.name.trim(),
      windows: selectedWindows(),
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
    <button id="exportCodes" type="button" class="oa-btn" :disabled="!loaded || !!error" @click="openExport">{{ t('exportCodes') }}</button>
    <button id="addCode" type="button" class="oa-btn primary" @click="open(null)">{{ t('addCode') }}</button>
  </Teleport>

  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!loaded" class="oa-table-empty">{{ t('loading') }}</p>

  <OaTable
    id="codesTitle"
    v-else
    :columns="columns"
    :rows="codes"
    :empty="t('noCodes')"
    :muted="(row) => row.claimed >= row.cards"
    selectable
    @select="open($event)"
  >
    <template #cell-code="{ row }">
      <OaCellStack
        :title="row.code"
        :sub="Array.from(new Set([row.name || undefined, formatCodeScope(row.windows), row.note || undefined].filter(Boolean))).join(' · ')"
        monospace
      />
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
    v-if="exporting"
    :title="t('exportCodes')"
    :confirm-label="t('download')"
    :confirmable="!exportError && exportCodes.length > 0"
    :error="exportError"
    @close="exporting = false"
    @confirm="exportFile"
  >
    <p class="oa-field-hint">{{ t('codeExportHint') }}</p>
    <OaTextField v-model="createdFrom" type="datetime-local" :label="t('codeCreatedFrom')" />
    <OaTextField v-model="createdThrough" type="datetime-local" :label="t('codeCreatedThrough')" />
    <p class="oa-field-hint" role="status">{{ t('codeExportCount', { count: exportCodes.length }) }}</p>
  </OaPanel>

  <OaPanel
    v-if="panelOpen"
    :title="minted ? t('codesMinted', { count: minted.length }) : creating ? t('addCode') : maskCredential(existing!.code)"
    :footer="creating && !minted"
    :confirm-label="t('add')"
    :destructive-label="existing ? t('deleteLabel') : undefined"
    :destructive-confirm="existing ? t('confirmDeleteCode', { code: maskCredential(existing.code) }) : undefined"
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
        <code v-for="entry in minted" :key="entry.id" class="oa-code-line">{{ maskCredential(entry.code) }}</code>
      </div>
    </template>

    <template v-else-if="!creating">
      <p class="oa-field-hint">
        {{ t('codeClaimedSoFar', { claimed: existing!.claimed, cards: existing!.cards }) }}
      </p>
      <div v-if="existing!.name || (existing!.windows && existing!.windows.length)" class="oa-card-list" style="margin-bottom: 12px;">
        <div class="oa-card-row">
          <div>
            <span class="oa-card-title">{{ existing!.name || formatCodeScope(existing!.windows) }}</span>
            <span v-if="existing!.name" class="oa-card-sub">{{ formatCodeScope(existing!.windows) }}</span>
          </div>
        </div>
      </div>
      <OaFormSection :title="t('codeRedeemedBy')" />
      <p v-if="redemptionsLoading" class="oa-field-hint">{{ t('loading') }}</p>
      <p v-else-if="redemptionsError" class="oa-field-hint">{{ redemptionsError }}</p>
      <p v-else-if="!redemptions.length" class="oa-field-hint">{{ t('codeNoRedemptions') }}</p>
      <div v-else class="oa-card-list">
        <div v-for="redemption in redemptions" :key="redemption.user_id" class="oa-card-row">
          <OaCellStack
            :title="maskUser(redemption.nickname || redemption.username)"
            :sub="`@${maskUser(redemption.username)}`"
          />
          <span class="oa-card-expiry">{{ relativeTime(redemption.redeemed_at) }}</span>
        </div>
      </div>
    </template>

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
      <OaTextField
        v-model="form.name"
        :label="t('cardName')"
        :placeholder="t('cardNamePlaceholder')"
        :hint="t('cardNameHint')"
        :max-length="64"
      />
      <OaSelectField
        v-model="form.scopePreset"
        :label="t('cardResetScope')"
        :hint="t('cardResetScopeHint')"
        :options="[
          { value: 'full', label: t('cardResetFull') },
          { value: '5h', label: t('cardReset5H') },
          { value: '1w', label: t('cardReset1W') },
          { value: '1m', label: t('cardReset1M') },
          { value: 'custom', label: t('cardResetCustom') },
        ]"
      />
      <OaCheckList
        v-if="form.scopePreset === 'custom'"
        v-model="form.customWindows"
        :label="t('cardResetScope')"
        :empty-text="t('nothingYet')"
        :items="[
          { value: '5h', label: t('cardScopeLabel5H') },
          { value: '1w', label: t('cardScopeLabel1W') },
          { value: '1m', label: t('cardScopeLabel1M') },
        ]"
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
