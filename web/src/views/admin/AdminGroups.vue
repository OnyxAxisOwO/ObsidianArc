<script setup lang="ts">
// Groups: who may use which models, and what allowance they share.
//
// Nothing is named in advance. There is no Free and no Pro anywhere in the
// source; an instance starts with one group called Default and an
// administrator makes whatever the deployment actually needs.
//
// A group's quota lives in the same panel as its permissions, because they
// are the same decision — "what does this tier get" — and splitting them
// across two screens makes an operator hold half the answer in their head.

import { computed, nextTick, onMounted, ref } from 'vue';
import {
  adminApi, emptyPolicy,
  type AdminModel, type Group, type QuotaLimits, type QuotaPolicy, type QuotaWindowKind,
} from '@/admin/api';
import { ApiError } from '@/api/client';
import OaBadge from '@/components/OaBadge.vue';
import OaBadgeRow from '@/components/OaBadgeRow.vue';
import OaCellStack from '@/components/OaCellStack.vue';
import OaFormSection from '@/components/OaFormSection.vue';
import OaNumberField from '@/components/OaNumberField.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTable from '@/components/OaTable.vue';
import OaTextArea from '@/components/OaTextArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import OaTierList from '@/components/OaTierList.vue';
import type { ListItem } from '@/components/list-items';
import type { Column } from '@/components/table-types';
import { t, tn } from '@/composables/useI18n';
import AdminFailure from './AdminFailure.vue';
import CreditsField from './CreditsField.vue';
import { useAdminView } from './adminView';

const WINDOWS: QuotaWindowKind[] = ['5h', '1w', '1m'];

const view = useAdminView();
view.setTitle(t('groupsTitle'), t('groupsSubtitle'));

const groups = ref<Group[]>([]);
const policies = ref<QuotaPolicy[]>([]);
const models = ref<AdminModel[]>([]);
const error = ref('');
const loaded = ref(false);

const panelOpen = ref(false);
const existing = ref<Group | null>(null);
const busy = ref(false);
const panelError = ref('');
const nameField = ref<InstanceType<typeof OaTextField> | null>(null);

interface WindowForm {
  enabled: boolean;
  requests: number | null;
  tokens: number | null;
  credits: number | null;
}

const form = ref({
  name: '',
  description: '',
  isDefault: false,
  allowAll: false,
  apiAccess: true,
  allowStats: true,
  allowDelete: true,
  sortOrder: 0 as number | null,
  grants: {} as Record<string, 'use' | 'view'>,
  rpm: null as number | null,
  tpm: null as number | null,
  windows: {} as Record<QuotaWindowKind, WindowForm>,
});

const creating = computed(() => existing.value === null);

const modelItems = computed<ListItem[]>(() => models.value.map((model) => ({
  value: model.id,
  label: model.display_name,
  sub: `${model.provider_name} · ${model.model_id}`,
})));

function windowLabel(kind: QuotaWindowKind): string {
  return kind === '5h' ? t('every5h') : kind === '1w' ? t('everyWeek') : t('everyMonth');
}

function policyFor(groupID: string): QuotaPolicy {
  return policies.value.find((policy) => policy.scope === 'group' && policy.scope_id === groupID)
    ?? emptyPolicy('group', groupID);
}

const columns = computed<Array<Column<Group>>>(() => [
  { key: 'group', header: t('colGroup') },
  { key: 'members', header: t('colMembers'), text: (row) => String(row.members), numeric: true, width: '80px' },
  { key: 'models', header: t('colModels'), width: '130px' },
  { key: 'limits', header: t('colLimits'), secondary: true, width: '130px' },
  { key: 'default', header: '', width: '90px' },
]);

function limitBadges(policy: QuotaPolicy): string[] {
  const parts: string[] = [];
  if (policy.rpm) parts.push(t('perMinute', { count: policy.rpm }));
  for (const kind of WINDOWS) {
    const window = policy.windows[kind];
    if (!window?.enabled) continue;
    const figure = window.credits ?? window.tokens ?? window.requests;
    parts.push(figure === null || figure === undefined ? kind : `${kind}: ${figure}`);
  }
  if (!parts.length) parts.push(t('inherits'));
  return parts;
}

