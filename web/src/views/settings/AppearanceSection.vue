<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { Palette } from 'lucide-vue-next';
import OaSelectField from '@/components/OaSelectField.vue';
import { changeLanguage, currentLanguage, t, type Language, type StringKey } from '@/composables/useI18n';
import { useTheme } from '@/composables/useTheme';
import { IconAuto, IconCheck, IconMoon, IconSpark, IconSun } from '@/icons';
import { ACCENTS, ACCENT_NAMES, accentPalette, baseAccent, normalizeHex, type AccentName } from '@/theme/color-utils';
import { backgroundAccent, setBackgroundAccent, setAccentPreference, type ThemeMode } from '@/theme/theme';
import { persistTheme, syncPreferences } from '@/stores/session';
import WallpaperSection from './WallpaperSection.vue';

const theme = useTheme();
const preference = computed(() => theme.accent());
const custom = computed(() => preference.value.accent === 'custom');
const base = computed(() => baseAccent(preference.value));
const background = computed(() => (void theme.version.value, backgroundAccent()));
const hex = ref(base.value);
watch(base, (next) => { hex.value = next; });

const labels: Record<AccentName, StringKey> = {
  violet: 'accentViolet', neutral: 'accentNeutral', red: 'accentRed', pink: 'accentPink',
  indigo: 'accentIndigo', blue: 'accentBlue', cyan: 'accentCyan', teal: 'accentTeal',
  green: 'accentGreen', orange: 'accentOrange',
};
const backgrounds: Array<{ value: AccentName; label: StringKey }> = [
  { value: 'orange', label: 'backgroundVanilla' }, { value: 'teal', label: 'backgroundMint' },
  { value: 'blue', label: 'backgroundCloud' }, { value: 'violet', label: 'backgroundLilac' },
  { value: 'pink', label: 'backgroundPeach' },
];
const modes = [
  { value: 'auto', label: 'themeAuto', icon: IconAuto },
  { value: 'light', label: 'themeLight', icon: IconSun },
  { value: 'dark', label: 'themeDark', icon: IconMoon },
] as const;

function apply(accent: AccentName | 'custom', value: string): void {
  const normalized = accent === 'custom' ? normalizeHex(value, null) : '';
  if (accent === 'custom' && !normalized) { hex.value = base.value; return; }
  setAccentPreference({ accent, customAccent: normalized ?? '' });
  syncPreferences({ accent, custom_accent: normalized ?? '' });
}
// The native picker fires input for every step of a drag. Only the screen
// follows those; the account is told once, on change, so the server is not
// sent a run of saves that can land out of order and leave an earlier colour
// stored.
function previewCustom(value: string): void {
  const normalized = normalizeHex(value, null);
  if (normalized) setAccentPreference({ accent: 'custom', customAccent: normalized });
}
function chooseBackground(value: AccentName | ''): void {
  setBackgroundAccent(value);
  // The palette also colours panels over a wallpaper, so neither choice replaces the other.
  syncPreferences({ background_accent: value });
}
function onTheme(mode: ThemeMode): void { persistTheme(mode); }
const languageFlash = ref('');
async function onLanguage(next: Language): Promise<void> {
  languageFlash.value = '';
  try {
    await changeLanguage(next);
  } catch {
    // The dictionary did not arrive, so the language did not change. The
    // account must not be told about a choice the reader never saw.
    languageFlash.value = t('languageLoadFailed');
    return;
  }
  // A later pick may have taken over while this dictionary loaded; setLanguage
  // then leaves the language alone, and that later pick saves its own choice.
  if (currentLanguage() !== next) return;
  syncPreferences({ language: next });
}
</script>

