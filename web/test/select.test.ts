import { describe, it, expect, beforeEach, vi } from 'vitest';
import { select, placeList } from '../src/ui/select';

// jsdom has no layout, so neither of these exists. The control calls both
// while opening; what is under test is everything around them.
beforeEach(() => {
  Element.prototype.scrollIntoView = () => {};
  // The open list is module state, so a test that leaves one open would be
  // read by the next one as a list it opened itself.
  document.dispatchEvent(new PointerEvent('pointerdown'));
  document.body.textContent = '';
});

const COLOURS = [
  { value: 'red', label: 'Red' },
  { value: 'green', label: 'Green' },
  { value: 'blue', label: 'Blue' },
];

function mount(config: Parameters<typeof select>[0] = { choices: COLOURS }) {
  const control = select(config);
  document.body.appendChild(control.element);
  return control;
}

function list(): HTMLElement | null {
  return document.querySelector('.oa-select-menu');
}

function key(node: HTMLElement, name: string): void {
  node.dispatchEvent(new KeyboardEvent('keydown', { key: name, bubbles: true, cancelable: true }));
}

describe('select', () => {
  it('names its value, and the first choice when it was given none', () => {
    expect(mount({ choices: COLOURS, value: 'green' }).element.textContent).toBe('Green');
    expect(mount({ choices: COLOURS }).element.textContent).toBe('Red');
  });

  // The reason this control exists rather than a styled <select>: every place
  // one appears is inside something that scrolls or carries a filter, and a
  // popup left in that subtree is clipped by it. It has to be a child of
  // <body> or it is the same bug in a different shape.
  it('puts the list on the body while open and takes it away after', async () => {
    const control = mount();
    expect(list()).toBeNull();

    control.element.click();
    expect(list()).not.toBeNull();
    expect(list()!.parentElement).toBe(document.body);
    expect(control.element.getAttribute('aria-expanded')).toBe('true');

    key(control.element, 'Escape');
    expect(control.element.getAttribute('aria-expanded')).toBe('false');
    // The node outlives the click by the length of its transition.
    await new Promise((resolve) => setTimeout(resolve, 250));
    expect(list()).toBeNull();
  });

  it('walks with the arrows and commits on Enter', () => {
    const changes: string[] = [];
    const control = mount({ choices: COLOURS, onChange: (value) => changes.push(value) });

    control.element.click();
    key(control.element, 'ArrowDown');
    key(control.element, 'ArrowDown');
    key(control.element, 'Enter');

    expect(control.value()).toBe('blue');
    expect(control.element.textContent).toBe('Blue');
    expect(changes).toEqual(['blue']);
    expect(control.element.getAttribute('aria-expanded')).toBe('false');
  });

  it('leaves the value alone when the list is dismissed', () => {
    const changes: string[] = [];
    const control = mount({ choices: COLOURS, value: 'green', onChange: (v) => changes.push(v) });

    control.element.click();
    key(control.element, 'ArrowDown');
    key(control.element, 'Escape');

    expect(control.value()).toBe('green');
    expect(changes).toEqual([]);
  });

  it('jumps to a choice by its first letters', () => {
    const control = mount();
    control.element.click();
    key(control.element, 'b');
    key(control.element, 'Enter');
    expect(control.value()).toBe('blue');
  });

  // set() is how a screen restores a stored preference. It must not look like
  // somebody choosing, or restoring would write the value back to the server.
  it('does not report a value it was told to show', () => {
    const changes: string[] = [];
    const control = mount({ choices: COLOURS, onChange: (v) => changes.push(v) });
    control.set('blue');
    expect(control.value()).toBe('blue');
    expect(control.element.textContent).toBe('Blue');
    expect(changes).toEqual([]);
  });

  it('keeps a value that survives a new list and drops one that does not', () => {
    const control = mount({ choices: COLOURS, value: 'green' });

    control.setChoices([{ value: 'green', label: 'Green' }, { value: 'grey', label: 'Grey' }]);
    expect(control.value()).toBe('green');

    control.setChoices([{ value: 'black', label: 'Black' }]);
    expect(control.value()).toBe('black');
    expect(control.element.textContent).toBe('Black');
  });

  it('marks the current value in the list', () => {
    const control = mount({ choices: COLOURS, value: 'green' });
    control.element.click();
    const selected = Array.from(list()!.children)
      .filter((row) => row.getAttribute('aria-selected') === 'true')
      .map((row) => row.textContent);
    expect(selected).toEqual(['Green']);
  });

  // Two lists over each other is the bug that a shared open-list reference
  // exists to prevent.
  it('closes the list that was open when another opens', async () => {
    const first = mount();
    const second = mount();

    first.element.click();
    second.element.click();

    // Immediately: one is shut, though its node is still running its
    // transition out and is inert until it goes.
    expect(first.element.getAttribute('aria-expanded')).toBe('false');
    expect(second.element.getAttribute('aria-expanded')).toBe('true');

    await new Promise((resolve) => setTimeout(resolve, 250));
    expect(document.querySelectorAll('.oa-select-menu').length).toBe(1);
  });

  // AGENTS.md's worked example: a modal whose node was removed while its
  // document key handler survived, so Escape kept firing from unrelated
  // screens. Every listener this control adds while open comes off again.
  it('leaves no listener behind on the document or the window', () => {
    const added = new Map<string, number>();
    const bump = (map: Map<string, number>, type: string, by: number) =>
      map.set(type, (map.get(type) ?? 0) + by);

    for (const target of [document, window] as const) {
      const add = target.addEventListener.bind(target);
      const remove = target.removeEventListener.bind(target);
      vi.spyOn(target, 'addEventListener').mockImplementation((type, fn, opts) => {
        bump(added, type, 1);
        add(type, fn as EventListener, opts as AddEventListenerOptions);
      });
      vi.spyOn(target, 'removeEventListener').mockImplementation((type, fn, opts) => {
        bump(added, type, -1);
        remove(type, fn as EventListener, opts as AddEventListenerOptions);
      });
    }

    const control = mount();
    control.element.click();
    expect([...added.values()].some((count) => count > 0)).toBe(true);

    key(control.element, 'Escape');
    for (const [type, count] of added) {
      expect(`${type}:${count}`).toBe(`${type}:0`);
    }
    vi.restoreAllMocks();
  });

  // Reproduced against the shipped file: open, walk down, replace the list
  // with a shorter one, press Enter. It threw on choices[active].value, and
  // the throw left aria-expanded true with the document listener still on.
  it('survives Enter after the list shrank under the walk', () => {
    const control = mount();
    control.element.click();
    key(control.element, 'ArrowDown');
    key(control.element, 'ArrowDown');

    control.setChoices([{ value: 'red', label: 'Red' }]);
    expect(() => key(control.element, 'Enter')).not.toThrow();
    expect(control.element.getAttribute('aria-expanded')).toBe('false');
  });

  // A rebuild while open leaves a list with new ids and a possibly shorter
  // walk. Nothing highlighted, or a highlight naming a row that is gone, is
  // how the arrow keys resume from an index that means nothing.
  it('re-marks the value after the list is replaced under it', () => {
    const control = mount({ choices: COLOURS, value: 'blue' });
    control.element.click();

    control.setChoices([...COLOURS, { value: 'white', label: 'White' }]);
    const marked = Array.from(list()!.children).filter((row) => row.classList.contains('active'));
    expect(marked.length).toBe(1);
    expect(marked[0]!.id).toBe(control.element.getAttribute('aria-activedescendant'));
    expect(marked[0]!.textContent).toBe('Blue');
  });

  // Four teardown paths in this app destroy a screen without a router render
  // — ui/panel.ts empties its body, the admin rail swaps the whole body node.
  // The list is on <body> and hears about none of them, so it watches.
  it('closes itself when its trigger is taken out of the document', async () => {
    const control = mount();
    control.element.click();
    expect(control.element.getAttribute('aria-expanded')).toBe('true');

    control.element.remove();
    await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    expect(control.element.getAttribute('aria-expanded')).toBe('false');

    await new Promise((resolve) => setTimeout(resolve, 250));
    expect(list()).toBeNull();
  });

  // A press outside is a dismissal, but the <label> that wraps the control
  // forwards a click on its own text to it — reading that as outside would
  // shut the list in the same gesture that opened it.
  it('treats the label around it as part of the control', () => {
    const control = select({ choices: COLOURS });
    const label = document.createElement('label');
    const text = document.createElement('span');
    label.appendChild(text);
    label.appendChild(control.element);
    document.body.appendChild(label);

    control.element.click();
    text.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
    expect(control.element.getAttribute('aria-expanded')).toBe('true');

    document.body.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
    expect(control.element.getAttribute('aria-expanded')).toBe('false');
  });
});

