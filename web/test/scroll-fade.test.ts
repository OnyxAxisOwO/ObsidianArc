import { describe, expect, it } from 'vitest';
import { ref } from 'vue';
import { useScrollFade } from '../src/views/admin/usage/useScrollFade';

// The handler reads three numbers off the element the browser laid out, so a
// plain object with the same three is all it needs. The frame is 100px tall
// over 400px of rows: the top edge is at 0 and the bottom edge at 300.
function scrolledTo(scrollTop: number): Event {
  return { target: { scrollTop, clientHeight: 100, scrollHeight: 400 } } as unknown as Event;
}

describe('useScrollFade', () => {
  it('fades only the far edge at first, and nothing when the list does not overflow', () => {
    const overflows = ref(true);
    const fade = useScrollFade(overflows);

    expect(fade.scrollClass.value).toBe('oa-board-scroll fade-bottom');

    overflows.value = false;
    expect(fade.scrollClass.value).toBe('oa-board-scroll');
  });

  it('fades both edges while the list is in the middle', () => {
    const fade = useScrollFade(ref(true));

    fade.onScroll(scrolledTo(150));

    expect(fade.scrollClass.value).toBe('oa-board-scroll fade-top fade-bottom');
  });

  it('drops each fade once its edge is reached', () => {
    const fade = useScrollFade(ref(true));

    fade.onScroll(scrolledTo(300));
    expect(fade.scrollClass.value).toBe('oa-board-scroll fade-top');

    fade.onScroll(scrolledTo(0));
    expect(fade.scrollClass.value).toBe('oa-board-scroll fade-bottom');
  });

  it('counts two pixels short of an edge as being at it', () => {
    const fade = useScrollFade(ref(true));

    fade.onScroll(scrolledTo(2));
    expect(fade.scrollClass.value).toBe('oa-board-scroll fade-bottom');

    fade.onScroll(scrolledTo(298));
    expect(fade.scrollClass.value).toBe('oa-board-scroll fade-top');
  });

  it('forgets the position when the rows underneath are replaced', () => {
    const fade = useScrollFade(ref(true));
    fade.onScroll(scrolledTo(300));

    fade.reset();

    expect(fade.atEnd.value).toBe(false);
    expect(fade.scrollClass.value).toBe('oa-board-scroll fade-bottom');
  });
});
