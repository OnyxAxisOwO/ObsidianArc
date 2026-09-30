// The titled card of rows the settings and the backoffice are drawn with. What
// is held here is the structure the stylesheet relies on — the heading outside
// the card, everything else a direct child of it, so one hairline rule divides
// any of them — and that AdminControlCard, which every backoffice page builds
// from, is one of these rather than a second shape that could drift from it.

import { afterEach, describe, expect, it } from 'vitest';
import { createApp, defineComponent, h, type App } from 'vue';
import OaGroup from '@/components/OaGroup.vue';
import OaRow from '@/components/OaRow.vue';
import { IconKey } from '@/icons';
import AdminControlCard from '@/views/admin/AdminControlCard.vue';

let app: App | null = null;

afterEach(() => {
  app?.unmount();
  app = null;
  document.body.textContent = '';
});

function mount(render: () => ReturnType<typeof h>): HTMLElement {
  const host = document.createElement('div');
  document.body.appendChild(host);
  app = createApp(defineComponent({ render }));
  app.mount(host);
  return host;
}

describe('OaGroup', () => {
  it('puts the heading above the card, and the rows inside it', () => {
    const host = mount(() => h(OaGroup, { title: 'Profile', hint: 'Who you are' }, {
      default: () => [h(OaRow, { title: 'One' }), h(OaRow, { title: 'Two' })],
    }));
    const group = host.querySelector('.oa-group')!;
    expect([...group.children].map((node) => node.className)).toEqual(['oa-group-head', 'oa-group-card']);
    expect(group.querySelector('.oa-group-title')?.textContent).toBe('Profile');
    expect(group.querySelector('.oa-group-hint')?.textContent).toBe('Who you are');
    expect(group.querySelectorAll('.oa-group-card > .oa-group-row')).toHaveLength(2);
  });

  it('has no heading at all when it has nothing to say', () => {
    const host = mount(() => h(OaGroup, {}, { default: () => h(OaRow, { title: 'One' }) }));
    expect(host.querySelector('.oa-group-head')).toBeNull();
    expect(host.querySelector('.oa-group-card')).not.toBeNull();
  });

  it('keeps its actions in the heading, beside the title', () => {
    const host = mount(() => h(OaGroup, { title: 'Bars' }, {
      actions: () => h('button', { class: 'act' }, 'Add'),
      default: () => h(OaRow, { title: 'One' }),
    }));
    expect(host.querySelector('.oa-group-head > .act')?.textContent).toBe('Add');
  });
});

describe('OaRow', () => {
  it('reads as text on the left and a control on the right', () => {
    const host = mount(() => h(OaRow, { title: 'GitHub', meta: 'octocat', icon: IconKey }, {
      default: () => h('button', 'Disconnect'),
    }));
    const row = host.querySelector('.oa-group-row')!;
    expect(row.classList.contains('stacked')).toBe(false);
    expect(row.querySelector('.oa-group-row-mark svg')).not.toBeNull();
    expect(row.querySelector('.oa-group-row-title')?.textContent).toBe('GitHub');
    expect(row.querySelector('.oa-group-row-meta')?.textContent).toBe('octocat');
    expect(row.querySelector('.oa-group-row-control button')?.textContent).toBe('Disconnect');
  });

  it('draws no empty control or text column for what it was not given', () => {
    const host = mount(() => h(OaRow, { title: 'Only a line' }));
    expect(host.querySelector('.oa-group-row-control')).toBeNull();
    expect(host.querySelector('.oa-group-row-meta')).toBeNull();
    expect(host.querySelector('.oa-group-row-mark')).toBeNull();
  });

  it('gives a stacked row to a field, which brings its own label', () => {
    const host = mount(() => h(OaRow, { stacked: true }, { default: () => h('input') }));
    expect(host.querySelector('.oa-group-row.stacked .oa-group-row-control input')).not.toBeNull();
    expect(host.querySelector('.oa-group-row-text')).toBeNull();
  });

  it('lets the text column carry more than a title and a line', () => {
    const host = mount(() => h(OaRow, { title: 'Plugin' }, {
      text: () => h('p', { class: 'fault' }, 'not running'),
    }));
    expect(host.querySelector('.oa-group-row-text .fault')?.textContent).toBe('not running');
  });
});

describe('AdminControlCard', () => {
  it('is a group, with its id and class on the one root', () => {
    const host = mount(() => h(AdminControlCard, { id: 'secBackup', title: 'Schedule', hint: 'When', icon: IconKey }, {
      actions: () => h('button', { class: 'act' }, 'Run'),
      default: () => h('p', { class: 'body' }, 'inside'),
    }));
    const card = host.querySelector('.oa-control-card')!;
    expect(card.id).toBe('secBackup');
    expect(card.classList.contains('oa-group')).toBe(true);
    expect(card.querySelector('.oa-group-title')?.textContent).toBe('Schedule');
    expect(card.querySelector('.oa-group-title svg')).not.toBeNull();
    expect(card.querySelector('.oa-group-hint')?.textContent).toBe('When');
    expect(card.querySelector('.oa-group-head > .act')?.textContent).toBe('Run');
    expect(card.querySelector('.oa-group-card > .body')?.textContent).toBe('inside');
  });
});
