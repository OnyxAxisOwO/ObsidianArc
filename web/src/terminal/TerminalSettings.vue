<script setup lang="ts">
// What the terminal looks like. The body of the appearance column.
//
// Every field here reports two moments, mirroring `OaRangeField`'s own
// `update:modelValue` / `commit` split: `preview` repaints the open tabs
// immediately (through the CSS custom properties `TerminalPanel.vue` sets),
// and `commit` is the one that reaches `localStorage` — so dragging the font
// size does not write on every pixel, but every other field, which has no
// drag phase, commits the moment it changes.
//
// There is no transparency control here and there should not be one: the
// terminal paints no surface of its own, so its translucency and blur are
// the wallpaper settings', like every other card in the backoffice.

import { computed, ref } from 'vue';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaRangeField from '@/components/OaRangeField.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTextField from '@/components/OaTextField.vue';
import type { Choice } from '@/components/choice';
import { t } from '@/composables/useI18n';
import {
  DEFAULT_TERMINAL_FONT_FAMILY,
  FONT_FAMILY_MAX_LENGTH,
  TERMINAL_CURSOR_STYLES,
  TERMINAL_FONT_FAMILY_PRESETS,
  TERMINAL_PREFS_DEFAULTS,
  TERMINAL_SCROLLBACK_SIZES,
  type TerminalCursorStyle,
  type TerminalPrefs,
  type TerminalScrollbackSize,
} from './prefs';

const props = defineProps<{ modelValue: TerminalPrefs }>();

const emit = defineEmits<{
  /** Applied to the CSS custom properties straight away; never persisted by itself. */
  (event: 'update:modelValue', value: TerminalPrefs): void;
  /** The value to write to `localStorage`. Always paired with an `update:modelValue` carrying the same value. */
  (event: 'commit', value: TerminalPrefs): void;
}>();

/** A field with no drag phase: previewing and committing are the same moment. */
function set(patch: Partial<TerminalPrefs>): void {
  const next = { ...props.modelValue, ...patch };
  emit('update:modelValue', next);
  emit('commit', next);
}

function preview(patch: Partial<TerminalPrefs>): void {
  emit('update:modelValue', { ...props.modelValue, ...patch });
}

function commit(patch: Partial<TerminalPrefs>): void {
  emit('commit', { ...props.modelValue, ...patch });
}

const CUSTOM_FONT = '__custom__';

// Whether the custom field is open is a mode the user chooses, not a fact
// about the stack: picking "Custom…" with a preset active has to open the
// field even though the stack is still a preset. So it is held here, set once
// from the stored value, and the stack alone never decides it.
const customMode = ref(!TERMINAL_FONT_FAMILY_PRESETS.includes(props.modelValue.fontFamily));
// What the custom field shows while it is typed. It may be blank, and a blank
// value is never committed, so the field keeps what the user typed instead of
// being refilled with the stored stack while they retype it.
const fontDraft = ref(props.modelValue.fontFamily);
const fontFamilySelectValue = computed(() => (customMode.value ? CUSTOM_FONT : props.modelValue.fontFamily));

