import { useCallback } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useHttp } from "@/hooks/use-ws";
import { queryKeys } from "@/lib/query-keys";
import type { ProviderData, ProviderInput } from "@/types/provider";

export type { ProviderData, ProviderInput };

export function useProviders() {
  const http = useHttp();
  const queryClient = useQueryClient();

  const { data: providers = [], isLoading: loading } = useQuery({
    queryKey: queryKeys.providers.all,
    queryFn: async () => {
      const res = await http.get<{ providers: ProviderData[] }>("/v1/providers");
      return res.providers ?? [];
    },
  });

  const invalidate = useCallback(
    () => queryClient.invalidateQueries({ queryKey: queryKeys.providers.all }),
    [queryClient],
  );

  const createProvider = useCallback(
    async (data: ProviderInput) => {
      const res = await http.post<ProviderData>("/v1/providers", data);
      await invalidate();
      return res;
    },
    [http, invalidate],
  );

  const updateProvider = useCallback(
    async (id: string, data: Partial<ProviderInput>) => {
      await http.put(`/v1/providers/${id}`, data);
      await invalidate();
    },
    [http, invalidate],
  );

  const deleteProvider = useCallback(
    async (id: string) => {
      await http.delete(`/v1/providers/${id}`);
      await invalidate();
    },
    [http, invalidate],
  );

  return {
    providers,
    loading,
    refresh: invalidate,
    createProvider,
    updateProvider,
    deleteProvider,
  };
}
