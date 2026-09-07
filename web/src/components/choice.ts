// One option in a list.
//
// Its own module rather than a type exported from OaSelect.vue, because a
// `<script setup>` block cannot export anything — and every filter bar, form
// field and picker in the interface needs to name this shape.

export interface Choice<V extends string = string> {
  value: V;
  label: string;
}
