import type { LegalDocument } from '@/api/auth';

export function legalAcceptanceMissing(documents: LegalDocument[], accepted: Record<string, boolean>): boolean {
  return documents.some((document) => document.required && !accepted[document.id]);
}
