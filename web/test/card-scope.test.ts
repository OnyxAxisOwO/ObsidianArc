// The words a card's windows read as. The admin screens and the usage panel
// all draw a card's scope from this one function, so its order and its
// fallbacks are what every one of them shows.

import { describe, expect, it } from 'vitest';
import { changeLanguage, t } from '../src/composables/useI18n';
import { cardTitle, formatCardScope, windowChoices } from '../src/lib/card-scope';
import { scopePresetChoices } from '../src/admin/quota';
import '../src/i18n.admin';
import { isAbortError } from '../src/lib/abort';

describe('card scope wording', () => {
  it('reads a card with no windows, or the full one, as a full reset', async () => {
    await changeLanguage('en');
    expect(formatCardScope({})).toBe(t('cardFullReset'));
    expect(formatCardScope({ windows: [] })).toBe(t('cardFullReset'));
    expect(formatCardScope({ windows: ['5h', 'full'] })).toBe(t('cardFullReset'));
  });

  it('lists the windows shortest first, whatever order the server sent them in', async () => {
    await changeLanguage('en');
    expect(formatCardScope({ windows: ['1m', '5h'] })).toBe(formatCardScope({ windows: ['5h', '1m'] }));
    expect(formatCardScope({ windows: ['1m', '5h'] })).toBe(
      t('cardScopeCombined', { windows: `${t('cardScopeLabel5H')} + ${t('cardScopeLabel1M')}` }),
    );
  });

  it('uses the standalone wording for a single window and the list wording inside several', async () => {
    await changeLanguage('en');
    expect(formatCardScope({ windows: ['1w'] })).toBe(t('cardScope1W'));
    expect(formatCardScope({ windows: ['1w', '5h'] })).toBe(
      t('cardScopeCombined', { windows: `${t('cardScopeLabel5H')} + ${t('cardScopeLabel1W')}` }),
    );
  });

  it('shows a window this build has no name for as the server spelled it', async () => {
    await changeLanguage('en');
    expect(formatCardScope({ windows: ['2d'] })).toBe(t('cardScopeCombined', { windows: '2d' }));
  });

  it('titles a card by its own name when it has one', async () => {
    await changeLanguage('en');
    expect(cardTitle({ name: '  Spring promo  ', windows: ['5h'] })).toBe('Spring promo');
    expect(cardTitle({ name: '   ', windows: ['5h'] })).toBe(t('cardScope5H'));
  });

  it('offers the three windows as choices, shortest first', async () => {
    await changeLanguage('en');
    expect(windowChoices().map((choice) => choice.value)).toEqual(['5h', '1w', '1m']);
    expect(windowChoices()[1]!.label).toBe(t('cardScopeLabel1W'));
  });

  it('offers the reset presets as everything, each window, then a hand-picked set', async () => {
    await changeLanguage('en');
    expect(scopePresetChoices().map((choice) => choice.value)).toEqual(['full', '5h', '1w', '1m', 'custom']);
    expect(scopePresetChoices()[0]!.label).toBe(t('cardResetFull'));
    expect(scopePresetChoices()[4]!.label).toBe(t('cardResetCustom'));
  });
});

describe('abort detection', () => {
  it('recognises a cancelled request by its name, whatever class carries it', () => {
    expect(isAbortError(new DOMException('stopped', 'AbortError'))).toBe(true);
    expect(isAbortError(Object.assign(new Error('stopped'), { name: 'AbortError' }))).toBe(true);
    expect(isAbortError({ name: 'AbortError' })).toBe(true);
  });

  it('does not take any other failure for a cancellation', () => {
    expect(isAbortError(new Error('network down'))).toBe(false);
    expect(isAbortError('AbortError')).toBe(false);
    expect(isAbortError(null)).toBe(false);
  });
});
