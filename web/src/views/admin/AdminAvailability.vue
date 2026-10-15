<script setup lang="ts">
// Availability, liveness detection, and degradation settings.
//
// Consolidates model health policies, automated probing, service degradation
// thresholds, user-facing uptime visibility, and manual uptime reset into a
// single screen.

import { computed, onMounted, ref } from 'vue';
import { adminApi, type ModelHealth } from '@/admin/api';
import { ApiError } from '@/api/client';
import { loadModels } from '@/chat/useModels';
import OaBadge from '@/components/OaBadge.vue';
import OaSearchField from '@/components/OaSearchField.vue';
import { matchesSearch } from '@/lib/search';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import AdminControlCard from './AdminControlCard.vue';
import AdminWorkbench from './AdminWorkbench.vue';
import type { WorkbenchGroup } from './workbench';
import { useSettingsDraft } from './settingsDraft';
import { IconPulse, IconLock, IconSliders, IconTrash, IconUsers } from '@/icons';
import OaNumberField from '@/components/OaNumberField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import { t } from '@/composables/useI18n';
import { maskProvider } from '@/admin/safeMode';
import AdminFailure from './AdminFailure.vue';
import { useAdminView } from './adminView';

const view = useAdminView();
view.setTitle(() => t('navAvailability'), () => t('availabilitySubtitle'));

const error = ref('');
const loaded = ref(false);
const models = ref<ModelHealth[]>([]);
const flash = ref('');
const flashSuccess = ref('');
const saveLabel = ref('');
const busy = ref(false);
const resetting = ref(false);
const probing = ref(false);
const probeCompleted = ref(0);
const probeTotal = ref(0);

const form = ref({
  healthProbe: true,
  healthWindow: 30 as number | null,
  healthDisableAfter: 0 as number | null,
  healthDisableBelow: 0 as number | null,
  healthWarnBelow: 90 as number | null,
  healthShowUsers: false,
  healthRetainDays: 14 as number | null,
});

// A blank box is not a value. It used to be saved as the default it stood for,
// and then the box was left blank on screen, so the page showed one number and
// stored another. A blank box now keeps the save shut until a number is typed,
// and "0" is how auto-disable is turned off.
function collect(): Record<string, string> {
  return {
    'health.probe': String(form.value.healthProbe),
    'health.window_minutes': String(form.value.healthWindow ?? ''),
    'health.disable_after': String(form.value.healthDisableAfter ?? ''),
    'health.disable_below': String(form.value.healthDisableBelow ?? ''),
    'health.warn_below': String(form.value.healthWarnBelow ?? ''),
    'health.show_users': String(form.value.healthShowUsers),
    'health.retain_days': String(form.value.healthRetainDays ?? ''),
  };
}

const settingsValid = computed(() => {
  const f = form.value;
  return f.healthWindow !== null && f.healthDisableAfter !== null && f.healthDisableBelow !== null
    && f.healthWarnBelow !== null && f.healthRetainDays !== null;
});

const { dirty, changes, accept } = useSettingsDraft(collect);

async function save(): Promise<void> {
  if (!loaded.value || error.value || busy.value || !settingsValid.value) return;
  // Only what this page changed, so the save cannot undo another screen's edit.
  const values = changes();
  if (Object.keys(values).length === 0) return;
  busy.value = true;
  saveLabel.value = t('saving');
  flash.value = '';
  flashSuccess.value = '';
  try {
    await adminApi.saveSettings(values);
    accept(values);
    saveLabel.value = t('saved');
    window.setTimeout(() => { saveLabel.value = ''; }, 1500);
  } catch (failure) {
    flash.value = failure instanceof ApiError ? failure.message : String(failure);
    saveLabel.value = '';
  } finally {
    busy.value = false;
  }
}

async function resetUptime(): Promise<void> {
  resetting.value = true;
  flash.value = '';
  flashSuccess.value = '';
  try {
    await adminApi.resetHealth();
    flashSuccess.value = t('resetUptimeDone');
    window.setTimeout(() => { flashSuccess.value = ''; }, 3000);
    await Promise.all([refreshHealth(), loadModels()]);
  } catch (failure) {
    flash.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    resetting.value = false;
  }
}

