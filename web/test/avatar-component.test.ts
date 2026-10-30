import { afterEach, describe, expect, it } from 'vitest';
import { createApp, h, nextTick, reactive } from 'vue';
import OaAvatar from '../src/components/OaAvatar.vue';
import { makeAccount } from './fixtures/account';

let cleanup: (() => void) | undefined;
afterEach(() => { cleanup?.(); cleanup = undefined; });

describe('account avatar rendering', () => {
  it.each(['gravatar', 'weavatar'])('uses a custom avatar first, then tries %s and its backup before initials', async (provider) => {
    const fallback = `https://${provider}.com/avatar/${'a'.repeat(64)}?s=160&d=404&r=g`;
    const account = reactive(makeAccount({ nickname: 'Ada Lovelace', avatar: '/avatar.png', default_avatar: fallback }));
    const host = document.createElement('div');
    document.body.append(host);
    const app = createApp({ render: () => h(OaAvatar, { account }) });
    app.mount(host);
    cleanup = () => { app.unmount(); host.remove(); };
    expect(host.querySelector('img')?.getAttribute('src')).toBe('/avatar.png');
    account.avatar = '';
    await nextTick();
    expect(host.querySelector('img')?.getAttribute('src')).toBe(fallback);
    host.querySelector('img')!.dispatchEvent(new Event('error'));
    await nextTick();
    expect(host.querySelector('img')?.getAttribute('src')).toBe(fallback.replace(`${provider}.com`, provider === 'gravatar' ? 'weavatar.com' : 'gravatar.com'));
    host.querySelector('img')!.dispatchEvent(new Event('error'));
    await nextTick();
    expect(host.querySelector('img')).toBeNull();
    expect(host.textContent).toBe('AL');
    account.avatar = '/new-avatar.png';
    await nextTick();
    expect(host.querySelector('img')?.getAttribute('src')).toBe('/new-avatar.png');
  });
});
