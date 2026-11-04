import { describe, expect, it } from 'vitest';
import { joinPanelStack } from '@/composables/usePanelStack';

describe('panel stack', () => {
  it('leaves a lone panel open', () => {
    const row = document.createElement('div');
    const a = joinPanelStack(row);
    expect(a.collapsed.value).toBe(false);
  });

  it('folds the older panels when a second opens, and swaps on activate', () => {
    const row = document.createElement('div');
    const a = joinPanelStack(row);
    const b = joinPanelStack(row);
    expect(a.collapsed.value).toBe(true);
    expect(b.collapsed.value).toBe(false);
    a.activate();
    expect(a.collapsed.value).toBe(false);
    expect(b.collapsed.value).toBe(true);
  });

  it('opens the newest remaining panel when the open one leaves', () => {
    const row = document.createElement('div');
    const a = joinPanelStack(row);
    const b = joinPanelStack(row);
    const c = joinPanelStack(row);
    c.leave();
    c.leave();
    expect(b.collapsed.value).toBe(false);
    expect(a.collapsed.value).toBe(true);
    b.leave();
    expect(a.collapsed.value).toBe(false);
  });

  it('keeps rows apart', () => {
    const a = joinPanelStack(document.createElement('div'));
    const b = joinPanelStack(document.createElement('div'));
    expect(a.collapsed.value).toBe(false);
    expect(b.collapsed.value).toBe(false);
  });
});
