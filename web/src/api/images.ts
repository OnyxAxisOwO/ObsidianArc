// The image generation API.

import { api } from './client';

export interface ImageGenerationItem {
  id?: string;
  attachment_id?: string;
  url?: string;
  b64_json?: string;
  revised_prompt?: string;
  created_at?: number;
}

export interface ImageGenerationRecord {
  id: string;
  user_id: string;
  attachment_id: string;
  url: string;
  model_id: string;
  prompt: string;
  revised_prompt: string;
  size: string;
  style: string;
  created_at: number;
}

export interface ImageGenerationRequest {
  model_id: string;
  prompt: string;
  size?: string;
  style?: string;
  quality?: string;
  n?: number;
  /** Base64, no data: prefix — a picture for the prompt to work from. */
  image?: string;
  /** Base64, no data: prefix — pictures for the prompt to work from. */
  images?: string[];
}

export interface ImageGenerationResponse {
  created: number;
  images: ImageGenerationItem[];
}

export function generateImages(req: ImageGenerationRequest): Promise<ImageGenerationResponse> {
  return api.post<ImageGenerationResponse>('/api/images/generate', req);
}

export function listImageGenerations(params?: { limit?: number; before?: number }): Promise<{ generations: ImageGenerationRecord[] }> {
  const query = new URLSearchParams();
  if (params?.limit) query.set('limit', String(params.limit));
  if (params?.before) query.set('before', String(params.before));
  const qs = query.toString();
  return api.get<{ generations: ImageGenerationRecord[] }>(`/api/images/generations${qs ? `?${qs}` : ''}`);
}

export function deleteImageGeneration(id: string): Promise<void> {
  return api.delete(`/api/images/generations/${id}`);
}
