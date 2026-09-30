<script setup lang="ts">
// A titled group of rows in one card: the shape the security, invites and
// usage screens already use, made a component so the screens that had fields
// lying loose on the panel can have it too.
//
// The title and its explanation sit outside, above the card, the way the
// security screen's headings do; everything inside is a row (OaRow) or a
// plain paragraph, divided from the next by a hairline. The card takes the
// card token, which stands clear of the panel it sits on — the panel is the
// container tone, and a section of fields drawn straight onto it reads as
// text lying loose.

import type { Component } from 'vue';

defineProps<{
  title?: string | undefined;
  hint?: string | undefined;
  /** A small mark before the title, for a screen with many groups to tell apart. */
  icon?: Component | undefined;
}>();
</script>

<template>
  <section class="oa-group">
    <header v-if="title || hint || $slots['actions']" class="oa-group-head">
      <div class="oa-group-head-text">
        <h2 v-if="title" class="oa-group-title">
          <component :is="icon" v-if="icon" :size="14" />{{ title }}
        </h2>
        <p v-if="hint" class="oa-group-hint">{{ hint }}</p>
      </div>
      <slot name="actions" />
    </header>
    <div class="oa-group-card"><slot /></div>
  </section>
</template>