function open(row: Group | null): void {
  existing.value = row;
  panelError.value = '';
  const policy = row ? policyFor(row.id) : emptyPolicy('group', '');

  const grants: Record<string, 'use' | 'view'> = {};
  if (row?.model_grants && row.model_grants.length > 0) {
    for (const grant of row.model_grants) {
      if (grant.access === 'use' || grant.access === 'view') grants[grant.model_id] = grant.access;
    }
  } else if (row?.model_ids) {
    for (const id of row.model_ids) grants[id] = 'use';
  }

  const windows = {} as Record<QuotaWindowKind, WindowForm>;
  for (const kind of WINDOWS) {
    const limits: QuotaLimits = policy.windows[kind]
      ?? { enabled: null, requests: null, tokens: null, credits: null };
    windows[kind] = {
      enabled: limits.enabled === true,
      requests: limits.requests,
      tokens: limits.tokens,
      credits: limits.credits,
    };
  }

  form.value = {
    name: row?.name ?? '',
    description: row?.description ?? '',
    isDefault: row?.is_default ?? false,
    allowAll: row?.allow_all_models ?? false,
    // Ticked for a new group, matching the column default: the instance-wide
    // switch is the deliberate act, and this narrows it rather than standing
    // in for it.
    apiAccess: row?.api_access ?? true,
    // What members may do with the interface, as opposed to which models they
    // may reach. Both default to on, so a new group can do everything an
    // existing one can until somebody takes it away.
    allowStats: row?.allow_stats ?? true,
    allowDelete: row?.allow_delete_conversations ?? true,
    sortOrder: row?.sort_order ?? 0,
    grants,
    rpm: policy.rpm,
    tpm: policy.tpm,
    windows,
  };

  panelOpen.value = true;
  void nextTick(() => nameField.value?.focus({ preventScroll: true }));
}

