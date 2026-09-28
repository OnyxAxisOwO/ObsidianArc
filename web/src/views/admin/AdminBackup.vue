<script setup lang="ts">
// Instance backup covers every account and conversation, so its credentials
// and controls stay on one page behind the super administrator gate.

import { computed, onMounted, ref, watch } from 'vue';
import { useDocumentVisibility, useIntervalFn } from '@vueuse/core';
import { adminApi, type AdminBackup, type AdminBackupInput } from '@/admin/api';
import { ApiError } from '@/api/client';
import OaBadge from '@/components/OaBadge.vue';
import OaNumberField from '@/components/OaNumberField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t } from '@/composables/useI18n';
import { IconArchive, IconServer } from '@/icons';
import { absoluteTime } from '@/lib/format';
import AdminControlCard from './AdminControlCard.vue';
import AdminFailure from './AdminFailure.vue';
import { useAdminView } from './adminView';

type BackupForm = Omit<AdminBackupInput, 'interval_hours' | 'retention_days'> & {
  interval_hours: number | null;
  retention_days: number | null;
};

const view = useAdminView();
view.setTitle(t('navBackup'), t('backupSubtitle'));

const snapshot = ref<AdminBackup | null>(null);
const form = ref<BackupForm>({
  enabled: false,
  endpoint: '',
  bucket: '',
  region: '',
  prefix: '',
  access_key_id: '',
  secret_access_key: '',
  interval_hours: 24,
  retention_days: 7,
});
const baseline = ref('');
const loaded = ref(false);
const loading = ref(false);
const busy = ref(false);
const error = ref('');
const actionError = ref('');
const notice = ref('');
const logRef = ref<HTMLPreElement | null>(null);

function collect(): AdminBackupInput {
  return {
    ...form.value,
    interval_hours: form.value.interval_hours ?? 24,
    retention_days: form.value.retention_days ?? 7,
  };
}

const dirty = computed(() => baseline.value !== JSON.stringify(collect()));
const actionDisabled = computed(() => (
  busy.value || dirty.value || !snapshot.value?.configured || !!snapshot.value.running
));
const statusLabel = computed(() => {
  if (snapshot.value?.running) return t('backupStatusRunning');
  switch (snapshot.value?.last_status) {
    case 'success': return t('backupStatusSuccess');
    case 'error': return t('backupStatusError');
    case 'running': return t('backupStatusUnknown');
    default: return t('backupStatusIdle');
  }
});
const statusTone = computed<'default' | 'muted' | 'danger' | 'warning'>(() => {
  if (snapshot.value?.running) return 'warning';
  if (snapshot.value?.last_status === 'error') return 'danger';
  return snapshot.value?.last_status === 'success' ? 'default' : 'muted';
});
const nextRunLabel = computed(() => {
  const current = snapshot.value;
  if (!current || !current.enabled) return t('backupNotScheduled');
  if (current.running) return t('backupStatusRunning');
  return current.next_run_at ? absoluteTime(current.next_run_at) : t('backupNotScheduled');
});

const visibility = useDocumentVisibility();
let inFlightCount = 0;
let refreshSeq = 0;
const statusPollInterval = computed(() => snapshot.value?.running ? 5000 : 30000);
const { pause: pauseStatusPoll, resume: resumeStatusPoll } = useIntervalFn(() => {
  if (visibility.value === 'visible') void refreshStatus();
}, statusPollInterval, { immediate: false });
watch(
  () => [snapshot.value?.running, visibility.value] as const,
  ([, visible]) => {
    if (visible === 'visible') resumeStatusPoll();
    else pauseStatusPoll();
  },
  { immediate: true },
);
watch(dirty, (isDirty) => {
  if (isDirty && loaded.value) {
    notice.value = '';
  }
});
watch(
  () => snapshot.value?.last_log,
  () => {
    if (logRef.value && snapshot.value?.running) {
      logRef.value.scrollTop = logRef.value.scrollHeight;
    }
  },
  { flush: 'post' },
);

function message(failure: unknown): string {
  return failure instanceof ApiError ? failure.message : String(failure);
}

function backupTime(at: number): string {
  return at ? absoluteTime(at) : t('backupNever');
}

