// The account fields plugins add, as the forms need them.
//
// Which fields exist and whether sign-up asks for them is the server's to
// say — the "fields" block of /api/site, keyed by column. How each one looks
// is its plugin's (plugins/registry.ts). A key the server names but no
// loaded plugin describes is skipped rather than drawn as a bare input: a
// label nobody wrote is worse than no field, and the server still decides.

import type { FieldRule, SiteInfo } from '@/api/auth';
import { fieldSpec } from '@/plugins/registry';

export interface FieldPlan {
  keys: string[];
  required: string[];
}

/** The fields a sign-up form asks for: every one whose rule is not off. */
export function signupFields(site: SiteInfo): FieldPlan {
  return plan(site, (rule) => rule !== 'off');
}

/**
 * The fields an account's own screen, or the backoffice's, edits: all of
 * them, whatever sign-up does — an account may carry a value the form no
 * longer asks for, and its owner may still want to change it.
 */
export function allFields(site: SiteInfo): FieldPlan {
  return plan(site, () => true);
}

function plan(site: SiteInfo, include: (rule: FieldRule) => boolean): FieldPlan {
  const keys: string[] = [];
  const required: string[] = [];
  for (const [key, rule] of Object.entries(site.fields ?? {})) {
    if (!fieldSpec(key) || !include(rule)) continue;
    keys.push(key);
    if (rule === 'required') required.push(key);
  }
  return { keys, required };
}

/**
 * The first reason these values would be refused, in the reader's language,
 * or null. Checked before submitting only so the answer is immediate.
 */
export function fieldProblem(values: Record<string, string>, plan: FieldPlan): string | null {
  for (const key of plan.keys) {
    const spec = fieldSpec(key);
    if (!spec) continue;
    const value = (values[key] ?? '').trim();
    if (!value) {
      if (plan.required.includes(key)) return spec.required();
      continue;
    }
    if (spec.pattern && !spec.pattern.test(value)) return spec.invalid();
  }
  return null;
}

/** The values trimmed, for the request body, limited to the planned keys. */
export function fieldValues(values: Record<string, string>, plan: FieldPlan): Record<string, string> {
  const out: Record<string, string> = {};
  for (const key of plan.keys) out[key] = (values[key] ?? '').trim();
  return out;
}
