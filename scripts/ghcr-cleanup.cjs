const { execFileSync } = require('node:child_process');

const releaseTag = /^(v(\d+)\.(\d+)\.(\d+)(?:-rc\.(\d+))?)(?:-amd64|-arm64)?$/;

async function deletions(versions, currentTag, manifest, keep = 5) {
  const groups = new Map();
  const protectedGroups = new Set([currentTag]);
  for (const version of versions) {
    const tags = version.metadata.container.tags;
    const releases = tags.map(tag => releaseTag.exec(tag)).filter(Boolean);
    for (const match of releases) {
      groups.set(match[1], match.slice(2).map(value => Number(value || 0)));
      if (tags.some(tag => !releaseTag.test(tag))) protectedGroups.add(match[1]);
    }
  }
  for (const prerelease of [false, true]) {
    const sorted = [...groups].filter(([tag]) => tag.includes('-rc.') === prerelease)
      .sort((a, b) => {
        for (let i = 0; i < 4; i++) {
          if (a[1][i] !== b[1][i]) return b[1][i] - a[1][i];
        }
        return 0;
      });
    for (const [tag] of sorted.slice(0, keep)) protectedGroups.add(tag);
  }
  const protectedDigests = new Set();
  const pending = versions.filter(version => version.metadata.container.tags.some(tag =>
    !releaseTag.test(tag) || protectedGroups.has(releaseTag.exec(tag)[1])))
    .map(version => version.name);
  // A retained multi-platform index may reference untagged image manifests.
  // Deleting those children breaks pulls even though the version tag survives.
  while (pending.length) {
    const digest = pending.pop();
    if (protectedDigests.has(digest)) continue;
    protectedDigests.add(digest);
    const document = await manifest(digest);
    for (const child of document.manifests || []) pending.push(child.digest);
  }
  return versions.filter(version => !protectedDigests.has(version.name))
    .sort((a, b) => b.created_at.localeCompare(a.created_at));
}

async function main() {
  const [owner, repository] = process.env.GH_REPO.split('/');
  const name = repository.toLowerCase();
  const endpoint = `users/${owner}/packages/container/${name}/versions`;
  const api = args => execFileSync('gh', ['api', ...args], { encoding: 'utf8' });
  const versions = JSON.parse(api(['--paginate', '--slurp', `${endpoint}?per_page=100`])).flat();
  const image = `${owner.toLowerCase()}/${name}`;
  const tokenResponse = await fetch(`https://ghcr.io/token?service=ghcr.io&scope=repository:${image}:pull`);
  if (!tokenResponse.ok) throw new Error(`Registry authentication: HTTP ${tokenResponse.status}`);
  const { token } = await tokenResponse.json();
  const manifest = async digest => {
    const response = await fetch(`https://ghcr.io/v2/${image}/manifests/${digest}`, {
      headers: {
        Authorization: `Bearer ${token}`,
        Accept: 'application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json',
      },
    });
    if (!response.ok) throw new Error(`Read manifest ${digest}: HTTP ${response.status}`);
    return response.json();
  };
  const remove = await deletions(versions, process.env.RELEASE_TAG || '', manifest);
  console.log(`GHCR: ${versions.length} stored manifests, deleting ${remove.length}.`);
  for (const version of remove) {
    console.log(`Delete ${version.name} (${version.metadata.container.tags.join(', ') || 'untagged'})`);
    api(['--method', 'DELETE', `${endpoint}/${version.id}`]);
  }
}

module.exports = { deletions };
if (require.main === module) main().catch(error => { console.error(error); process.exitCode = 1; });
