<script setup lang="ts">
// One card of a plugin's settings, drawn with the page's own controls.
//
// The page owns the values: it loads the section's keys with its own, keeps
// them in `draft`, and sends them with its own save — so a plugin's settings
// dirty, save and fail exactly like the card beside them. This component is
// only the layout the plugin declared.

import { IconSliders } from '@/icons';
import type { SettingsSection } from '@/plugins/types';
import AdminControlCard from './AdminControlCard.vue';
import PluginSettingControls from './PluginSettingControls.vue';

defineProps<{
  section: SettingsSection;
  /** The values being edited, by key. Secrets are empty until typed into. */
  draft: Record<string, string>;
  /** What is stored for each secret, as the server masked it. */
  hints: Record<string, string>;
}>();
</script>

<template>
  <AdminControlCard :id="section.id" :title="section.title()" :hint="section.hint?.() ?? ''" :icon="section.icon ?? IconSliders">
    <PluginSettingControls :section="section" :draft="draft" :hints="hints" />
  </AdminControlCard>
</template>