function presetLabel(stack: string): string {
  if (stack === DEFAULT_TERMINAL_FONT_FAMILY) return t('terminalFontDefault');
  // Font names are proper nouns, like `Base URL` elsewhere — not run through
  // t(). The first family in the stack is the one a reader recognises.
  return stack.split(',')[0]?.replace(/["']/g, '').trim() ?? stack;
}

const fontFamilyOptions = computed<Choice[]>(() => [
  ...TERMINAL_FONT_FAMILY_PRESETS.map((preset) => ({ value: preset, label: presetLabel(preset) })),
  { value: CUSTOM_FONT, label: t('terminalFontCustom') },
]);

const cursorStyleOptions = computed<Choice<TerminalCursorStyle>[]>(() =>
  TERMINAL_CURSOR_STYLES.map((style) => ({
    value: style,
    label: style === 'block' ? t('terminalCursorBlock')
      : style === 'bar' ? t('terminalCursorBar')
        : t('terminalCursorUnderline'),
  })));

const scrollbackOptions = computed<Choice[]>(() =>
  TERMINAL_SCROLLBACK_SIZES.map((size) => ({
    value: String(size),
    label: t('terminalScrollbackLines', { n: size }),
  })));

function onFontFamilyChange(value: string): void {
  if (value === CUSTOM_FONT) {
    // Opens the field on the stack in use, so choosing it never changes the
    // font by itself and never leaves the field blank.
    fontDraft.value = props.modelValue.fontFamily;
    customMode.value = true;
    return;
  }
  customMode.value = false;
  set({ fontFamily: value });
}

function onCustomFontInput(value: string): void {
  fontDraft.value = value;
  // Only a usable stack is committed, so the stored font is always one the
  // terminal can render; a blank draft waits for the next non-blank keystroke.
  if (value.trim()) set({ fontFamily: value });
}

function onCursorStyleChange(value: TerminalCursorStyle): void {
  set({ cursorStyle: value });
}

function onScrollbackChange(value: string): void {
  set({ scrollback: Number(value) as TerminalScrollbackSize });
}

function resetDefaults(): void {
  const next = { ...TERMINAL_PREFS_DEFAULTS };
  customMode.value = false;
  emit('update:modelValue', next);
  emit('commit', next);
}
</script>

<template>
  <div class="oa-terminal-settings">
    <p class="oa-terminal-settings-hint">{{ t('terminalShortcutsHint') }}</p>

    <OaSelectField
      :model-value="fontFamilySelectValue"
      :label="t('terminalFontFamily')"
      :options="fontFamilyOptions"
      @update:model-value="onFontFamilyChange"
    />
    <OaTextField
      v-if="customMode"
      :model-value="fontDraft"
      :label="t('terminalCustomFontFamily')"
      :hint="t('terminalCustomFontHint')"
      :max-length="FONT_FAMILY_MAX_LENGTH"
      monospace
      @update:model-value="onCustomFontInput"
    />

    <OaRangeField
      :model-value="props.modelValue.fontSize"
      :label="t('terminalFontSize')"
      :min="11" :max="20" :step="1"
      :format="(value) => `${value}px`"
      @update:model-value="(value) => preview({ fontSize: value })"
      @commit="(value) => commit({ fontSize: value })"
    />
    <OaRangeField
      :model-value="props.modelValue.lineHeight"
      :label="t('terminalLineHeight')"
      :min="1.2" :max="2" :step="0.1"
      :format="(value) => value.toFixed(1)"
      @update:model-value="(value) => preview({ lineHeight: value })"
      @commit="(value) => commit({ lineHeight: value })"
    />
    <OaRangeField
      :model-value="props.modelValue.letterSpacing"
      :label="t('terminalLetterSpacing')"
      :min="0" :max="2" :step="0.5"
      :format="(value) => `${value}px`"
      @update:model-value="(value) => preview({ letterSpacing: value })"
      @commit="(value) => commit({ letterSpacing: value })"
    />

    <OaSelectField
      :model-value="props.modelValue.cursorStyle"
      :label="t('terminalCursorStyle')"
      :options="cursorStyleOptions"
      :searchable="false"
      @update:model-value="onCursorStyleChange"
    />

    <OaSelectField
      :model-value="String(props.modelValue.scrollback)"
      :label="t('terminalScrollback')"
      :options="scrollbackOptions"
      :searchable="false"
      @update:model-value="onScrollbackChange"
    />

    <OaSwitchField
      :model-value="props.modelValue.timestamps"
      :label="t('terminalTimestamps')"
      @update:model-value="(value) => set({ timestamps: value })"
    />
    <OaSwitchField
      :model-value="props.modelValue.ligatures"
      :label="t('terminalLigatures')"
      @update:model-value="(value) => set({ ligatures: value })"
    />

    <p class="oa-terminal-settings-hint">{{ t('terminalOpacityHint') }}</p>

    <div class="oa-terminal-settings-foot">
      <OaConfirmButton
        class="oa-btn"
        :label="t('terminalReset')"
        :armed-label="t('terminalResetConfirm')"
        :armed-title="t('terminalResetConfirmTitle')"
        :resting-title="t('terminalReset')"
        @confirm="resetDefaults"
      />
    </div>
  </div>
</template>
