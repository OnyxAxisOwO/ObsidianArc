<script setup lang="ts">
// An action card declared by a plugin, drawn with the backoffice's own controls.
//
// Unlike settings cards which edit persistent key-value configuration, an action
// card holds parameters for an operation (such as a mass card grant), validates
// them, and triggers execution through an action button with optional confirmation.

import { computed, ref } from 'vue';
import { t } from '@/composables/useI18n';
import { IconSpark } from '@/icons';
import type { ActionCardSpec } from '@/plugins/types';
import AdminControlCard from './AdminControlCard.vue';
import PluginActionControl from './PluginActionControl.vue';
import PluginRunButton from './PluginRunButton.vue';
import { missingRequired, visibleControls } from './pluginControls';
import { daysFromNow } from './localTime';
import { failureText } from './shared';

// A datetime control the plugin left unset defaults to this many days from now.
const DEFAULT_EXPIRY_DAYS = 7;

const props = defineProps<{
  card: ActionCardSpec;
}>();

const emit = defineEmits<{
  (event: 'done'): void;
}>();

const draft = ref<Record<string, string>>({ ...(props.card.defaults ?? {}) });
const busy = ref(false);
// The success green is a literal: no --ai-* token carries it, and it is the
// green the stylesheets use for success too (.oa-2fa-flash.ok).
const result = ref<{ message: string; failed: boolean } | null>(null);

// Initialise any unset datetime controls to a sensible default (7 days). A
// default of '' the plugin gave is kept: an empty time can mean "now".
for (const control of props.card.controls) {
  if (control.kind === 'datetime' && draft.value[control.key] === undefined) {
    draft.value[control.key] = daysFromNow(DEFAULT_EXPIRY_DAYS);
  }
}

function set(key: string, value: string): void {
  draft.value = { ...draft.value, [key]: value };
}

const shown = computed(() => visibleControls(props.card.controls, draft.value));
const valid = computed(() => !missingRequired(shown.value, draft.value));

async function runAction(): Promise<void> {
  busy.value = true;
  result.value = null;
  try {
    const feedback = await props.card.button.run(draft.value);
    result.value = { message: feedback, failed: false };
    emit('done');
  } catch (failure: unknown) {
    result.value = { message: failureText(failure), failed: true };
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <AdminControlCard
    :id="card.id"
    :title="card.title()"
    :hint="card.hint?.() ?? ''"
    :icon="card.icon ?? IconSpark"
  >
    <div class="oa-plugin-controls">
      <div v-for="control in shown" :key="control.key" class="oa-plugin-control-item">
        <PluginActionControl :control="control" :draft="draft" @set="set" />
      </div>

      <div class="oa-card-actions">
        <!-- A destructive button is asked in place even when the plugin wrote no
             question for it: the core draws the danger, so it confirms too. -->
        <PluginRunButton
          :label="busy ? t('loading') : card.button.label()"
          :armed-label="card.button.armedLabel ? card.button.armedLabel() : card.button.label()"
          :armed-title="card.button.confirm ? card.button.confirm(draft) : card.button.label()"
          :asks="!!(card.button.confirm || card.button.danger)"
          :danger="!!card.button.danger"
          :disabled="busy || !valid"
          @run="runAction"
        />
      </div>

      <p
        v-if="result"
        class="oa-field-hint"
        :style="{ color: result.failed ? 'var(--ai-danger)' : '#10b981' }"
        role="status"
      >
        {{ result.message }}
      </p>
    </div>
  </AdminControlCard>
</template>
