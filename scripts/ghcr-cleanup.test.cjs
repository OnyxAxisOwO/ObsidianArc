const assert = require('node:assert/strict');
const test = require('node:test');
const { deletions } = require('./ghcr-cleanup.cjs');

function version(id, ...tags) {
  return { id, name: `sha256:${id}`, created_at: '2026-11-06T00:00:00Z',
    metadata: { container: { tags } } };
}
const leaf = async () => ({});

test('keeps five semantic release groups and both architecture images', async () => {
  const versions = [1, 2, 3, 4, 5, 9, 10].flatMap(n => [
    version(n * 3, `v0.12.${n}`), version(n * 3 + 1, `v0.12.${n}-amd64`),
    version(n * 3 + 2, `v0.12.${n}-arm64`),
  ]);
  assert.deepEqual((await deletions(versions, '', leaf)).map(v => v.id), [3, 4, 5, 6, 7, 8]);
});

test('protects latest, custom tags and the release just backfilled', async () => {
  const versions = [version(1, 'v0.1.0', 'latest'), version(2, 'v0.1.0-arm64'),
    version(3, 'v0.2.0'), version(4, 'v0.3.0', 'pinned'), version(5, 'v0.4.0')];
  assert.deepEqual(await deletions(versions, 'v0.2.0', leaf, 1), []);
});

test('retains candidates independently without displacing stable releases', async () => {
  const versions = [version(1, 'v0.9.0'), version(2, 'v0.10.0'),
    version(3, 'v0.11.0-rc.2'), version(4, 'v0.11.0-rc.10')];
  assert.deepEqual((await deletions(versions, '', leaf, 1)).map(v => v.id), [1, 3]);
});

test('preserves transitively referenced untagged manifests and deletes orphaned ones', async () => {
  const versions = [version(1, 'v0.12.0', 'latest'), version(2), version(3), version(4)];
  const manifest = async digest => ({ manifests: digest === 'sha256:1'
    ? [{ digest: 'sha256:2' }] : digest === 'sha256:2' ? [{ digest: 'sha256:3' }] : [] });
  assert.deepEqual((await deletions(versions, '', manifest)).map(v => v.id), [4]);
});

test('does not plan any deletion when a retained manifest cannot be inspected', async () => {
  await assert.rejects(deletions([version(1, 'latest'), version(2)], '', async () => {
    throw new Error('registry unavailable');
  }), /registry unavailable/);
});
