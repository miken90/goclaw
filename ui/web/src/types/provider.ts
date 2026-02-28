export interface ProviderData {
  id: string;
  name: string;
  display_name: string;
  provider_type: string;
  api_base: string;
  api_key: string; // masked "***" from server
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface ProviderInput {
  name: string;
  display_name?: string;
  provider_type: string;
  api_base?: string;
  api_key?: string;
  enabled?: boolean;
}

export interface ModelInfo {
  id: string;
  name?: string;
}
