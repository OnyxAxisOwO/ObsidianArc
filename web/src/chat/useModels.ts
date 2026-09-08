// What answers, and how hard it thinks.
//
// One store rather than two, because it is one decision. It used to be a chip
// in the header that chose a model and a section inside the composer's `+`
// menu that set the reasoning effort — always the same choice, "which model,
// and how much of it", split across two places that could disagree on screen.

import { computed, ref } from 'vue';
import { api } from '@/api/client';
import type { StringKey } from '@/i18n';
import { t } from '@/composables/useI18n';
import { currentPreferences, syncPreferences } from '@/stores/session';

export interface ModelCapabilities {
  supports_reasoning: boolean;
  supports_images: boolean;
  supports_vision: boolean;
  supports_streaming: boolean;
  supports_system_prompt: boolean;
  supports_tools: boolean;
  supports_image_gen: boolean;
  context_window: number;
  max_output_tokens: number;
}

/** One amount of thinking this model offers, named by an administrator. */
export interface ReasoningTier {
  id: string;
  name: string;
}

export interface AvailableModel extends ModelCapabilities {
  id: string;
  display_name: string;
  description: string;
  avatar: string;
  usable?: boolean;
  /** Absent for a model using the built-in three. */
  reasoning_tiers?: ReasoningTier[];
  /**
   * The share of recent requests this model answered, 0 to 1. Absent unless
   * the operator publishes it — most instances do not.
   */
  uptime?: number;
  /**
   * Set when it has been failing often enough to be worth saying so before
   * somebody types a long question into it. Independent of `uptime`: an
   * instance can warn without publishing a figure.
   */
  unstable?: boolean;
}

/**
 * Which amount of thinking, by the id of the tier that names it.
 *
 * A plain string rather than the three words it used to be: a model may
 * define its own tiers, so what is valid here is decided per model — by the
 * list the reader was offered, which the gateway checks the value against.
 */
export type Effort = string;

export interface ReasoningState {
  enabled: boolean;
  effort: Effort;
}

/**
 * One position on the slider.
 *
 * The label is a finished string rather than a dictionary key, because a
 * model may carry tiers of its own and those are named by an administrator —
 * in whatever language this instance is run in, so they never pass through
 * `t()`. The built-in three still do.
 */
export interface Stop {
  enabled: boolean;
  effort: Effort;
  label: string;
}

/** What a model with no tiers of its own offers, and what it always did. */
const BUILT_IN: Array<{ effort: Effort; label: StringKey }> = [
  { effort: 'low', label: 'effortLow' },
  { effort: 'medium', label: 'effortMedium' },
  { effort: 'high', label: 'effortHigh' },
];

/**
 * The slider's stops for one model.
 *
 * Off is a position on the same track rather than a switch beside it: the
 * question is how much thinking, and none is an amount.
 */
export function stopsFor(model: AvailableModel | null): Stop[] {
  const tiers: Stop[] = model?.reasoning_tiers?.length
    ? model.reasoning_tiers.map((tier) => ({ enabled: true, effort: tier.id, label: tier.name }))
    : BUILT_IN.map((stop) => ({ enabled: true, effort: stop.effort, label: t(stop.label) }));
  // Off carries the middle tier's value, so sliding away and back lands where
  // it used to rather than on a value this model never offered.
  const middle = tiers[Math.floor(tiers.length / 2)]!;
  return [{ enabled: false, effort: middle.effort, label: t('effortOff') }, ...tiers];
}

export function stopFor(state: ReasoningState, stops: Stop[]): number {
  if (!state.enabled) return 0;
  const found = stops.findIndex((stop) => stop.enabled && stop.effort === state.effort);
  // An effort this model does not offer — a preference remembered from
  // another model, or from before its tiers were edited — reads as the middle
  // one, which is the same tier the server resolves it to.
  return found === -1 ? 1 + Math.floor((stops.length - 1) / 2) : found;
}

export const models = ref<AvailableModel[]>([]);
export const selectedID = ref('');
export const reasoning = ref<ReasoningState>({ enabled: false, effort: 'medium' });

export const currentModel = computed<AvailableModel | null>(
  () => models.value.find((model) => model.id === selectedID.value) ?? null,
);

/** Reads the account's remembered choices. Called once, before the fetch. */
export function restorePreferences(): void {
  const preferences = currentPreferences.value;
  const stored = preferences['default_model_id'];
  if (typeof stored === 'string') selectedID.value = stored;

  const effort = preferences['reasoning_effort'];
  reasoning.value = {
    enabled: preferences['reasoning_enabled'] === true,
    // Any remembered tier, not just the built-in three: a model may name its
    // own, and the picker resolves an id the current model does not offer
    // back to its middle tier anyway.
    effort: typeof effort === 'string' && effort !== '' ? effort : 'medium',
  };
}

export function selectModel(modelID: string): void {
  const model = models.value.find((entry) => entry.id === modelID);
  if (!model || model.usable === false) return;
  selectedID.value = modelID;
  syncPreferences({ default_model_id: modelID });
}

export function setReasoning(next: ReasoningState): void {
  reasoning.value = next;
  syncPreferences({ reasoning_enabled: next.enabled, reasoning_effort: next.effort });
}

export async function loadModels(): Promise<void> {
  const result = await api.get<{ models: AvailableModel[] }>('/api/models');
  models.value = result.models;

  const usable = result.models.filter((model) => model.usable !== false);
  // A remembered model that is gone, or that this account may see but not
  // use, falls back rather than leaving the composer pointing at nothing.
  if (!usable.some((model) => model.id === selectedID.value)) {
    selectedID.value = usable[0]?.id ?? '';
  }
}