<template>
  <div class="oa-settings-panel oa-appearance">
    <header class="oa-appearance-intro">
      <span class="oa-appearance-kicker"><IconSpark :size="14" />{{ t('secAppearance') }}</span>
      <h2>{{ t('appearancePreview') }}</h2>
      <p>{{ t('appearanceIntro') }}</p>
    </header>
    <div class="oa-appearance-preview" :aria-label="t('appearancePreviewHint')">
      <div class="oa-preview-message"><IconSpark :size="17" /><span>{{ t('appearancePreviewHint') }}</span></div>
      <div class="oa-preview-message sent"><IconCheck :size="14" /><span>{{ t('appearanceIntro') }}</span></div>
    </div>
    <!-- The instance has taken the look over. Said once, in place of the
         controls, rather than leaving pickers that change nothing. -->
    <p v-if="theme.enforced()" class="oa-field-hint">{{ t('themeEnforcedHint') }}</p>
    <section v-if="!theme.enforced()" class="oa-appearance-section">
      <div class="oa-appearance-heading"><h3>{{ t('chatBackground') }}</h3><button type="button" class="oa-btn" @click="chooseBackground('')">{{ t('reset') }}</button></div>
      <div class="oa-background-grid">
        <button v-for="entry in backgrounds" :key="entry.value" type="button" class="oa-background-choice"
          :class="{ active: background === entry.value }"
          :aria-pressed="background === entry.value" @click="chooseBackground(entry.value)">
          <span class="oa-background-swatch" :style="{ background: accentPalette(ACCENTS[entry.value], theme.dark())['--ai-field-bg'] }">
            <span v-if="background === entry.value" class="oa-background-check"><IconCheck :size="13" /></span>
          </span>
          <span>{{ t(entry.label) }}</span>
        </button>
      </div>
      <p class="oa-field-hint">{{ t('backgroundHint') }}</p>
      <WallpaperSection />
    </section>
    <section v-if="!theme.enforced()" class="oa-appearance-section">
      <div class="oa-appearance-heading"><h3>{{ t('controlColours') }}</h3><Palette :size="16" /></div>
      <div class="oa-color-grid">
        <button v-for="name in ACCENT_NAMES" :key="name" type="button" class="oa-color-dot"
          :class="{ active: !custom && preference.accent === name }" :style="{ backgroundColor: ACCENTS[name] }"
          :title="t(labels[name])" :aria-label="t(labels[name])" :aria-pressed="!custom && preference.accent === name" @click="apply(name, preference.customAccent)">
          <IconCheck v-if="!custom && preference.accent === name" :size="16" />
        </button>
        <label class="oa-custom-colour" :class="{ active: custom }" :title="t('customColour')">
          <Palette :size="18" /><input type="color" :aria-label="t('customColour')" :value="base" @input="previewCustom(($event.target as HTMLInputElement).value)" @change="apply('custom', ($event.target as HTMLInputElement).value)">
        </label>
      </div>
      <div v-if="custom" class="oa-field oa-custom-hex"><label>{{ t('customColour') }}<input v-model="hex" type="text" maxlength="7" @change="apply('custom', hex)"></label></div>
      <p class="oa-field-hint">{{ t('controlColoursHint') }}</p>
    </section>
    <section v-if="!theme.modeLocked()" class="oa-appearance-section">
      <div class="oa-appearance-heading"><h3>{{ t('themeStyle') }}</h3></div>
      <div class="oa-theme-choices">
        <button v-for="entry in modes" :key="entry.value" type="button" class="oa-theme-choice"
          :class="{ active: theme.mode() === entry.value }" :aria-pressed="theme.mode() === entry.value" @click="onTheme(entry.value)">
          <component :is="entry.icon" :size="18" /><span>{{ t(entry.label) }}</span>
        </button>
      </div>
    </section>
    <OaSelectField :model-value="currentLanguage()" :label="t('languageLabel')" :hint="t('languageHint')"
      :options="[{ value: 'en', label: 'English' }, { value: 'zh', label: '中文' }]" @update:model-value="onLanguage" />
    <p v-if="languageFlash" class="oa-field-hint" role="alert">{{ languageFlash }}</p>
  </div>
</template>