async function load(): Promise<void> {
  loading.value = true;
  error.value = '';
  try {
    const data = await adminApi.backup();
    snapshot.value = data;
    // Secret values never come back from the API. Keeping these fields blank
    // lets a write preserve the stored credentials unless the operator enters
    // replacements.
    form.value = {
      enabled: data.enabled,
      endpoint: data.endpoint,
      bucket: data.bucket,
      region: data.region,
      prefix: data.prefix,
      access_key_id: '',
      secret_access_key: '',
      interval_hours: data.interval_hours,
      retention_days: data.retention_days,
    };
    baseline.value = JSON.stringify(collect());
    loaded.value = true;
  } catch (failure) {
    error.value = message(failure);
  } finally {
    loading.value = false;
  }
}

// Polling drops overlapping ticks, but explicit actions (save, test, run)
// force a fresh fetch. Tracking inFlightCount ensures an older request
// finishing cannot prematurely re-enable background polling while a newer
// forced fetch is still running. Background polling never overwrites user
// action errors.
async function refreshStatus(fromAction = false): Promise<void> {
  if (inFlightCount > 0 && !fromAction) return;
  inFlightCount++;
  const seq = ++refreshSeq;
  try {
    const data = await adminApi.backup();
    if (seq === refreshSeq) {
      snapshot.value = data;
    }
  } catch (failure) {
    if (fromAction && seq === refreshSeq) {
      actionError.value = message(failure);
    }
  } finally {
    inFlightCount--;
  }
}

async function save(): Promise<void> {
  if (!loaded.value || busy.value || !dirty.value) return;
  busy.value = true;
  actionError.value = '';
  notice.value = '';
  const input = collect();
  try {
    await adminApi.saveBackup(input);
    // Clear the one-time input only if it still contains what this request
    // saved. Edits made while the request was in flight stay dirty for the
    // next save.
    if (form.value.access_key_id === input.access_key_id) form.value.access_key_id = '';
    if (form.value.secret_access_key === input.secret_access_key) form.value.secret_access_key = '';
    baseline.value = JSON.stringify({ ...input, access_key_id: '', secret_access_key: '' });
    notice.value = t('backupSaved');
    await refreshStatus(true);
  } catch (failure) {
    actionError.value = message(failure);
  } finally {
    busy.value = false;
  }
}

async function testStorage(): Promise<void> {
  if (actionDisabled.value) return;
  busy.value = true;
  actionError.value = '';
  notice.value = '';
  try {
    await adminApi.testBackup();
    notice.value = t('backupTestSucceeded');
    await refreshStatus(true);
  } catch (failure) {
    actionError.value = message(failure);
  } finally {
    busy.value = false;
  }
}

async function runBackup(): Promise<void> {
  if (actionDisabled.value) return;
  busy.value = true;
  actionError.value = '';
  notice.value = '';
  try {
    await adminApi.runBackup();
    notice.value = t('backupRunStarted');
    await refreshStatus(true);
  } catch (failure) {
    actionError.value = message(failure);
  } finally {
    busy.value = false;
  }
}

onMounted(load);
</script>

