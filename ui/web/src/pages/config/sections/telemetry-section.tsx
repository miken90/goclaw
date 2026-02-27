import { useState, useEffect } from "react";
import { Save } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { InfoLabel } from "@/components/shared/info-label";
import { KeyValueEditor } from "@/components/shared/key-value-editor";

interface TelemetryData {
  enabled?: boolean;
  endpoint?: string;
  protocol?: string;
  insecure?: boolean;
  service_name?: string;
  headers?: Record<string, string>;
}

const DEFAULT: TelemetryData = {};

interface Props {
  data: TelemetryData | undefined;
  onSave: (value: TelemetryData) => Promise<void>;
  saving: boolean;
}

export function TelemetrySection({ data, onSave, saving }: Props) {
  const [draft, setDraft] = useState<TelemetryData>(data ?? DEFAULT);
  const [headers, setHeaders] = useState<Record<string, string>>({});
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    setDraft(data ?? DEFAULT);
    setHeaders(data?.headers ?? {});
    setDirty(false);
  }, [data]);

  const update = (patch: Partial<TelemetryData>) => {
    setDraft((prev) => ({ ...prev, ...patch }));
    setDirty(true);
  };

  const handleSave = () => {
    onSave({ ...draft, headers: Object.keys(headers).length > 0 ? headers : undefined });
  };

  if (!data) return null;

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="text-base">Telemetry</CardTitle>
        <CardDescription>OpenTelemetry export configuration</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex items-center justify-between">
          <InfoLabel tip="Enable OpenTelemetry export of LLM call traces and spans.">Enabled</InfoLabel>
          <Switch checked={draft.enabled ?? false} onCheckedChange={(v) => update({ enabled: v })} />
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="grid gap-1.5">
            <InfoLabel tip="OTel collector endpoint address (host:port). E.g. localhost:4317 for gRPC or localhost:4318 for HTTP.">Endpoint</InfoLabel>
            <Input
              value={draft.endpoint ?? ""}
              onChange={(e) => update({ endpoint: e.target.value })}
              placeholder="localhost:4317"
            />
          </div>
          <div className="grid gap-1.5">
            <InfoLabel tip="Transport protocol for exporting traces. gRPC is recommended for most setups.">Protocol</InfoLabel>
            <Select value={draft.protocol ?? "grpc"} onValueChange={(v) => update({ protocol: v })}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="grpc">gRPC</SelectItem>
                <SelectItem value="http">HTTP</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="grid gap-1.5">
            <InfoLabel tip="Service name reported in trace spans. Useful for distinguishing multiple gateway instances.">Service Name</InfoLabel>
            <Input
              value={draft.service_name ?? ""}
              onChange={(e) => update({ service_name: e.target.value })}
              placeholder="goclaw-gateway"
            />
          </div>
          <div className="flex items-center justify-between">
            <InfoLabel tip="Disable TLS for the connection to the OTel collector. Use only for local/trusted networks.">Insecure (no TLS)</InfoLabel>
            <Switch checked={draft.insecure ?? false} onCheckedChange={(v) => update({ insecure: v })} />
          </div>
        </div>

        <div className="grid gap-1.5">
          <InfoLabel tip="Additional HTTP headers sent with each export request. Useful for authentication tokens or routing metadata.">Headers</InfoLabel>
          <KeyValueEditor
            value={headers}
            onChange={(v) => { setHeaders(v); setDirty(true); }}
            keyPlaceholder="Header name"
            valuePlaceholder="Header value"
            addLabel="Add Header"
          />
        </div>

        {dirty && (
          <div className="flex justify-end pt-2">
            <Button size="sm" onClick={handleSave} disabled={saving} className="gap-1.5">
              <Save className="h-3.5 w-3.5" /> {saving ? "Saving..." : "Save"}
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
