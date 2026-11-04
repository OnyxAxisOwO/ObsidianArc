import { afterEach, describe, expect, it, vi } from 'vitest';
import { ccSwitchImportURL, openCCSwitch, type CCSwitchProvider } from '../src/lib/cc-switch';

const PROVIDER: CCSwitchProvider = {
  app: 'claude',
  origin: 'https://arc.example.test/',
  name: '站点 · 工作 & + # ? %',
  token: 'sk-test&model=injected+secret#fragment',
  model: 'model-id/with+symbols &中文',
};

afterEach(() => vi.unstubAllGlobals());

describe('CC Switch provider import', () => {
  it('encodes a Claude provider with a root endpoint and restricted model aliases', () => {
    const url = new URL(ccSwitchImportURL(PROVIDER));
    expect(url.protocol).toBe('ccswitch:');
    expect(url.host).toBe('v1');
    expect(url.pathname).toBe('/import');
    expect(url.hash).toBe('');
    expect(Object.fromEntries(url.searchParams)).toEqual({
      resource: 'provider',
      app: 'claude',
      name: PROVIDER.name,
      homepage: 'https://arc.example.test',
      endpoint: 'https://arc.example.test',
      apiKey: PROVIDER.token,
      model: PROVIDER.model,
      enabled: 'false',
      haikuModel: PROVIDER.model,
      sonnetModel: PROVIDER.model,
      opusModel: PROVIDER.model,
    });
    expect(url.searchParams.getAll('model')).toHaveLength(1);
  });

  it('uses /v1 for Codex without Claude aliases or remote configuration', () => {
    const url = new URL(ccSwitchImportURL({ ...PROVIDER, app: 'codex' }));
    expect(Object.fromEntries(url.searchParams)).toEqual({
      resource: 'provider',
      app: 'codex',
      name: PROVIDER.name,
      homepage: 'https://arc.example.test',
      endpoint: 'https://arc.example.test/v1',
      apiKey: PROVIDER.token,
      model: PROVIDER.model,
      enabled: 'false',
    });
  });

  it('hands the custom protocol to the browser exactly once', () => {
    const assign = vi.fn();
    vi.stubGlobal('window', { location: { assign } });
    openCCSwitch(PROVIDER);
    expect(assign).toHaveBeenCalledExactlyOnceWith(ccSwitchImportURL(PROVIDER));
  });
});
