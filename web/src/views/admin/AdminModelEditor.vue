<script setup lang="ts">
// The panel that adds or edits one model. The page owns the state — which row
// is open, the form, whether a save is running — because opening, saving and
// the address's hash are page decisions; this file only draws the form.
//
// The form arrives as a v-model and its fields are written in place, the way
// they were when this was part of the page: the object is the page's own
// reactive form, so a field typed here is the field the page saves.

import { computed, ref } from 'vue';
import { useRouter } from 'vue-router';
import type { AdminModel, Group, Meta, ModelHealth, Provider, UsageBreakdown } from '@/admin/api';
import OaBadge from '@/components/OaBadge.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaNumberField from '@/components/OaNumberField.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaRow from '@/components/OaRow.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTextArea from '@/components/OaTextArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import OaTierList from '@/components/OaTierList.vue';
import type { ListItem } from '@/components/list-items';
import { t } from '@/composables/useI18n';
import { IconCheck, IconCopy } from '@/icons';
import { maskProvider } from '@/admin/safeMode';
import AdminControlCard from './AdminControlCard.vue';
import AdminDetectModels, { type DetectedModel } from './AdminDetectModels.vue';
import ReasoningTiers from './ReasoningTiers.vue';
import { reasoningLabel } from './reasoning-labels';
import { useAdminView } from './adminView';
import type { ModelForm } from './modelForm';
import UsageBoard from './usage/UsageBoard.vue';

const props = defineProps<{
  existing: AdminModel | null;
  many: boolean;
  providers: Pick<Provider, 'id' | 'name' | 'kind' | 'enabled'>[];
  groups: Pick<Group, 'id' | 'name'>[];
  models: AdminModel[];
  meta: Meta;
  status: ModelHealth | undefined;
  users: UsageBreakdown[] | null;
  busy: boolean;
  error: string;
  idCopied: boolean;
}>();

const emit = defineEmits<{
  close: [];
  confirm: [];
  destructive: [];
  duplicate: [];
  copyId: [id: string];
}>();

const form = defineModel<ModelForm>('form', { required: true });
const detected = defineModel<DetectedModel[]>('detected', { required: true });

const router = useRouter();
const view = useAdminView();

const panel = ref<InstanceType<typeof OaPanel> | null>(null);
const modelIDField = ref<InstanceType<typeof OaTextField> | null>(null);

const creating = computed(() => props.existing === null);

const groupItems = computed<ListItem[]>(() => props.groups.map((group) => ({
  value: group.id,
  label: group.name,
})));

// Every other model is a candidate except this one and any that is already
// routed: resolution is a single hop, so a chain would not do what the second
// link says. The server refuses both as well.
const routeChoices = computed(() => [
  { value: '', label: t('routeNone') },
  ...props.models
    .filter((entry) => entry.id !== props.existing?.id && !entry.route_to_id)
    .map((entry) => ({ value: entry.id, label: `${entry.display_name} — ${maskProvider(entry.provider_name)}` })),
]);

// An image model's whole cost is its per-request weight: there are no tokens
// to charge for, so the three token weights multiply out to nothing. That
// weight starts at zero while the token weights start at one, so ticking this
// box and saving used to produce a model that is free to use however often —
// which is not what turning image generation on is meant to mean. Seeded
// rather than enforced: an operator who wants it free can still set it back.
// It runs from the switch, not a watch on the flag: a watch also fires when
// open() loads a stored image model, and a free one saved at 0 came back as 1.
function setImageGen(on: boolean): void {
  form.value.imageGen = on;
  if (on && !form.value.requestWeight) form.value.requestWeight = 1;
}

// The page's panel slot closes this through `close`, as it would the panel
// itself, and focuses the first field when it opens one.
defineExpose({
  close: (): void => { panel.value?.close(); },
  focusModelID: (): void => { modelIDField.value?.focus({ preventScroll: true }); },
});
</script>

