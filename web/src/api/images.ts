// The image generation API.

import { api } from './client';

export interface ImageGenerationItem {
  attachment_id?: string;
  url?: string;
  b64_json?: string;
  revised_prompt?: string;
}

export interface ImageGenerationRequest {
  model_id: string;
  prompt: string;
  size?: string;
  style?: string;
  quality?: string;
  n?: number;
}

export interface ImageGenerationResponse {
  created: number;
  images: ImageGenerationItem[];
}

export function generateImages(req: ImageGenerationRequest): Promise<ImageGenerationResponse> {
  return api.post<ImageGenerationResponse>('/api/images/generate', req);
}