async function probeAllModels(): Promise<void> {
  probing.value = true;
  probeCompleted.value = 0;
  probeTotal.value = 0;
  flash.value = '';
  flashSuccess.value = '';
  try {
    const result = await adminApi.probeAllModels((progress) => {
      probeCompleted.value = progress.completed;
      probeTotal.value = progress.total;
    });
    flashSuccess.value = t('probeAllModelsDone', {
      succeeded: result.succeeded,
      total: result.total,
      failed: result.failed,
      skipped: result.skipped,
    });
    window.setTimeout(() => { flashSuccess.value = ''; }, 5000);
    await Promise.all([refreshHealth(), loadModels()]);
  } catch (failure) {
    flash.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    probing.value = false;
  }
}

// A health refresh must not reload settings and discard a policy draft.
async function refreshHealth(): Promise<void> { models.value = (await adminApi.health()).models; }
const modelQuery = ref('');
const filteredModels = computed(() => models.value.filter((model) => matchesSearch(modelQuery.value, model.name, model.provider)));
const counts = computed(() => ({
  up: models.value.filter((m) => m.status.state === 'up' && !m.auto_disabled).length,
  down: models.value.filter((m) => m.status.state === 'down' || m.auto_disabled).length,
  unknown: models.value.filter((m) => m.status.state === 'unknown' && !m.auto_disabled).length,
}));

async function load(): Promise<void> {
  error.value = '';
  try {
    const [settingsData, healthData] = await Promise.all([
      adminApi.settings(),
      adminApi.health(),
    ]);
    const values = settingsData.settings;
    form.value = {
      healthProbe: values['health.probe'] !== 'false',
      healthWindow: Number(values['health.window_minutes'] ?? 30),
      healthDisableAfter: Number(values['health.disable_after'] ?? 0),
      healthDisableBelow: Number(values['health.disable_below'] ?? 0),
      healthWarnBelow: Number(values['health.warn_below'] ?? 90),
      healthShowUsers: values['health.show_users'] === 'true',
      healthRetainDays: Number(values['health.retain_days'] ?? 14),
    };
    models.value = healthData.models;
    accept();
  } catch (failure) {
    error.value = failure instanceof Error ? failure.message : String(failure);
  } finally {
    loaded.value = true;
  }
}

const categories: WorkbenchGroup[] = [
  { id: 'status', label: 'controlHealth', hint: 'controlHealthHint', icon: IconPulse, sections: ['modelHealthOverview'] },
  { id: 'protection', label: 'controlProtection', hint: 'controlProtectionHint', icon: IconLock, sections: ['secDegradationPolicy', 'secProbingWindow'] },
  { id: 'maintenance', label: 'controlMaintenance', hint: 'controlMaintenanceHint', icon: IconSliders, sections: ['secUserVisibility', 'secResetUptime'] },
];

onMounted(load);
</script>

