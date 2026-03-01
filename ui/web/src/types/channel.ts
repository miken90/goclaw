export interface ChannelInstanceData {
  id: string;
  name: string;
  display_name: string;
  channel_type: string;
  agent_id: string;
  config: Record<string, unknown> | null;
  enabled: boolean;
  is_default: boolean;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface ChannelInstanceInput {
  name: string;
  display_name?: string;
  channel_type: string;
  agent_id: string;
  credentials?: Record<string, unknown>;
  config?: Record<string, unknown>;
  enabled?: boolean;
}
