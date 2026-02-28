import { useState, useCallback } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useHttp } from "@/hooks/use-ws";
import { queryKeys } from "@/lib/query-keys";
import type { ChannelInstanceData, ChannelInstanceInput } from "@/types/channel";

export type { ChannelInstanceData, ChannelInstanceInput };

export interface ChannelInstanceFilters {
  search?: string;
  limit?: number;
  offset?: number;
}

export function useChannelInstances(filters: ChannelInstanceFilters = {}) {
  const http = useHttp();
  const queryClient = useQueryClient();
  const [supported, setSupported] = useState(true); // false if standalone mode (404)

  const queryKey = queryKeys.channels.list({ ...filters });

  const { data, isLoading: loading } = useQuery({
    queryKey,
    queryFn: async () => {
      try {
        const params: Record<string, string> = {};
        if (filters.search) params.search = filters.search;
        if (filters.limit) params.limit = String(filters.limit);
        if (filters.offset !== undefined) params.offset = String(filters.offset);

        const res = await http.get<{ instances: ChannelInstanceData[]; total?: number }>("/v1/channels/instances", params);
        setSupported(true);
        return { instances: res.instances ?? [], total: res.total ?? 0 };
      } catch (err: unknown) {
        // 404 means standalone mode — channel instances not available
        if (err instanceof Error && err.message.includes("404")) {
          setSupported(false);
        }
        return { instances: [], total: 0 };
      }
    },
    placeholderData: (prev) => prev,
  });

  const instances = data?.instances ?? [];
  const total = data?.total ?? 0;

  const invalidate = useCallback(
    () => queryClient.invalidateQueries({ queryKey: queryKeys.channels.all }),
    [queryClient],
  );

  const createInstance = useCallback(
    async (data: ChannelInstanceInput) => {
      const res = await http.post<{ id: string }>("/v1/channels/instances", data);
      await invalidate();
      return res;
    },
    [http, invalidate],
  );

  const updateInstance = useCallback(
    async (id: string, data: Partial<ChannelInstanceInput>) => {
      await http.put(`/v1/channels/instances/${id}`, data);
      await invalidate();
    },
    [http, invalidate],
  );

  const deleteInstance = useCallback(
    async (id: string) => {
      await http.delete(`/v1/channels/instances/${id}`);
      await invalidate();
    },
    [http, invalidate],
  );

  return { instances, total, loading, supported, refresh: invalidate, createInstance, updateInstance, deleteInstance };
}
