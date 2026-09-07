<script setup lang="ts">
// Theme, accent and language.
//
// The accent picker is the same control it always was — ten dots and a custom
// hex, one hue clamped per scheme so the choice works against both a white
// and a near-black background. It is carried over rather than redesigned
// because it was already the best thing on the settings panel.

import { computed, ref, watch } from 'vue';
import OaSelectField from '@/components/OaSelectField.vue';
import { changeLanguage, currentLanguage, t, type Language, type StringKey } from '@/composables/useI18n';
import { useTheme } from '@/composables/useTheme';
import { ACCENTS, ACCENT_NAMES, baseAccent, normalizeHex, type AccentName } from '@/theme/color-utils';
import { setAccentPreference, type ThemeMode } from '@/theme/theme';
import { persistTheme, syncPreferences } from '@/stores/session';

const theme = useTheme();

const preference = computed(() => theme.accent());
const custom = computed(() => preference.value.accent === 'custom');
const base = computed(() => baseAccent(preference.value));

// A dot click updates these two as well, so they read as "here is the hex you
// just picked" rather than freezing on whatever was last typed.
const hex = ref(base.value);
watch(base, (next) => { hex.value = next; });

const labels: Record<AccentName, StringKey> = {
  violet: 'accentViolet', neutral: 'accentNeutral', red: 'accentRed', pink: 'accentPink',
  indigo: 'accentIndigo', blue: 'accentBlue', cyan: 'accentCyan', teal: 'accentTeal',
  green: 'accentGreen', orange: 'accentOrange',
};

function apply(accent: AccentName | 'custom', value: string): void {
  const normalized = accent === 'custom' ? normalizeHex(value, null) : '';
  if (accent === 'custom' && !normalized) return;
  setAccentPreference({ accent, customAccent: normalized ?? '' });
  syncPreferences({ accent, custom_accent: normalized ?? '' });
}

function onTheme(mode: ThemeMode): void {
  // Applied through the session, so the choice follows the account to another
  // device rather than living only in this browser.
  persistTheme(mode);
}

function onLanguage(next: Language): void {
  void changeLanguage(next);
  syncPreferences({ language: next });
}
</script>

<template>
  <div class="oa-settings-panel">
    <h2 class="oa-admin-section-title">{{ t('secAppearance') }}</h2>

    <OaSelectField
      :model-value="theme.mode()"
      :label="t('theme')"
      :options="[
        { value: 'auto', label: t('themeAuto') },
        { value: 'light', label: t('themeLight') },
        { value: 'dark', label: t('themeDark') },
      ]"
      @update:model-value="onTheme"
    />

    <span class="oa-field-label">{{ t('accentColour') }}</span>
    <span class="oa-field-hint">{{ t('accentHint') }}</span>

    <div class="oa-color-grid">
      <button
        v-for="name in ACCENT_NAMES"
        :key="name"
        type="button"
        class="oa-color-dot"
        :class="{ active: !custom && preference.accent === name }"
        :style="{ backgroundColor: ACCENTS[name] }"
        :title="t(labels[name])"
        :aria-label="t(labels[name])"
        @click="apply(name, preference.customAccent)"
      />
    </div>

    <div class="oa-color-custom-row oa-input-row" :class="{ active: custom }">
      <input type="color" :value="base" @input="apply('custom', ($event.target as HTMLInputElement).value)">
      <input
        v-model="hex"
        type="text"
        placeholder="#6C4CD6"
        maxlength="7"
        @change="apply('custom', hex)"
      >
    </div>

    <OaSelectField
      :model-value="currentLanguage()"
      :label="t('languageLabel')"
      :hint="t('languageHint')"
      :options="[{ value: 'en', label: 'English' }, { value: 'zh', label: '中文' }]"
      @update:model-value="onLanguage"
    />
  </div>
</template>