<template>
  <Teleport :to="view.actionsHost">
    <span v-if="loaded && !error" class="oa-control-save-state" :class="{ dirty }" role="status">
      <span class="oa-dashboard-dot" />{{ dirty ? t('controlUnsaved') : t('controlSaved') }}
    </span>
    <button type="button" class="oa-btn primary" :disabled="busy || !loaded || !!error || !settingsValid" @click="save">
      {{ saveLabel || t('save') }}
    </button>
  </Teleport>
  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!loaded" class="oa-table-empty">{{ t('loading') }}</p>
  <AdminWorkbench v-else page="availability" :groups="categories" :searchable="false" v-slot="{ visible }">
    <p v-if="!settingsValid" class="oa-field-hint" role="alert">{{ t('settingNumberRequired') }}</p>
    <div v-show="visible('modelHealthOverview')" class="oa-health-summary oa-control-card-wide">
      <div><IconPulse :size="22" /><span>{{ t('controlTotalModels') }}</span><strong>{{ models.length }}</strong></div>
      <div><span>{{ t('uptimeOperational') }}</span><strong>{{ counts.up }}</strong></div>
      <div :class="{ 'has-errors': counts.down > 0 }"><span>{{ t('controlNeedsAttention') }}</span><strong>{{ counts.down }}</strong></div>
      <div><span>{{ t('uptimeNoData') }}</span><strong>{{ counts.unknown }}</strong></div>
    </div>
    <AdminControlCard id="modelHealthOverview" v-show="visible('modelHealthOverview')" class="oa-control-card-wide"
      :title="t('modelHealthOverview')" :hint="t('controlHealthHintDetail')" :icon="IconPulse">
      <template #actions><div class="oa-control-actions">
        <button type="button" class="oa-btn" :disabled="probing || resetting" @click="probeAllModels">
          {{ probing ? probeTotal > 0 ? t('probeAllModelsProgress', { completed: probeCompleted, total: probeTotal }) : t('probeAllModelsRunning') : t('probeAllModels') }}
        </button>
        <a href="/uptime" target="_blank" rel="noopener" class="oa-btn">{{ t('viewUptimePage') }}</a>
      </div></template>
      <OaSearchField v-model="modelQuery" :label="t('controlHealthSearch')" />
      <p v-if="!models.length" class="oa-table-empty">{{ t('noModelsConfigured') }}</p>
      <p v-else-if="!filteredModels.length" class="oa-table-empty" role="status">{{ t('noSearchResults') }}</p>
      <div v-else class="oa-health-grid">
        <article v-for="m in filteredModels" :key="m.model_id" class="oa-health-model" :class="{ 'has-errors': m.auto_disabled || m.status.state === 'down' }">
          <div class="oa-health-model-heading"><span class="oa-dashboard-dot" />
            <h4>{{ m.name }}</h4><OaBadge :tone="m.auto_disabled || m.status.state === 'down' ? 'danger' : 'muted'">
              {{ m.auto_disabled || m.status.state === 'down' ? t('uptimeOutage') : m.status.state === 'up' ? t('uptimeOperational') : t('uptimeNoData') }}
            </OaBadge>
          </div>
          <p>{{ maskProvider(m.provider) }}</p>
          <div class="oa-health-model-value"><strong>{{ m.status.samples > 0 ? (m.status.uptime * 100).toFixed(1) + '%' : '—' }}</strong>
            <span>{{ m.status.samples }} {{ t('statRequests') }}</span></div>
          <div class="oa-health-meter" aria-hidden="true"><span :style="{ width: `${m.status.samples > 0 ? Math.min(100, Math.max(0, m.status.uptime * 100)) : 0}%` }" /></div>
        </article>
      </div>
    </AdminControlCard>
    <AdminControlCard id="secDegradationPolicy" v-show="visible('secDegradationPolicy')" :title="t('secDegradationPolicy')" :icon="IconLock">
      <OaNumberField
        v-model="form.healthWarnBelow"
        :label="t('healthWarnBelow')"
        :min="0"
        :max="100"
        :hint="t('healthWarnBelowHint')"
      />
      <OaNumberField
        v-model="form.healthDisableBelow"
        :label="t('healthDisableBelow')"
        :min="0"
        :max="100"
        :hint="t('healthDisableBelowHint')"
      />
      <OaNumberField
        v-model="form.healthDisableAfter"
        :label="t('healthDisableAfter')"
        :min="0"
        :hint="t('healthDisableAfterHint')"
      />
    </AdminControlCard>
    <AdminControlCard id="secProbingWindow" v-show="visible('secProbingWindow')" :title="t('secProbingWindow')" :icon="IconPulse" :hint="t('livenessHint')">
      <OaSwitchField v-model="form.healthProbe" :label="t('healthProbe')" :hint="t('healthProbeHint')" />
      <OaNumberField v-model="form.healthWindow" :label="t('healthWindow')" :min="1" :hint="t('healthWindowHint')" />
      <OaNumberField
        v-model="form.healthRetainDays"
        :label="t('healthRetainDays')"
        :min="1"
        :hint="t('healthRetainDaysHint')"
      />
    </AdminControlCard>
    <AdminControlCard id="secUserVisibility" v-show="visible('secUserVisibility')" :title="t('secUserVisibility')" :icon="IconUsers">
      <OaSwitchField
        v-model="form.healthShowUsers"
        :label="t('healthShowUsers')"
        :hint="t('healthShowUsersHint')"
      />
    </AdminControlCard>
    <AdminControlCard id="secResetUptime" v-show="visible('secResetUptime')" :title="t('secResetUptime')" :icon="IconTrash" :hint="t('resetUptimeHint')">
      <div class="oa-field">
        <OaConfirmButton
          class="oa-btn"
          :label="t('resetUptime')"
          :armed-label="t('resetUptimeConfirm')"
          :armed-title="t('resetUptime')"
          :resting-title="t('resetUptime')"
          :disabled="resetting || probing"
          @confirm="resetUptime"
        />
      </div>
    </AdminControlCard>
  </AdminWorkbench>
  <p v-if="flashSuccess" class="oa-drawer-flash visible ok oa-control-flash" role="status">{{ flashSuccess }}</p>
  <p v-if="flash" class="oa-drawer-flash visible oa-control-flash" role="alert">{{ flash }}</p>
</template>