async function save(): Promise<void> {
  busy.value = true;
  panelError.value = '';
  try {
    const modelGrants = form.value.allowAll
      ? []
      : Object.entries(form.value.grants).map(([model_id, access]) => ({ model_id, access }));
    const modelIDs = form.value.allowAll
      ? []
      : Object.entries(form.value.grants).filter(([, access]) => access === 'use').map(([id]) => id);

    const payload: Record<string, unknown> = {
      name: form.value.name.trim(),
      description: form.value.description.trim(),
      is_default: form.value.isDefault,
      allow_all_models: form.value.allowAll,
      api_access: form.value.apiAccess,
      allow_stats: form.value.allowStats,
      allow_delete_conversations: form.value.allowDelete,
      sort_order: form.value.sortOrder ?? 0,
      model_ids: modelIDs,
      model_grants: modelGrants,
    };

    const saved = creating.value
      ? (await adminApi.createGroup(payload)).group
      : (await adminApi.updateGroup(existing.value!.id, payload)).group;

    await adminApi.savePolicy({
      scope: 'group',
      scope_id: saved.id,
      rpm: form.value.rpm,
      tpm: form.value.tpm,
      windows: Object.fromEntries(WINDOWS.map((kind) => [kind, {
        enabled: form.value.windows[kind].enabled ? true : null,
        requests: form.value.windows[kind].requests,
        tokens: form.value.windows[kind].tokens,
        credits: form.value.windows[kind].credits,
      }])),
    });

    panelOpen.value = false;
    view.reload();
  } catch (failure) {
    busy.value = false;
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

async function remove(): Promise<void> {
  const row = existing.value;
  if (!row) return;
  busy.value = true;
  try {
    await adminApi.deleteGroup(row.id);
    panelOpen.value = false;
    view.reload();
  } catch (failure) {
    busy.value = false;
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

async function load(): Promise<void> {
  error.value = '';
  try {
    const [groupsResult, modelsResult] = await Promise.all([adminApi.groups(), adminApi.models()]);
    groups.value = groupsResult.groups;
    policies.value = groupsResult.policies;
    models.value = modelsResult.models;
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
    <button id="addGroup" type="button" class="oa-btn primary" @click="open(null)">{{ t('addGroup') }}</button>
  </Teleport>

  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!loaded" class="oa-table-empty">{{ t('loading') }}</p>

  <template v-else>
    <OaTable
      id="groupsList"
      :columns="columns"
      :rows="groups"
      :empty="t('noGroups')"
      selectable
      @select="open($event)"
    >
      <template #cell-group="{ row }">
        <OaCellStack :title="row.name" :sub="row.description || undefined" />
      </template>
      <template #cell-models="{ row }">
        <OaBadge tone="muted">
          {{ row.allow_all_models ? t('allModels') : t('nAllowed', { count: row.model_ids.length }) }}
        </OaBadge>
      </template>
      <template #cell-limits="{ row }">
        <OaBadgeRow>
          <OaBadge v-for="part in limitBadges(policyFor(row.id))" :key="part" tone="muted">
            {{ part }}
          </OaBadge>
        </OaBadgeRow>
      </template>
      <template #cell-default="{ row }">
        <OaBadge v-if="row.is_default">{{ t('defaultBadge') }}</OaBadge>
        <span v-else />
      </template>
    </OaTable>

    <p class="oa-field-hint">{{ t('groupsFooterHint') }}</p>
  </template>

  <OaPanel
    v-if="panelOpen"
    :title="creating ? t('addGroup') : existing!.name"
    :confirm-label="creating ? t('add') : t('save')"
    :destructive-label="existing && !existing.is_default ? t('deleteLabel') : undefined"
    :destructive-confirm="existing && !existing.is_default
      ? tn(existing.members, 'confirmDeleteGroupOne', 'confirmDeleteGroupOther', { name: existing.name })
      : undefined"
    :width="460"
    :busy="busy"
    :error="panelError"
    @close="panelOpen = false"
    @confirm="save"
    @destructive="remove"
  >
    <OaTextField
      ref="nameField"
      v-model="form.name"
      :label="t('name')"
      :placeholder="t('groupNamePlaceholder')"
      :max-length="40"
    />
    <OaTextArea
      v-model="form.description"
      :label="t('description')"
      :rows="2"
      :hint="t('groupDescriptionHint')"
    />
    <OaSwitchField v-model="form.isDefault" :label="t('isDefault')" :hint="t('isDefaultHint')" />
    <OaNumberField v-model="form.sortOrder" :label="t('sortOrder')" />

    <OaFormSection :title="t('secModelAccess')" />
    <OaSwitchField
      v-model="form.allowAll"
      :label="t('allowAllModels')"
      :hint="t('allowAllModelsHint')"
    />
    <OaTierList
      v-if="!form.allowAll"
      v-model="form.grants"
      :label="t('allowedModels')"
      :hint="t('modelAccessTiersHint')"
      :items="modelItems"
      :empty-text="t('noModelsConfigured')"
    />

    <OaFormSection :title="t('apiKeys')" />
    <OaSwitchField
      v-model="form.apiAccess"
      :label="t('groupApiAccess')"
      :hint="t('groupApiAccessHint')"
    />

    <OaFormSection :title="t('secGroupAbilities')" />
    <OaSwitchField
      v-model="form.allowStats"
      :label="t('groupAllowStats')"
      :hint="t('groupAllowStatsHint')"
    />
    <OaSwitchField
      v-model="form.allowDelete"
      :label="t('groupAllowDelete')"
      :hint="t('groupAllowDeleteHint')"
    />

    <OaFormSection :title="t('secAllowance')" :hint="t('allowanceHint')" />
    <OaNumberField
      v-model="form.rpm"
      :label="t('requestsPerMinute')"
      :placeholder="t('inherit')"
      :min="0"
      :hint="t('inheritHint')"
    />
    <OaNumberField
      v-model="form.tpm"
      :label="t('tokensPerMinute')"
      :placeholder="t('inherit')"
      :min="0"
    />

    <template v-for="kind in WINDOWS" :key="kind">
      <OaFormSection :title="windowLabel(kind)" />
      <OaSwitchField
        v-model="form.windows[kind].enabled"
        :label="t('enforceWindow', { window: windowLabel(kind).toLowerCase() })"
      />
      <OaNumberField
        v-model="form.windows[kind].requests"
        :label="t('limitRequests')"
        :placeholder="t('noLimit')"
        :min="0"
      />
      <OaNumberField
        v-model="form.windows[kind].tokens"
        :label="t('limitTokens')"
        :placeholder="t('noLimit')"
        :min="0"
      />
      <CreditsField v-model="form.windows[kind].credits" />
    </template>
  </OaPanel>
</template>
