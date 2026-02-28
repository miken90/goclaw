import { useCallback } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useHttp } from "@/hooks/use-ws";
import { queryKeys } from "@/lib/query-keys";
import type { CustomToolData, CustomToolInput } from "@/types/custom-tool";

export type { CustomToolData, CustomToolInput };

export interface CustomToolFilters {
  search?: string;
  limit?: number;
  offset?: number;
}

export function useCustomTools(filters: CustomToolFilters = {}) {
  const http = useHttp();
  const queryClient = useQueryClient();

  const queryKey = queryKeys.customTools.list({ ...filters });

  const { data, isLoading: loading } = useQuery({
    queryKey,
    queryFn: async () => {
      const params: Record<string, string> = {};
      if (filters.search) params.search = filters.search;
      if (filters.limit) params.limit = String(filters.limit);
      if (filters.offset !== undefined) params.offset = String(filters.offset);

      const res = await http.get<{ tools: CustomToolData[]; total?: number }>("/v1/tools/custom", params);
      return { tools: res.tools ?? [], total: res.total ?? 0 };
    },
    placeholderData: (prev) => prev,
  });

  const tools = data?.tools ?? [];
  const total = data?.total ?? 0;

  const invalidate = useCallback(
    () => queryClient.invalidateQueries({ queryKey: queryKeys.customTools.all }),
    [queryClient],
  );

  const createTool = useCallback(
    async (data: CustomToolInput) => {
      const res = await http.post<{ id: string }>("/v1/tools/custom", data);
      await invalidate();
      return res;
    },
    [http, invalidate],
  );

  const updateTool = useCallback(
    async (id: string, data: Partial<CustomToolInput>) => {
      await http.put(`/v1/tools/custom/${id}`, data);
      await invalidate();
    },
    [http, invalidate],
  );

  const deleteTool = useCallback(
    async (id: string) => {
      await http.delete(`/v1/tools/custom/${id}`);
      await invalidate();
    },
    [http, invalidate],
  );

  return { tools, total, loading, refresh: invalidate, createTool, updateTool, deleteTool };
}
