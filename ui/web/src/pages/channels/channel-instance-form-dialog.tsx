import { useState, useEffect, useCallback } from "react";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { ChannelInstanceData, ChannelInstanceInput } from "./hooks/use-channel-instances";
import type { AgentData } from "@/types/agent";
import { slugify, isValidSlug } from "@/lib/slug";
import { credentialsSchema, configSchema } from "./channel-schemas";
import { ChannelFields } from "./channel-fields";
import { TelegramGroupOverrides } from "./telegram-group-overrides";
import { CHANNEL_TYPES } from "@/constants/channels";

interface ChannelInstanceFormDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  instance?: ChannelInstanceData | null;
  agents: AgentData[];
  onSubmit: (data: ChannelInstanceInput) => Promise<unknown>;
}

export function ChannelInstanceFormDialog({
  open,
  onOpenChange,
  instance,
  agents,
  onSubmit,
}: ChannelInstanceFormDialogProps) {
  const [name, setName] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [channelType, setChannelType] = useState("telegram");
  const [agentId, setAgentId] = useState("");
  const [credsValues, setCredsValues] = useState<Record<string, unknown>>({});
  const [configValues, setConfigValues] = useState<Record<string, unknown>>({});
  const [enabled, setEnabled] = useState(true);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (open) {
      setName(instance?.name ?? "");
      setDisplayName(instance?.display_name ?? "");
      setChannelType(instance?.channel_type ?? "telegram");
      setAgentId(instance?.agent_id ?? (agents[0]?.id ?? ""));
      setCredsValues({}); // never pre-fill credentials
      setConfigValues(instance?.config ? { ...instance.config } : {});
      setEnabled(instance?.enabled ?? true);
      setError("");
    }
  }, [open, instance, agents]);

  const handleCredsChange = useCallback((key: string, value: unknown) => {
    setCredsValues((prev) => ({ ...prev, [key]: value }));
  }, []);

  const handleConfigChange = useCallback((key: string, value: unknown) => {
    setConfigValues((prev) => ({ ...prev, [key]: value }));
  }, []);

  const handleSubmit = async () => {
    if (!name.trim()) {
      setError("Name is required");
      return;
    }
    if (!isValidSlug(name.trim())) {
      setError("Name must be a valid slug (lowercase letters, numbers, hyphens only)");
      return;
    }
    if (!agentId) {
      setError("Agent is required");
      return;
    }

    // Validate required credentials on create
    if (!instance) {
      const schema = credentialsSchema[channelType] ?? [];
      const missing = schema.filter((f) => f.required && !credsValues[f.key]);
      if (missing.length > 0) {
        setError(`Required: ${missing.map((f) => f.label).join(", ")}`);
        return;
      }
    }

    // Build clean config (strip undefined/empty values)
    const cleanConfig = Object.fromEntries(
      Object.entries(configValues).filter(([, v]) => v !== undefined && v !== "" && v !== null),
    );

    // Build credentials JSON from field values (only non-empty values)
    const cleanCreds = Object.fromEntries(
      Object.entries(credsValues).filter(([, v]) => v !== undefined && v !== "" && v !== null),
    );

    setLoading(true);
    setError("");
    try {
      const data: ChannelInstanceInput = {
        name: name.trim(),
        display_name: displayName.trim() || undefined,
        channel_type: channelType,
        agent_id: agentId,
        config: Object.keys(cleanConfig).length > 0 ? cleanConfig : undefined,
        enabled,
      };
      // Only send credentials if user entered something
      if (Object.keys(cleanCreds).length > 0) {
        data.credentials = cleanCreds;
      }
      await onSubmit(data);
      onOpenChange(false);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Failed to save");
    } finally {
      setLoading(false);
    }
  };

  const credsFields = credentialsSchema[channelType] ?? [];
  const cfgFields = configSchema[channelType] ?? [];

  return (
    <Dialog open={open} onOpenChange={(v) => !loading && onOpenChange(v)}>
      <DialogContent className="max-h-[85vh] max-w-lg flex flex-col">
        <DialogHeader>
          <DialogTitle>
            {instance ? "Edit Channel Instance" : "Create Channel Instance"}
          </DialogTitle>
        </DialogHeader>

        <div className="grid gap-4 py-2 overflow-y-auto min-h-0">
          {/* Basic fields */}
          <div className="grid gap-1.5">
            <Label htmlFor="ci-name">Name *</Label>
            <Input
              id="ci-name"
              value={name}
              onChange={(e) => setName(slugify(e.target.value))}
              placeholder="my-telegram-bot"
              disabled={!!instance}
            />
            <p className="text-xs text-muted-foreground">
              Unique slug used as channel identifier
            </p>
          </div>

          <div className="grid gap-1.5">
            <Label htmlFor="ci-display">Display Name</Label>
            <Input
              id="ci-display"
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
              placeholder="Sales Bot"
            />
          </div>

          <div className="grid gap-1.5">
            <Label>Channel Type *</Label>
            <Select value={channelType} onValueChange={setChannelType} disabled={!!instance}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CHANNEL_TYPES.map((ct) => (
                  <SelectItem key={ct.value} value={ct.value}>
                    {ct.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="grid gap-1.5">
            <Label>Agent *</Label>
            <Select value={agentId} onValueChange={setAgentId}>
              <SelectTrigger>
                <SelectValue placeholder="Select agent" />
              </SelectTrigger>
              <SelectContent>
                {agents.map((a) => (
                  <SelectItem key={a.id} value={a.id}>
                    {a.display_name || a.agent_key}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {/* Credentials section */}
          {credsFields.length > 0 && (
            <fieldset className="rounded-md border p-3 space-y-3">
              <legend className="px-1 text-sm font-medium">
                Credentials
                {instance && (
                  <span className="text-xs font-normal text-muted-foreground ml-1">
                    (leave blank to keep current)
                  </span>
                )}
              </legend>
              <ChannelFields
                fields={credsFields}
                values={credsValues}
                onChange={handleCredsChange}
                idPrefix="ci-cred"
                isEdit={!!instance}
              />
              <p className="text-xs text-muted-foreground">
                Encrypted server-side. Never returned in API responses.
              </p>
            </fieldset>
          )}

          {/* Config section */}
          {cfgFields.length > 0 && (
            <fieldset className="rounded-md border p-3 space-y-3">
              <legend className="px-1 text-sm font-medium">Configuration</legend>
              <ChannelFields
                fields={cfgFields}
                values={configValues}
                onChange={handleConfigChange}
                idPrefix="ci-cfg"
              />
            </fieldset>
          )}

          {/* Telegram group/topic overrides */}
          {channelType === "telegram" && (
            <TelegramGroupOverrides
              groups={(configValues.groups as Record<string, Record<string, unknown>>) ?? {}}
              onChange={(groups) => {
                setConfigValues((prev) => ({
                  ...prev,
                  groups: Object.keys(groups).length > 0 ? groups : undefined,
                }));
              }}
            />
          )}

          <div className="flex items-center gap-2">
            <Switch id="ci-enabled" checked={enabled} onCheckedChange={setEnabled} />
            <Label htmlFor="ci-enabled">Enabled</Label>
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={loading}>
            {loading ? "Saving..." : instance ? "Update" : "Create"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