<template>
  <Teleport :to="view.actionsHost">
    <span v-if="loaded" class="oa-control-save-state" :class="{ dirty }" role="status">
      <span class="oa-dashboard-dot" />{{ dirty ? t('controlUnsaved') : t('controlSaved') }}
    </span>
    <button type="button" class="oa-btn primary" :disabled="busy || !loaded || !!error || !dirty" @click="save">
      {{ busy ? t('saving') : t('save') }}
    </button>
  </Teleport>

  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!loaded || loading" class="oa-table-empty">{{ t('loading') }}</p>
  <div v-else class="oa-workbench">
    <div class="oa-workbench-grid">
      <AdminControlCard id="backupConfig" :title="t('backupConfig')" :hint="t('backupConfigHint')" :icon="IconArchive">
        <OaSwitchField v-model="form.enabled" :label="t('backupEnabled')" :hint="t('backupEnabledHint')" />
        <OaTextField v-model="form.endpoint" :label="t('backupEndpoint')" :hint="t('backupEndpointHint')" placeholder="https://storage.example.com" monospace />
        <OaTextField v-model="form.bucket" :label="t('backupBucket')" :hint="t('backupBucketHint')" />
        <OaTextField v-model="form.region" :label="t('backupRegion')" :hint="t('backupRegionHint')" />
        <OaTextField v-model="form.prefix" :label="t('backupPrefix')" :hint="t('backupPrefixHint')" monospace />
        <OaTextField v-model="form.access_key_id" :label="t('backupAccessKey')" autocomplete="off" />
        <OaTextField v-model="form.secret_access_key" type="password" :label="t('backupSecretKey')" autocomplete="new-password" />
        <p class="oa-field-hint">{{ t('backupCredentialsHint') }}</p>
        <OaNumberField v-model="form.interval_hours" :label="t('backupInterval')" :hint="t('backupIntervalHint')" :min="1" :step="1" />
        <OaNumberField v-model="form.retention_days" :label="t('backupRetention')" :hint="t('backupRetentionHint')" :min="1" :step="1" />
      </AdminControlCard>

      <AdminControlCard id="backupStatus" :title="t('backupStatus')" :hint="t('backupStatusHint')" :icon="IconServer">
        <dl class="oa-resource-definition">
          <div>
            <dt>{{ t('backupLastStatus') }}</dt>
            <dd><OaBadge :tone="statusTone">{{ statusLabel }}</OaBadge></dd>
          </div>
          <div>
            <dt>{{ t('backupStorageStatus') }}</dt>
            <dd><OaBadge :tone="snapshot?.configured ? 'default' : 'warning'">{{ snapshot?.configured ? t('backupStorageReady') : t('backupStorageMissing') }}</OaBadge></dd>
          </div>
          <div>
            <dt>{{ t('backupCredentialsStatus') }}</dt>
            <dd><OaBadge :tone="snapshot?.secret_configured ? 'default' : 'warning'">{{ snapshot?.secret_configured ? t('backupCredentialsSaved') : t('backupCredentialsMissing') }}</OaBadge></dd>
          </div>
          <div>
            <dt>{{ t('backupLastStarted') }}</dt>
            <dd>{{ snapshot ? backupTime(snapshot.last_started_at) : t('backupNever') }}</dd>
          </div>
          <div>
            <dt>{{ t('backupLastFinished') }}</dt>
            <dd>{{ snapshot ? backupTime(snapshot.last_finished_at) : t('backupNever') }}</dd>
          </div>
          <div>
            <dt>{{ t('backupLastSuccess') }}</dt>
            <dd>{{ snapshot ? backupTime(snapshot.last_success_at) : t('backupNever') }}</dd>
          </div>
          <div>
            <dt>{{ t('backupNextRun') }}</dt>
            <dd>{{ nextRunLabel }}</dd>
          </div>
        </dl>
        <p v-if="snapshot?.last_error" class="oa-field-hint" role="status">
          <strong>{{ t('backupLastError') }}:</strong> {{ snapshot.last_error }}
        </p>
        <div class="oa-control-actions">
          <button type="button" class="oa-btn" :disabled="actionDisabled" @click="testStorage">
            {{ t('backupTest') }}
          </button>
          <button type="button" class="oa-btn primary" :disabled="actionDisabled" @click="runBackup">
            {{ busy && !dirty ? t('backupStatusRunning') : t('backupRun') }}
          </button>
        </div>
        <p v-if="dirty" class="oa-field-hint">{{ t('backupActionsHint') }}</p>
        <p v-else-if="!snapshot?.configured" class="oa-field-hint">{{ t('backupStorageMissing') }}</p>
        <p v-if="actionError" class="oa-field-hint" role="alert">{{ actionError }}</p>
        <p v-else-if="notice" class="oa-field-hint" role="status">{{ notice }}</p>
        <div class="oa-backup-log-section">
          <div class="oa-backup-log-head">
            <span class="oa-field-label">{{ t('backupLog') }}</span>
            <span v-if="snapshot?.running" class="oa-backup-log-live">
              <span class="oa-dashboard-dot" />{{ t('backupStatusRunning') }}
            </span>
          </div>
          <pre ref="logRef" class="oa-backup-log-body" role="log">{{ snapshot?.last_log || t('backupLogEmpty') }}</pre>
        </div>
      </AdminControlCard>
    </div>
  </div>
</template>