describe('placeList', () => {
  const box = { top: 250, bottom: 282, left: 100, width: 400 };
  const view = { width: 1000, height: 560 };

  // The bug this function was extracted to make testable: a 320px list under
  // a trigger 282px down a 560px window used to be placed below anyway,
  // because the flip only asked which side was bigger and never asked whether
  // either side fitted. 48px of rows ended up past the bottom edge of a node
  // that scrolls with nothing.
  it('never reaches past the bottom of the window', () => {
    const spot = placeList(box, 320, 400, view);
    expect(spot.top + spot.maxHeight).toBeLessThanOrEqual(view.height);
    expect(spot.maxHeight).toBeLessThan(320);
  });

  it('hangs below when the list fits there', () => {
    const spot = placeList(box, 120, 400, view);
    expect(spot.top).toBe(box.bottom + 6);
    expect(spot.origin).toBe('top left');
  });

  it('flips above only when there is more room above', () => {
    const low = { top: 480, bottom: 512, left: 100, width: 400 };
    const spot = placeList(low, 320, 400, view);
    expect(spot.origin).toBe('bottom left');
    expect(spot.top).toBeGreaterThanOrEqual(8);
    expect(spot.top + spot.maxHeight).toBeLessThanOrEqual(low.top - 6);
  });

  it('keeps a wide list inside the right edge', () => {
    const far = { top: 100, bottom: 132, left: 900, width: 90 };
    expect(placeList(far, 100, 300, view).left).toBe(1000 - 8 - 300);
  });
});
