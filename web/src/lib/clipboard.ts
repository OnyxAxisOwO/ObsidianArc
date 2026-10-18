/**
 * Writes text to the clipboard, reporting whether it landed.
 *
 * One implementation that every copy button shares is the point — the code
 * block, the transcript and the settings screens alike: two would drift
 * apart on the fallback, which is the half nobody exercises. It lives in
 * lib/ so the screens outside the chat do not reach into its renderer.
 */
export async function copyToClipboard(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // navigator.clipboard does not exist on a plain-http origin and its
    // permission can be refused outright, so without the path below the copy
    // control would silently do nothing on exactly the deployments that have
    // no TLS in front of them.
  }

  const staging = document.createElement('textarea');
  staging.value = text;
  staging.readOnly = true;
  // Off-screen rather than hidden: execCommand copies a selection, and an
  // element that is not rendered cannot hold one. Fixed positioning keeps the
  // page from scrolling to it.
  staging.style.position = 'fixed';
  staging.style.top = '-1000px';
  staging.style.opacity = '0';
  document.body.appendChild(staging);
  try {
    staging.select();
    return document.execCommand('copy');
  } catch {
    return false;
  } finally {
    staging.remove();
  }
}
