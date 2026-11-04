import { IconGithub, IconGoogle, IconKey, type OaIcon } from '@/icons';

const MARKS: Record<string, OaIcon> = { github: IconGithub, google: IconGoogle };

// The mark a sign-in provider is drawn with. A provider with no picture of its
// own gets the generic key. The lookup checks the object's own keys, because a
// provider id is whatever the server was configured with, and an inherited name
// such as "constructor" would otherwise come back as a component.
export function providerMark(id: string): OaIcon {
  return Object.hasOwn(MARKS, id) ? MARKS[id]! : IconKey;
}
