<script setup lang="ts">
// A section of a panel whose heading folds its contents away, and is remembered.
//
// The usage screen has six sections and a list of turns under them, and what
// somebody comes to it for is usually one of them. Folding is per section and
// kept per browser: a reader who never wants the totals folds them once.
//
// The heading is a button of its own — chevron and title — and the section's
// other controls (a refresh, a "+", the day's check-in) sit beside it in the same
// row rather than inside it, so pressing one of those folds nothing, and what
// folds is a real button rather than a row pretending to be one. What is folded
// away is inert, not just out of sight: nothing in it takes focus or is read out.
// The height animates the way the uptime accordion's does, as a grid row going
// from 0fr to 1fr, so the content is never measured.
//
// It starts open unless the caller says otherwise and the reader has not left it
// folded: what is shown is the default, so a section nobody has met yet is never
// hidden from them.

import { ref, useId } from 'vue';
import { IconChevron } from '@/icons';

const props = withDefaults(defineProps<{
  /** Names the section in storage: one section, one key. */
  id: string;
  title: string;
  /** How it starts when nothing is remembered. */
  open?: boolean;
}>(), { open: true });

const STORAGE_PREFIX = 'obsidian-arc-fold-';
const key = STORAGE_PREFIX + props.id;
const bodyId = useId();

function read(): boolean {
  try {
    const remembered = localStorage.getItem(key);
    if (remembered === '1') return false;
    if (remembered === '0') return true;
  } catch {
    // Storage disabled: it starts the way the caller said, which is open for
    // everything here — the state that shows the reader all of it.
  }
  return props.open;
}

const expanded = ref(read());

function toggle(): void {
  expanded.value = !expanded.value;
  try {
    localStorage.setItem(key, expanded.value ? '0' : '1');
  } catch {
    // Best effort; it still folded.
  }
}

/**
 * Opens it for now, without changing what is remembered. For a control beside
 * the heading whose result lives inside the section — the "+" that opens the
 * code field, an error the section has to show — and which should not undo a
 * reader's choice to keep it folded.
 */
function reveal(): void {
  expanded.value = true;
}

defineExpose({ toggle, reveal, expanded });
</script>

<template>
  <section class="oa-collapsible" :class="{ open: expanded }">
    <div class="oa-collapsible-head">
      <h3 class="oa-collapsible-title">
        <button
          type="button"
          class="oa-collapsible-toggle"
          :aria-expanded="expanded"
          :aria-controls="bodyId"
          @click="toggle"
        >
          <span class="oa-collapsible-chevron" aria-hidden="true"><IconChevron :size="14" /></span>
          <span class="oa-drawer-subhead">{{ props.title }}</span>
        </button>
      </h3>
      <slot name="meta" />
      <span class="oa-header-spacer" />
      <slot name="actions" />
    </div>
    <!-- undefined rather than false: where the element has no `inert` property
         the binding falls back to an attribute, and a boolean attribute that is
         present at all — even as "false" — is on. -->
    <div :id="bodyId" class="oa-collapsible-body" :inert="expanded ? undefined : true">
      <div class="oa-collapsible-inner">
        <div class="oa-collapsible-content"><slot /></div>
      </div>
    </div>
  </section>
</template>
