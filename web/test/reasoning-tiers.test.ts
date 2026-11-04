// A budget that is not a finite number must not leave the page as Infinity:
// JSON sends that as null, and the server would take it for no budget at all.

import { afterEach, describe, expect, it } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import type { ReasoningTier } from '../src/admin/api';
import { changeLanguage, t } from '../src/composables/useI18n';
import ReasoningTiers from '../src/views/admin/ReasoningTiers.vue';

let app: App | undefined;
let host: HTMLElement;

afterEach(() => {
  app?.unmount();
  app = undefined;
  host.remove();
});

const typeIn = (selector: string, value: string) => {
  const field = host.querySelector<HTMLInputElement>(selector)!;
  field.value = value;
  field.dispatchEvent(new Event('input'));
};

describe('the reasoning tier budget', () => {
  it('sends 0 for a budget typed as Infinity or 1e400, and the number otherwise', async () => {
    await changeLanguage('en');
    const emitted: ReasoningTier[][] = [];
    host = document.createElement('div');
    document.body.append(host);
    app = createApp({
      render: () => h(ReasoningTiers, {
        modelValue: [],
        'onUpdate:modelValue': (value: ReasoningTier[]) => emitted.push(value),
      }),
    });
    app.mount(host);
    await nextTick();

    const addButton = [...host.querySelectorAll<HTMLButtonElement>('button')]
      .find((button) => button.textContent?.includes(t('addTier')))!;
    addButton.click();
    await nextTick();
    typeIn('.oa-thinking-name', 'Deep');
    typeIn('.oa-thinking-value', 'deep');

    typeIn('.oa-thinking-budget', '1e400');
    await nextTick();
    expect(emitted.at(-1)).toEqual([{ id: 'deep', name: 'Deep', budget: 0 }]);

    typeIn('.oa-thinking-budget', 'Infinity');
    await nextTick();
    expect(emitted.at(-1)).toEqual([{ id: 'deep', name: 'Deep', budget: 0 }]);

    typeIn('.oa-thinking-budget', '2048.7');
    await nextTick();
    expect(emitted.at(-1)).toEqual([{ id: 'deep', name: 'Deep', budget: 2048 }]);
  });
});