<template>
  <OaPanel
    ref="panel"
    :title="creating ? t('addModel') : existing!.display_name"
    :confirm-label="many ? t('addPickedN', { count: detected.length }) : creating ? t('add') : t('save')"
    :destructive-label="existing ? t('deleteLabel') : undefined"
    :destructive-confirm="existing ? t('confirmDeleteModel', { name: existing.display_name }) : undefined"
    :busy="busy"
    :error="error"
    @close="emit('close')"
    @confirm="emit('confirm')"
    @destructive="emit('destructive')"
  >
    <template v-if="existing" #actions>
      <OaIconButton class="oa-icon-btn" :label="t('duplicate')" @click="emit('duplicate')">
        <IconCopy :size="16" />
      </OaIconButton>
    </template>

    <AdminControlCard :title="t('colProvider')">
      <template v-if="creating">
        <OaSelectField
          v-model="form.providerID"
          :label="t('colProvider')"
          :options="providers.map((provider) => ({ value: provider.id, label: provider.name }))"
        />
        <!-- Detect belongs here as well as on the provider screen: this is the
             form where an upstream id has to be typed exactly, so it is where
             being handed the list saves the typing. -->
        <AdminDetectModels v-model:picked="detected" :provider-id="form.providerID" mode="pick" />
      </template>
      <!-- A field that shows a value the form cannot change. -->
      <template v-else>
        <OaRow class="oa-fact-row" :title="t('colProvider')">
          <span class="oa-row-value">{{ maskProvider(existing!.provider_name) }}</span>
        </OaRow>
        <OaRow class="oa-fact-row" :title="t('modelUniqueID')">
          <span class="oa-row-value mono" :title="existing!.id">{{ existing!.id }}</span>
          <OaIconButton
            class="oa-icon-btn tiny"
            :label="idCopied ? t('copied') : t('copy')"
            @click="emit('copyId', existing!.id)"
          >
            <IconCheck v-if="idCopied" :size="12" />
            <IconCopy v-else :size="12" />
          </OaIconButton>
        </OaRow>
      </template>
    </AdminControlCard>

    <AdminControlCard :title="t('secModelBasics')">
      <!-- With several picked, each brings its own id and name, and an API
           name could only be one model's: those fields give way to the list. -->
      <OaRow
        v-if="many"
        :title="t('addingModelsN', { count: detected.length })"
        :meta="detected.map((entry) => entry.model_id).join(', ')"
      />
      <template v-else>
        <OaTextField
          ref="modelIDField"
          v-model="form.modelID"
          :label="t('modelIDLabel')"
          placeholder="anthropic/claude-opus-5"
          :hint="t('modelIDHint')"
          monospace
        />
        <OaTextField
          v-model="form.apiName"
          :label="t('apiNameLabel')"
          :placeholder="form.modelID || 'gpt-5.6-sol'"
          :hint="t('apiNameHint')"
          monospace
        />
        <OaTextField
          v-model="form.displayName"
          :label="t('displayName')"
          placeholder="Claude Opus 5"
          :hint="t('displayNameHint')"
          :max-length="80"
        />
      </template>
      <OaTextArea
        v-model="form.description"
        :label="t('description')"
        :placeholder="t('modelDescriptionPlaceholder')"
        :rows="2"
        :hint="t('modelDescriptionHint')"
      />
      <OaTextArea
        v-model="form.systemPrompt"
        :label="t('modelPromptLabel')"
        :rows="4"
        :hint="t('modelPromptHint')"
      />
    </AdminControlCard>

    <AdminControlCard :title="t('secModelShown')">
      <OaSwitchField v-model="form.enabled" :label="t('enabled')" />
      <OaSwitchField v-model="form.hidden" :label="t('modelHidden')" :hint="t('modelHiddenHint')" />
      <OaNumberField v-model="form.sortOrder" :label="t('sortOrder')" />
    </AdminControlCard>

    <!-- Why a model is down, in the panel where somebody is about to act on it. -->
    <AdminControlCard v-if="existing" :title="t('secHealth')">
      <!-- The verdict and the way to its evidence on one row; then one row per
           kind of failure, its count where a control would sit. -->
      <OaRow
        :meta="!status || status.status.samples === 0
          ? t('healthNoEvidence')
          : t('healthSummary', {
            uptime: (status.status.uptime * 100).toFixed(1),
            users: status.status.user_samples,
            system: status.status.system_samples,
          })"
      >
        <button
          type="button"
          class="oa-btn small"
          @click="router.push({ path: '/admin/logs', query: { model_id: existing.id, outcome: 'failed', window: '' } })"
        >{{ t('viewModelErrorHistory') }}</button>
      </OaRow>
      <p v-if="status?.auto_disabled" class="oa-group-flash">{{ t('healthAutoDisabled') }}</p>
      <OaRow
        v-for="failure in status?.status.errors ?? []"
        :key="failure.code"
        class="oa-error-row"
        :title="failure.code"
        :meta="failure.message || undefined"
      >
        <OaBadge tone="muted">{{ failure.count }}×</OaBadge>
      </OaRow>
    </AdminControlCard>

    <!-- Who reaches for this model, before the controls that decide who may:
         taking a model away from a group reads differently once the names of
         the people using it are on the screen. -->
    <AdminControlCard v-if="existing && users && users.length" id="secWhoUsesIt" :title="t('whoUsesIt')" :hint="t('whoUsesItHint')">
      <template v-if="view.open" #actions>
        <button type="button" class="oa-btn small" @click="view.open?.('/admin/usage', { model: existing.id })">{{ t('viewInUsage') }}</button>
      </template>
      <UsageBoard
        :rows="users" kind="user" metric="tokens" :limit="5" :reach="false"
        :selectable="!!view.open" :empty-text="t('nothingYet')"
        @select="view.open?.('/admin/usage', { model: existing.id, user: $event })"
      />
    </AdminControlCard>

    <AdminControlCard id="secGroupAccess" :title="t('secGroupAccess')">
      <OaTierList
        v-model="form.groupGrants"
        :label="t('groupsTitle')"
        :hint="t('groupAccessHint')"
        :items="groupItems"
        :empty-text="t('noGroups')"
      />
    </AdminControlCard>

    <AdminControlCard id="secCapabilities" :title="t('secCapabilities')" :hint="t('capabilitiesHint')">
      <OaSwitchField
        :model-value="form.imageGen"
        :label="t('capImageGen')"
        :hint="t('capImageGenHint')"
        @update:model-value="setImageGen"
      />
      <OaSwitchField
        v-model="form.chatImageGen"
        :label="t('capChatImageGen')"
        :hint="t('capChatImageGenHint')"
      />
      <OaSwitchField v-model="form.reasoning" :label="t('capReasoning')" :hint="t('capReasoningHint')" />
      <OaSwitchField v-model="form.images" :label="t('capImages')" :hint="t('capImagesHint')" />
      <OaSwitchField v-model="form.vision" :label="t('capVision')" />
      <OaSwitchField v-model="form.streaming" :label="t('capStreams')" />
      <OaSwitchField v-model="form.systemPromptSupported" :label="t('capSystemPrompt')" />
      <OaSwitchField v-model="form.tools" :label="t('capTools')" />
      <OaSwitchField
        v-model="form.emulateTools"
        :label="t('capEmulateTools')"
        :hint="t('capEmulateToolsHint')"
      />
      <OaNumberField v-model="form.contextWindow" :label="t('contextWindow')" placeholder="200000" :min="0" />
      <OaNumberField v-model="form.maxOutput" :label="t('maxOutputTokens')" placeholder="8192" :min="0" />
    </AdminControlCard>

    <AdminControlCard id="secRouting" :title="t('secRouting')">
      <OaSelectField
        v-model="form.routeTo"
        :label="t('routeTo')"
        :hint="t('routeToHint')"
        :options="routeChoices"
      />
    </AdminControlCard>

    <!-- The style and the tiers are one subject — how this model is asked to
         think — and they used to sit under Routing, which is a different one. -->
    <AdminControlCard id="secThinking" :title="t('secThinking')">
      <OaSelectField
        v-model="form.reasoningStyle"
        :label="t('reasoningStyleModel')"
        :hint="t('reasoningStyleModelHint')"
        :options="[
          { value: '', label: t('styleInherit') },
          ...meta.reasoning_styles.map((value) => ({ value, label: reasoningLabel(value) })),
        ]"
      />
      <ReasoningTiers v-model="form.tiers" />
    </AdminControlCard>

    <AdminControlCard id="secRequestOverride" :title="t('secRequestOverride')" :hint="t('requestOverrideHint')">
      <OaTextArea
        v-model="form.requestOverride"
        :label="t('requestOverride')"
        placeholder='{"reasoning_effort": "low"}'
        :rows="3"
        :hint="t('requestOverrideFieldHint')"
      />
    </AdminControlCard>

    <AdminControlCard id="secWeights" :title="t('secWeights')" :hint="t('weightsHint')">
      <OaNumberField
        v-model="form.requestWeight"
        :label="t('perRequest')"
        :step="0.1"
        :min="0"
        :hint="t('perRequestHint')"
      />
      <OaNumberField v-model="form.inputWeight" :label="t('per1kInput')" :step="0.1" :min="0" />
      <OaNumberField v-model="form.outputWeight" :label="t('per1kOutput')" :step="0.1" :min="0" />
      <OaNumberField v-model="form.reasoningWeight" :label="t('per1kReasoning')" :step="0.1" :min="0" />
    </AdminControlCard>
  </OaPanel>
</template>
