export interface CustomToolData {
  id: string;
  name: string;
  description: string;
  parameters: Record<string, unknown> | null;
  command: string;
  working_dir: string;
  timeout_seconds: number;
  agent_id: string | null;
  enabled: boolean;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface CustomToolInput {
  name: string;
  description?: string;
  parameters?: Record<string, unknown>;
  command: string;
  working_dir?: string;
  timeout_seconds?: number;
  agent_id?: string;
  enabled?: boolean;
}
