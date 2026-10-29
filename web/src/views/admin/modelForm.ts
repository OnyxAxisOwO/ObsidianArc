// The model editor's form, and the two directions between it and a stored
// model. Kept apart from the page so the mapping can be read in one place:
// a field added to one direction and forgotten in the other is the bug this
// shape invites.

import type { AdminModel, AdminModelInput, Group, ReasoningStyle, ReasoningTier } from '@/admin/api';

export interface ModelForm {
  providerID: string;
  modelID: string;
  apiName: string;
  systemPrompt: string;
  displayName: string;
  description: string;
  /** `mark:<id>`, `mark:none`, an inline image, a same-site path, or empty for a guess. */
  avatar: string;
  enabled: boolean;
  hidden: boolean;
  sortOrder: number | null;
  groupGrants: Record<string, 'use' | 'view'>;
  reasoning: boolean;
  images: boolean;
  vision: boolean;
  streaming: boolean;
  systemPromptSupported: boolean;
  tools: boolean;
  imageGen: boolean;
  chatImageGen: boolean;
  emulateTools: boolean;
  contextWindow: number | null;
  maxOutput: number | null;
  routeTo: string;
  reasoningStyle: ReasoningStyle | '';
  tiers: ReasoningTier[];
  requestOverride: string;
  requestWeight: number | null;
  inputWeight: number | null;
  outputWeight: number | null;
  reasoningWeight: number | null;
}

/** What the page holds before any panel has been opened. */
export function blankModelForm(): ModelForm {
  return formFromModel(null, null, [], '');
}

/**
 * The form for `row`, or for a new model starting from `from` (a duplicate)
 * or from nothing. `row` alone decides the API name: a copy drops it because
 * it is unique across the instance.
 *
 * A brand-new model is usable by every group, because a model nobody can
 * reach is never what adding one meant; a copy keeps the grants it copied.
 */
export function formFromModel(
  row: AdminModel | null,
  from: AdminModel | null,
  groups: Pick<Group, 'id'>[],
  defaultProviderID: string,
): ModelForm {
  const source = row ?? from;

  const grants: Record<string, 'use' | 'view'> = {};
  if (source?.group_grants) {
    for (const grant of source.group_grants) {
      if (grant.access === 'use' || grant.access === 'view') grants[grant.group_id] = grant.access;
    }
  } else if (!source) {
    for (const g of groups) {
      grants[g.id] = 'use';
    }
  }

  return {
    providerID: source?.provider_id ?? defaultProviderID,
    modelID: source?.model_id ?? '',
    apiName: row?.api_name ?? '',
    systemPrompt: source?.system_prompt ?? '',
    displayName: source?.display_name ?? '',
    description: source?.description ?? '',
    avatar: source?.avatar ?? '',
    enabled: source?.enabled ?? true,
    hidden: source?.hidden ?? false,
    sortOrder: source?.sort_order ?? 0,
    groupGrants: grants,
    reasoning: source?.supports_reasoning ?? false,
    images: source?.supports_images ?? false,
    vision: source?.supports_vision ?? false,
    streaming: source?.supports_streaming ?? true,
    systemPromptSupported: source?.supports_system_prompt ?? true,
    tools: source?.supports_tools ?? false,
    imageGen: source?.supports_image_gen ?? false,
    chatImageGen: source?.supports_chat_image_gen ?? false,
    emulateTools: source?.emulate_tools ?? false,
    contextWindow: source?.context_window ?? null,
    maxOutput: source?.max_output_tokens ?? null,
    routeTo: source?.route_to_id ?? '',
    reasoningStyle: source?.reasoning_style ?? '',
    tiers: source?.reasoning_tiers ?? [],
    requestOverride: source?.request_override ?? '',
    requestWeight: source?.request_weight ?? 0,
    inputWeight: source?.input_token_weight ?? 1,
    outputWeight: source?.output_token_weight ?? 1,
    reasoningWeight: source?.reasoning_token_weight ?? 1,
  };
}

/** Whether a typed override is the JSON object the server merges into a request. */
export function isOverrideObject(text: string): boolean {
  try {
    const parsed: unknown = JSON.parse(text);
    return typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed);
  } catch {
    return false;
  }
}

/**
 * The body a save sends, from the form. A blank override is sent as `{}`
 * rather than omitted, so clearing the field clears the stored one. The
 * provider is the caller's to add: only a new model names its provider.
 */
export function modelPayload(form: ModelForm, trimmedOverride: string): AdminModelInput {
  return {
    route_to_id: form.routeTo,
    reasoning_style: form.reasoningStyle,
    reasoning_tiers: form.tiers,
    request_override: trimmedOverride || '{}',
    model_id: form.modelID.trim(),
    api_name: form.apiName.trim(),
    system_prompt: form.systemPrompt.trim(),
    display_name: form.displayName.trim(),
    description: form.description.trim(),
    avatar: form.avatar.trim(),
    enabled: form.enabled,
    hidden: form.hidden,
    sort_order: form.sortOrder ?? 0,
    supports_reasoning: form.reasoning,
    supports_images: form.images,
    supports_vision: form.vision,
    supports_streaming: form.streaming,
    supports_system_prompt: form.systemPromptSupported,
    supports_tools: form.tools,
    supports_image_gen: form.imageGen,
    supports_chat_image_gen: form.chatImageGen,
    emulate_tools: form.emulateTools,
    context_window: form.contextWindow ?? 0,
    max_output_tokens: form.maxOutput ?? 0,
    request_weight: form.requestWeight ?? 0,
    input_token_weight: form.inputWeight ?? 1,
    output_token_weight: form.outputWeight ?? 1,
    reasoning_token_weight: form.reasoningWeight ?? 1,
    group_grants: Object.entries(form.groupGrants).map(([group_id, access]) => ({ group_id, access })),
  };
}
