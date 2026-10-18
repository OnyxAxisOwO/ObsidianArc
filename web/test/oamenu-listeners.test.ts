// A list mounts one menu per row. Each menu listens to the document only
// while it is open, so a closed one costs every key and click nothing — and
// the click that opens a menu must not also count as a click outside it.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import OaMenu from '../src/components/OaMenu.vue';

let app: App | undefined;

afterEach(() => {
  app?.unmount();
  app = undefined;
  vi.restoreAllMocks();
});

function keydownListeners(spy: { mock: { calls: unknown[][] } }): number {
  return spy.mock.calls.filter((call) => call[0] === 'keydown').length;
}

async function mountMenus(count: number) {
  const menus = Array.from({ length: count }, () => ref<InstanceType<typeof OaMenu> | null>(null));
  const host = document.createElement('div');
  document.body.append(host);
  app = createApp({
    render: () => h('div', [
      ...menus.map((menu, index) => h(OaMenu, { ref: menu }, {
        trigger: ({ toggle }: { toggle: () => void }) =>
          h('button', { type: 'button', class: `trigger-${index}`, onClick: toggle }, 'Open'),
        default: () => h('p', 'Item'),
      })),
      h('p', { class: 'outside' }, 'Elsewhere'),
    ]),
  });
  app.mount(host);
  await nextTick();
  return menus;
}

describe('OaMenu document listeners', () => {
  it('adds none for closed menus, one when opened, and removes it on close', async () => {
    const added = vi.spyOn(document, 'addEventListener');
    const removed = vi.spyOn(document, 'removeEventListener');
    const menus = await mountMenus(20);
    expect(keydownListeners(added)).toBe(0);

    menus[3]!.value!.open();
    expect(keydownListeners(added)).toBe(1);

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', cancelable: true }));
    expect(menus[3]!.value!.isOpen()).toBe(false);
    expect(keydownListeners(removed)).toBe(1);
  });

  it('stays open after the click that opened it, and closes on a click outside', async () => {
    const menus = await mountMenus(2);
    const trigger = document.querySelector<HTMLButtonElement>('.trigger-0')!;
    trigger.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
    trigger.click();
    await nextTick();
    expect(menus[0]!.value!.isOpen()).toBe(true);

    const outside = document.querySelector<HTMLElement>('.outside')!;
    outside.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
    outside.click();
    await nextTick();
    expect(menus[0]!.value!.isOpen()).toBe(false);
  });

  it('a second menu opened from its trigger closes the first', async () => {
    const menus = await mountMenus(2);
    document.querySelector<HTMLButtonElement>('.trigger-0')!.click();
    await nextTick();
    const second = document.querySelector<HTMLButtonElement>('.trigger-1')!;
    second.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
    second.click();
    await nextTick();
    expect(menus[0]!.value!.isOpen()).toBe(false);
    expect(menus[1]!.value!.isOpen()).toBe(true);
  });
});
