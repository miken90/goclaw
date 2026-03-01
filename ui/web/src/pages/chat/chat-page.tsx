import { useState, useCallback, useEffect, useRef } from "react";
import { useParams, useNavigate } from "react-router";
import { Eye } from "lucide-react";
import { useAuthStore } from "@/stores/use-auth-store";
import { ChatSidebar } from "./chat-sidebar";
import { ChatThread } from "./chat-thread";
import { ChatInput } from "@/components/chat/chat-input";
import { useChatSessions } from "./hooks/use-chat-sessions";
import { useChatMessages } from "./hooks/use-chat-messages";
import { useChatSend } from "./hooks/use-chat-send";
import { isOwnSession } from "@/lib/session-key";

export function ChatPage() {
  const { sessionKey: urlSessionKey } = useParams<{ sessionKey: string }>();
  const navigate = useNavigate();
  const connected = useAuthStore((s) => s.connected);
  const userId = useAuthStore((s) => s.userId);

  const [agentId, setAgentId] = useState("default");
  const [sessionKey, setSessionKey] = useState(urlSessionKey ?? "");

  const {
    sessions,
    loading: sessionsLoading,
    refresh: refreshSessions,
    buildNewSessionKey,
  } = useChatSessions(agentId);

  const {
    messages,
    streamText,
    thinkingText,
    toolStream,
    isRunning,
    loading: messagesLoading,
    expectRun,
    addLocalMessage,
  } = useChatMessages(sessionKey, agentId);

  // Sync URL param to state
  useEffect(() => {
    if (urlSessionKey && urlSessionKey !== sessionKey) {
      setSessionKey(urlSessionKey);
    }
  }, [urlSessionKey, sessionKey]);

  // Refresh sessions when run completes
  const prevIsRunningRef = useRef(false);
  useEffect(() => {
    if (prevIsRunningRef.current && !isRunning) {
      refreshSessions();
    }
    prevIsRunningRef.current = isRunning;
  }, [isRunning, refreshSessions]);

  const isOwn = !sessionKey || isOwnSession(sessionKey, userId);

  const handleMessageAdded = useCallback(
    (msg: { role: "user" | "assistant" | "tool"; content: string; timestamp?: number }) => {
      addLocalMessage(msg);
    },
    [addLocalMessage],
  );

  const { send, abort, error: sendError } = useChatSend({
    agentId,
    onMessageAdded: handleMessageAdded,
    onExpectRun: expectRun,
  });

  const handleNewChat = useCallback(() => {
    const newKey = buildNewSessionKey();
    setSessionKey(newKey);
    navigate(`/chat/${encodeURIComponent(newKey)}`);
  }, [buildNewSessionKey, navigate]);

  const handleSessionSelect = useCallback(
    (key: string) => {
      setSessionKey(key);
      navigate(`/chat/${encodeURIComponent(key)}`);
    },
    [navigate],
  );

  const handleAgentChange = useCallback(
    (newAgentId: string) => {
      setAgentId(newAgentId);
      const newKey = `agent:${newAgentId}:ws-${userId}-${Date.now().toString(36)}`;
      setSessionKey(newKey);
      navigate(`/chat/${encodeURIComponent(newKey)}`);
    },
    [navigate, userId],
  );

  const handleSend = useCallback(
    (message: string) => {
      let key = sessionKey;
      if (!key) {
        key = buildNewSessionKey();
        setSessionKey(key);
        navigate(`/chat/${encodeURIComponent(key)}`);
      }
      // Pass key directly so send() doesn't use a stale closure value
      send(message, key);
    },
    [sessionKey, send, buildNewSessionKey, navigate],
  );

  const handleAbort = useCallback(() => {
    abort(sessionKey);
  }, [abort, sessionKey]);

  return (
    <div className="flex h-full">
      {/* Sidebar */}
      <ChatSidebar
        agentId={agentId}
        onAgentChange={handleAgentChange}
        sessions={sessions}
        sessionsLoading={sessionsLoading}
        activeSessionKey={sessionKey}
        onSessionSelect={handleSessionSelect}
        onNewChat={handleNewChat}
      />

      {/* Main chat area */}
      <div className="flex flex-1 flex-col">
        {sendError && (
          <div className="border-b bg-destructive/10 px-4 py-2 text-sm text-destructive">
            {sendError}
          </div>
        )}

        <ChatThread
          messages={messages}
          streamText={streamText}
          thinkingText={thinkingText}
          toolStream={toolStream}
          isRunning={isRunning}
          loading={messagesLoading}
        />

        {isOwn ? (
          <ChatInput
            onSend={handleSend}
            onAbort={handleAbort}
            isRunning={isRunning}
            disabled={!connected}
          />
        ) : (
          <div className="flex items-center gap-2 border-t bg-muted/50 px-4 py-3 text-sm text-muted-foreground">
            <Eye className="h-4 w-4" />
            Read-only — this session belongs to another user
          </div>
        )}
      </div>
    </div>
  );
}
