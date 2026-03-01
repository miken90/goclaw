import { useEffect, useRef, useMemo } from "react";
import { WsClient, type ConnectionState } from "@/api/ws-client";
import { HttpClient } from "@/api/http-client";
import { WsContext } from "@/hooks/use-ws";
import { useAuthStore } from "@/stores/use-auth-store";
import { useWsQueryInvalidation } from "@/hooks/use-query-invalidation";

// In dev mode, connect directly to backend WS (bypass Vite proxy).
// In production, use relative "/ws" path.
const WS_URL = import.meta.env.VITE_WS_URL || "/ws";

export function WsProvider({ children }: { children: React.ReactNode }) {
  const token = useAuthStore((s) => s.token);
  const userId = useAuthStore((s) => s.userId);
  const senderID = useAuthStore((s) => s.senderID);

  const wsRef = useRef<WsClient | null>(null);

  // Create WsClient once - survives StrictMode remounts
  if (!wsRef.current) {
    wsRef.current = new WsClient(
      WS_URL,
      () => useAuthStore.getState().token,
      () => useAuthStore.getState().userId,
      () => useAuthStore.getState().senderID,
      (state: ConnectionState) => {
        useAuthStore.getState().setConnected(state === "connected");
      },
    );
    wsRef.current.onAuthFailure = () => {
      useAuthStore.getState().logout();
    };
  }
  const ws = wsRef.current;

  const http = useMemo(() => {
    const client = new HttpClient(
      "",
      () => useAuthStore.getState().token,
      () => useAuthStore.getState().userId,
    );
    client.onAuthFailure = () => {
      useAuthStore.getState().logout();
    };
    return client;
  }, []);

  // Auto-connect when credentials are available (token or sender_id), disconnect when not.
  useEffect(() => {
    if ((token || senderID) && userId) {
      ws.connect();
    } else {
      ws.disconnect();
    }
  }, [token, userId, senderID, ws]);

  const value = useMemo(() => ({ ws, http }), [ws, http]);

  return (
    <WsContext.Provider value={value}>
      <WsQueryInvalidation />
      {children}
    </WsContext.Provider>
  );
}

function WsQueryInvalidation() {
  useWsQueryInvalidation();
  return null;
}
