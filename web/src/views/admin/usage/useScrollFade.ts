// The fade at the edges of a list that scrolls inside a fixed frame.
//
// The edge row is cut by half a row on purpose, and fades out, so a list that
// scrolls looks like one before anyone has touched it — a thin overlay
// scrollbar only shows once it is moving. Each fade goes when that end is
// reached, because there is nothing past it for the fade to point at.

import { computed, ref, type Ref } from 'vue';

export function useScrollFade(overflows: Ref<boolean>) {
  const atStart = ref(true);
  const atEnd = ref(false);

  function onScroll(event: Event): void {
    const element = event.target as HTMLElement;
    atStart.value = element.scrollTop <= 2;
    atEnd.value = element.scrollTop + element.clientHeight >= element.scrollHeight - 2;
  }
  /** For when the rows underneath were replaced and the position no longer means anything. */
  function reset(): void {
    atStart.value = true;
    atEnd.value = false;
  }
  const scrollClass = computed(() => {
    if (!overflows.value) return 'oa-board-scroll';
    return `oa-board-scroll${atStart.value ? '' : ' fade-top'}${atEnd.value ? '' : ' fade-bottom'}`;
  });

  return { atEnd, onScroll, reset, scrollClass };
}
