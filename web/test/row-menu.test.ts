import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it, vi } from 'vitest';

import { useRowMenu } from '../src/chat/useRowMenu';

// The project tab's copy of this measured against the window with its own
// threshold, so inside the narrow-screen rail — a short list far above the
// window's bottom — its menus always opened downwards, past the list's edge.

function rect(top: number, bottom: number): DOMRect {
  return { top, bottom, left: 0, right: 100, width: 100, height: bottom - top, x: 0, y: top, toJSON: () => ({}) };
}

function rowIn(listTop: number, listBottom: number, buttonTop: number): HTMLButtonElement {
  const list = document.createElement('div');
  list.className = 'ai-chat-list';
  list.getBoundingClientRect = () => rect(listTop, listBottom);
  const item = document.createElement('div');
  item.className = 'ai-chat-list-item';
  const button = document.createElement('button');
  button.className = 'ai-chat-list-more';
  button.getBoundingClientRect = () => rect(buttonTop, buttonTop + 24);
  item.appendChild(button);
  list.appendChild(item);
  document.body.appendChild(list);
  return button;
}

function press(button: HTMLElement, trigger: ReturnType<typeof useRowMenu>['trigger'], id: string): void {
  button.addEventListener('click', (event) => trigger(event, id, () => {}), { once: true });
  button.click();
}

describe('the conversation row menu', () => {
  it('opens upwards near the bottom of its list, even with the window far below', () => {
    vi.stubGlobal('innerHeight', 2000);
    const { openUp, trigger } = useRowMenu();
    // The list ends 300px down a 2000px window; the row is at its foot.
    press(rowIn(0, 300, 260), trigger, 'a');
    expect(openUp.value.a).toBe(true);
    vi.unstubAllGlobals();
  });

  it('opens downwards with room below it in the list', () => {
    const { openUp, trigger } = useRowMenu();
    press(rowIn(0, 600, 40), trigger, 'b');
    expect(openUp.value.b).toBe(false);
  });

  it('forwards a right-click on the row to its own menu button', () => {
    const { contextMenu } = useRowMenu();
    const button = rowIn(0, 600, 40);
    const clicked = vi.fn();
    button.addEventListener('click', clicked);
    const row = button.parentElement!;
    row.addEventListener('contextmenu', contextMenu);
    row.dispatchEvent(new MouseEvent('contextmenu'));
    expect(clicked).toHaveBeenCalledTimes(1);
  });

  // A project's conversations sit inside two overflow-hidden boxes for the
  // open/close slide, which cut the menu down to a few pixels.
  it('lifts the project list clips while a row menu is mounted inside it', () => {
    const scss = fs.readFileSync(path.resolve(__dirname, '../src/styles/folders.scss'), 'utf8');
    const rule = /\.ai-project-convs-collapse\.open:has\(\.ai-chat-context-menu\),\s*\.ai-project-convs-collapse\.open:has\(\.ai-chat-context-menu\) > \.ai-project-convs-inner \{([^}]*)\}/
      .exec(scss)?.[1] ?? '';
    expect(rule).toMatch(/overflow:\s*visible/);
  });
});
