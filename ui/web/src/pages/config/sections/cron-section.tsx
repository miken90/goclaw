import { useState, useEffect } from "react";
import { Save } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { InfoLabel } from "@/components/shared/info-label";

interface CronData {
  max_retries?: number;
  retry_base_delay?: string;
  retry_max_delay?: string;
}

const DEFAULT: CronData = {};

interface Props {
  data: CronData | undefined;
  onSave: (value: CronData) => Promise<void>;
  saving: boolean;
}

export function CronSection({ data, onSave, saving }: Props) {
  const [draft, setDraft] = useState<CronData>(data ?? DEFAULT);
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    setDraft(data ?? DEFAULT);
    setDirty(false);
  }, [data]);

  const update = (patch: Partial<CronData>) => {
    setDraft((prev) => ({ ...prev, ...patch }));
    setDirty(true);
  };

  if (!data) return null;

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="text-base">Cron</CardTitle>
        <CardDescription>Cron job retry settings</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid grid-cols-3 gap-4">
          <div className="grid gap-1.5">
            <InfoLabel tip="Maximum number of retry attempts for failed cron jobs before giving up.">Max Retries</InfoLabel>
            <Input
              type="number"
              value={draft.max_retries ?? ""}
              onChange={(e) => update({ max_retries: Number(e.target.value) })}
              placeholder="3"
              min={0}
            />
          </div>
          <div className="grid gap-1.5">
            <InfoLabel tip="Initial delay between retries with exponential backoff. Go duration format (e.g. 2s, 500ms).">Base Delay</InfoLabel>
            <Input
              value={draft.retry_base_delay ?? ""}
              onChange={(e) => update({ retry_base_delay: e.target.value })}
              placeholder="2s"
            />
          </div>
          <div className="grid gap-1.5">
            <InfoLabel tip="Maximum delay cap for exponential backoff. Retries won't wait longer than this. Go duration format.">Max Delay</InfoLabel>
            <Input
              value={draft.retry_max_delay ?? ""}
              onChange={(e) => update({ retry_max_delay: e.target.value })}
              placeholder="30s"
            />
          </div>
        </div>

        {dirty && (
          <div className="flex justify-end pt-2">
            <Button size="sm" onClick={() => onSave(draft)} disabled={saving} className="gap-1.5">
              <Save className="h-3.5 w-3.5" /> {saving ? "Saving..." : "Save"}
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
