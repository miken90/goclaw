import { useState, useEffect, useCallback } from "react";
import { useHttp } from "@/hooks/use-ws";

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

export function useProviders() {
  const http = useHttp();
  const [providers, setProviders] = useState<ProviderData[]>([]);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await http.get<{ providers: ProviderData[] }>("/v1/providers");
      setProviders(res.providers ?? []);
    } catch {
      // ignore
    } finally {
      setLoading(false);
    }
  }, [http]);

  useEffect(() => {
    load();
  }, [load]);

  const createProvider = useCallback(
    async (data: ProviderInput) => {
      const res = await http.post<ProviderData>("/v1/providers", data);
      await load();
      return res;
    },
    [http, load],
  );

  const updateProvider = useCallback(
    async (id: string, data: Partial<ProviderInput>) => {
      await http.put(`/v1/providers/${id}`, data);
      await load();
    },
    [http, load],
  );

  const deleteProvider = useCallback(
    async (id: string) => {
      await http.delete(`/v1/providers/${id}`);
      await load();
    },
    [http, load],
  );

  return {
    providers,
    loading,
    refresh: load,
    createProvider,
    updateProvider,
    deleteProvider,
  };
}
