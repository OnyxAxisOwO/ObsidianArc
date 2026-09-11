<script setup lang="ts">
// The screen where an account issues keys for the API.
//
// The shape of it is decided by one fact — the token exists once. The server
// keeps a digest and nothing else, so there is no "show key" button to build
// and no way to recover one that was not copied. That makes the moment of
// creation the whole design: the new token is shown in a panel of its own,
// selected and ready to copy, with the warning next to it rather than after
// it. Everything else here — renaming, re-expiring, pausing, model
// restrictions, revoking — is housekeeping on rows that no longer contain a
// secret.

import { computed, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { ApiError, api } from '@/api/client';
import { createKey, deleteKey, listKeys, updateKey, type ApiKey } from '@/api/keys';
import { copyToClipboard } from '@/chat/markdown';
import type { AvailableModel } from '@/chat/useModels';
import OaBadge from '@/components/OaBadge.vue';
import OaCheckList from '@/components/OaCheckList.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaTextField from '@/components/OaTextField.vue';
import OaTurnstile from '@/components/OaTurnstile.vue';
import type { ListItem } from '@/components/list-items';
import { t, type StringKey } from '@/composables/useI18n';
import { IconCheck, IconCopy, IconGear, IconPause, IconPlay, IconTrash } from '@/icons';
import { absoluteTime, relativeTime } from '@/lib/format';
import { currentUser, siteInfo } from '@/stores/session';

/** Expiry choices, as days from now. Zero is "never". */
const LIFETIMES: Array<{ days: number; label: StringKey }> = [
  { days: 0, label: 'keyNever' },
  { days: 7, label: 'keyDays7' },
  { days: 30, label: 'keyDays30' },
  { days: 90, label: 'keyDays90' },
  { days: 365, label: 'keyDays365' },
];

const DAY_MS = 24 * 60 * 60 * 1000;
// How long a revoke button stays armed. Long enough to read the question and
// decide; short enough that an armed button left alone goes back to being a
// safe one.
const ARM_MS = 6000;

const router = useRouter();

const keys = ref<ApiKey[]>([]);
const models = ref<AvailableModel[]>([]);
const enabled = ref(false);
const max = ref(0);
const loaded = ref(false);
const busy = ref(false);
const error = ref('');

/**
 * The token from the most recent creation, held only until the panel is
 * repainted past it. Never written anywhere else.
 */
const issued = ref<{ key: ApiKey; token: string } | null>(null);
const editing = ref<ApiKey | null>(null);

// --- the create form ---------------------------------------------------------

const newName = ref('');
const newLifetime = ref('0');
const newModels = ref<string[]>([]);
const nameField = ref<InstanceType<typeof OaTextField> | null>(null);
const guard = ref<InstanceType<typeof OaTurnstile> | null>(null);

// --- the edit form -----------------------------------------------------------

const editName = ref('');
const editLifetime = ref('keep');
const editStatus = ref<'active' | 'paused'>('active');
const editModels = ref<string[]>([]);

const full = computed(() => keys.value.length >= max.value);
const apiWarning = computed(() => {
  const account = currentUser.value;
  if (account?.api_restricted &&
      (account.api_restricted_until === 0 || account.api_restricted_until > Date.now())) {
    return account.api_restricted_until > 0
      ? t('apiRestrictedUntil', { when: absoluteTime(account.api_restricted_until) })
      : t('apiRestrictedIndefinitely');
  }
  return t('apiDisabledForYou');
});
const lifetimeChoices = computed(() => LIFETIMES.map((entry) => ({ value: String(entry.days), label: t(entry.label) })));
const endpoint = `${window.location.origin}/v1`;

const usableModels = computed(() => models.value.filter((model) => model.usable !== false));
const modelItems = computed<ListItem[]>(() =>
  usableModels.value.map((model) => ({ value: model.id, label: model.display_name || model.id })));

function itemsFor(selected: string[]): ListItem[] {
  const items = [...modelItems.value];
  const known = new Set(items.map((item) => item.value));
  for (const id of selected) if (!known.has(id)) items.unshift({ value: id, label: id });
  return items;
}

function modelName(id: string): string {
  return models.value.find((model) => model.id === id)?.display_name || id;
}

function modelIDsFor(row: ApiKey): string[] {
  if (row.model_ids?.length) return row.model_ids;
  return row.model_id ? [row.model_id] : [];
}

function expired(row: ApiKey): boolean {
  return row.expires_at > 0 && row.expires_at <= Date.now();
}

function expiryLabel(row: ApiKey): string {
  if (!row.expires_at) return t('keyNoExpiry');
  if (expired(row)) return t('keyExpiredAt', { when: absoluteTime(row.expires_at) });
  return t('keyExpiresAt', { when: absoluteTime(row.expires_at) });
}

async function refresh(): Promise<void> {
  try {
    const [result, modelsResult] = await Promise.all([
      listKeys(),
      api.get<{ models: AvailableModel[] }>('/api/models').catch(() => ({ models: [] })),
    ]);
    keys.value = result.keys;
    enabled.value = result.enabled;
    max.value = result.max;
    models.value = modelsResult.models ?? [];
  } catch (failure) {
    error.value = failure instanceof ApiError ? failure.message : t('failed');
  } finally {
    loaded.value = true;
  }
}

async function create(): Promise<void> {
  const label = newName.value.trim();
  if (!label) {
    error.value = t('keyNameRequired');
    nameField.value?.focus();
    return;
  }
  const days = Number(newLifetime.value);
  busy.value = true;
  error.value = '';
  try {
    issued.value = await createKey(
      label,
      days > 0 ? Date.now() + days * DAY_MS : 0,
      newModels.value,
      guard.value?.token() ?? '',
    );
    newName.value = '';
    newModels.value = [];
    newLifetime.value = '0';
    await refresh();
  } catch (failure) {
    error.value = failure instanceof ApiError ? failure.message : t('failed');
  } finally {
    busy.value = false;
  }
}

async function togglePaused(row: ApiKey): Promise<void> {
  busy.value = true;
  try {
    await updateKey(row.id, { disabled: !row.disabled });
    await refresh();
  } catch (failure) {
    error.value = failure instanceof ApiError ? failure.message : t('failed');
  } finally {
    busy.value = false;
  }
}

function beginEdit(row: ApiKey): void {
  editing.value = row;
  editName.value = row.name;
  editLifetime.value = 'keep';
  editStatus.value = row.disabled ? 'paused' : 'active';
  editModels.value = modelIDsFor(row);
}

async function saveEdit(): Promise<void> {
  const row = editing.value;
  if (!row) return;
  const label = editName.value.trim();
  if (!label) {
    error.value = t('keyNameRequired');
    return;
  }
  const changes: { name?: string; expires_at?: number; disabled?: boolean; model_ids?: string[] } = {
    name: label,
    disabled: editStatus.value === 'paused',
    model_ids: editModels.value,
  };
  if (editLifetime.value !== 'keep') {
    const days = Number(editLifetime.value);
    changes.expires_at = days > 0 ? Date.now() + days * DAY_MS : 0;
  }
  busy.value = true;
  try {
    await updateKey(row.id, changes);
    editing.value = null;
    await refresh();
  } catch (failure) {
    error.value = failure instanceof ApiError ? failure.message : t('failed');
  } finally {
    busy.value = false;
  }
}

// Revoking is immediate and cannot be undone, so the button asks first — in
// place, because a dialog the browser may suppress is a button that silently
// does nothing.
const armed = ref('');
let armTimer = 0;

function revoke(row: ApiKey): void {
  if (armed.value !== row.id) {
    armed.value = row.id;
    window.clearTimeout(armTimer);
    armTimer = window.setTimeout(() => { armed.value = ''; }, ARM_MS);
    return;
  }
  armed.value = '';
  busy.value = true;
  void deleteKey(row.id)
    .then(refresh)
    .catch((failure: unknown) => {
      error.value = failure instanceof ApiError ? failure.message : t('failed');
    })
    .finally(() => { busy.value = false; });
}

const copied = ref(false);
const endpointCopied = ref(false);

function copyToken(): void {
  const token = issued.value?.token;
  if (!token) return;
  void copyToClipboard(token).then((ok) => {
    if (!ok) return;
    copied.value = true;
    window.setTimeout(() => { copied.value = false; }, 1600);
  });
}

function copyEndpoint(): void {
  void copyToClipboard(endpoint).then((ok) => {
    if (!ok) return;
    endpointCopied.value = true;
    window.setTimeout(() => { endpointCopied.value = false; }, 1500);
  });
}

onMounted(() => void refresh());
</script>

<template>
  <OaPanel
    :title="t('apiKeys')"
    :footer="false"
    :width="520"
    :error="error"
    @close="router.push('/')"
  >
    <!-- The one moment the token exists. -->
    <section v-if="issued" class="oa-key-issued oa-key-issued-animated">
      <h3 class="oa-panel-section-title">{{ t('keyCreated', { name: issued.key.name }) }}</h3>
      <p class="oa-key-warning">{{ t('keyShownOnce') }}</p>

      <!-- An input rather than a block of text: it can be selected with one
           gesture, and copied by a keyboard on a browser whose clipboard API
           is unavailable or refused. -->
      <div class="oa-key-token oa-key-token-pulse">
        <input
          class="oa-key-token-input"
          type="text"
          readonly
          :value="issued.token"
          @focus="($event.target as HTMLInputElement).select()"
        >
        <OaIconButton
          class="oa-icon-btn oa-key-copy-btn"
          :class="{ copied }"
          :label="t('copy')"
          @click="copyToken"
        >
          <IconCheck v-if="copied" :size="15" />
          <IconCopy v-else :size="15" />
        </OaIconButton>
      </div>

      <div class="oa-key-endpoint">
        <div class="oa-key-endpoint-text">
          <span class="oa-field-label">{{ t('apiBaseUrl') }}</span>
          <code>{{ endpoint }}</code>
        </div>
        <OaIconButton
          class="oa-icon-btn"
          :label="endpointCopied ? t('copied') : t('copy')"
          @click="copyEndpoint"
        ><IconCopy :size="15" /></OaIconButton>
      </div>

      <button type="button" class="oa-btn primary" @click="issued = null">{{ t('keyCopied') }}</button>
    </section>

    <p v-else-if="!loaded" class="oa-menu-empty">{{ t('loading') }}</p>

    <!-- Editing one row replaces the panel's contents rather than opening a
         second column: it is the same record, being looked at more closely. -->
    <section v-else-if="editing" class="oa-keys-create oa-keys-edit-animated">
      <h3 class="oa-panel-section-title">{{ t('keyEdit') }}</h3>
      <OaTextField v-model="editName" :label="t('keyName')" :max-length="60" />
      <OaSelectField
        v-model="editStatus"
        :label="t('keyStatus')"
        :options="[
          { value: 'active', label: t('keyStatusActive') },
          { value: 'paused', label: t('keyStatusPaused') },
        ]"
      />
      <!-- "Leave as it is" is a real choice here, and the only one that does
           not silently move an expiry the owner set deliberately. -->
      <OaSelectField
        v-model="editLifetime"
        :label="t('keyExpires')"
        :options="[
          { value: 'keep', label: t('keyKeepExpiry', { when: expiryLabel(editing) }) },
          ...lifetimeChoices,
        ]"
      />
      <OaCheckList
        v-model="editModels"
        :label="t('keyModel')"
        :hint="t('keyModelHint')"
        :items="itemsFor(modelIDsFor(editing))"
        :empty-text="t('keyNoModels')"
      />
      <div class="oa-key-edit-actions">
        <button type="button" class="oa-btn" @click="editing = null">{{ t('cancel') }}</button>
        <button type="button" class="oa-btn primary" :disabled="busy" @click="saveEdit">
          {{ t('save') }}
        </button>
      </div>
    </section>

    <template v-else>
      <section class="oa-keys-intro">
        <p class="oa-field-hint">{{ t('apiKeysIntro') }}</p>
        <p v-if="!enabled" class="oa-key-warning">{{ apiWarning }}</p>
        <!-- The base URL to paste into a client, which is the other half of a
             key. It exists to be pasted somewhere else, and selecting
             monospace text out of a rounded box by hand is the part nobody
             enjoys. -->
        <div v-else class="oa-key-endpoint">
          <div class="oa-key-endpoint-text">
            <span class="oa-field-label">{{ t('apiBaseUrl') }}</span>
            <code>{{ endpoint }}</code>
          </div>
          <OaIconButton
            class="oa-icon-btn"
            :label="endpointCopied ? t('copied') : t('copy')"
            @click="copyEndpoint"
          ><IconCopy :size="15" /></OaIconButton>
        </div>
      </section>

      <section v-if="enabled" class="oa-keys-create">
        <h3 class="oa-panel-section-title">{{ t('keyNew') }}</h3>
        <p v-if="full" class="oa-field-hint">{{ t('keyLimitReached', { count: max }) }}</p>
        <template v-else>
          <OaTextField
            ref="nameField"
            v-model="newName"
            :label="t('keyName')"
            :placeholder="t('keyNamePlaceholder')"
            :max-length="60"
          />
          <OaSelectField v-model="newLifetime" :label="t('keyExpires')" :options="lifetimeChoices" />
          <OaCheckList
            v-model="newModels"
            :label="t('keyModel')"
            :hint="t('keyModelHint')"
            :items="modelItems"
            :empty-text="t('keyNoModels')"
          />
          <!-- Where the operator asked for one. A key outlives the session
               that asked for it, which is the thing a stolen cookie would
               rather turn into. -->
          <OaTurnstile
            v-if="siteInfo.turnstile_on_api_key"
            ref="guard"
            :site-key="siteInfo.turnstile_site_key ?? ''"
          />
          <button type="button" class="oa-btn primary" :disabled="busy" @click="create">
            {{ t('keyCreate') }}
          </button>
        </template>
      </section>

      <section class="oa-keys-list-wrap">
        <h3 class="oa-panel-section-title">{{ t('keyYours') }}</h3>
        <p v-if="!keys.length" class="oa-menu-empty">{{ t('keysEmpty') }}</p>
        <div v-else class="oa-keys-list">
          <div
            v-for="(row, index) in keys"
            :key="row.id"
            class="oa-key-row"
            :class="{ 'oa-key-row-paused': row.disabled }"
            :style="{ '--item-idx': String(index) }"
          >
            <div class="oa-key-info">
              <div class="oa-key-title">
                <span class="oa-key-name">{{ row.name }}</span>
                <OaBadge v-if="row.disabled" tone="warning">{{ t('keyPaused') }}</OaBadge>
                <OaBadge v-if="expired(row)" tone="danger">{{ t('keyExpired') }}</OaBadge>
              </div>
              <div class="oa-key-meta">
                <code class="oa-key-prefix">{{ row.prefix }}…</code>
                <span
                  v-if="modelIDsFor(row).length"
                  class="oa-key-model-pill"
                  :title="modelIDsFor(row).join(', ')"
                >{{ t('keyOnlyModel', { model: modelIDsFor(row).map(modelName).join(', ') }) }}</span>
                <span v-else class="oa-key-model-all">{{ t('keyAllModels') }}</span>
                <span>{{ expiryLabel(row) }}</span>
                <span>
                  {{ row.last_used_at
                    ? t('keyLastUsed', { when: relativeTime(row.last_used_at) })
                    : t('keyNeverUsed') }}
                </span>
              </div>
            </div>

            <div class="oa-key-actions">
              <OaIconButton
                class="oa-icon-btn oa-key-toggle-btn"
                :class="{ active: row.disabled }"
                :label="row.disabled ? t('keyResume') : t('keyPause')"
                :hidden="armed === row.id"
                :disabled="busy"
                @click="togglePaused(row)"
              >
                <IconPlay v-if="row.disabled" :size="15" />
                <IconPause v-else :size="15" />
              </OaIconButton>
              <OaIconButton
                class="oa-icon-btn"
                :label="t('edit')"
                :hidden="armed === row.id"
                @click="beginEdit(row)"
              ><IconGear :size="15" /></OaIconButton>
              <OaIconButton
                class="oa-icon-btn danger"
                :label="t('keyRevoke')"
                @click="revoke(row)"
              >
                <span v-if="armed === row.id" class="oa-key-confirm">{{ t('keyRevokeConfirm') }}</span>
                <IconTrash v-else :size="15" />
              </OaIconButton>
            </div>
          </div>
        </div>
      </section>
    </template>
  </OaPanel>
</template>
