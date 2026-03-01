import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { InfoLabel } from "./config-section";

const THINKING_LEVELS = [
  { value: "off", label: "Off", description: "No extended thinking" },
  { value: "low", label: "Low", description: "~4K token budget" },
  { value: "medium", label: "Medium", description: "~10-16K token budget" },
  { value: "high", label: "High", description: "~32K token budget" },
] as const;

interface ThinkingSectionProps {
  value: string;
  onChange: (v: string) => void;
}

export function ThinkingSection({ value, onChange }: ThinkingSectionProps) {
  return (
    <section className="space-y-3">
      <div>
        <h3 className="text-sm font-medium">Extended Thinking</h3>
        <p className="text-xs text-muted-foreground">
          Allow the model to reason before responding. Higher levels use more
          tokens but produce better results on complex tasks.
        </p>
      </div>
      <div className="space-y-2">
        <InfoLabel tip="Thinking level controls the token budget for reasoning. Anthropic uses budget_tokens, OpenAI uses reasoning_effort, DashScope uses thinking_budget. Token budgets vary by provider.">
          Thinking Level
        </InfoLabel>
        <Select value={value || "off"} onValueChange={onChange}>
          <SelectTrigger className="w-48">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {THINKING_LEVELS.map((level) => (
              <SelectItem key={level.value} value={level.value}>
                <span>{level.label}</span>
                <span className="ml-2 text-xs text-muted-foreground">
                  {level.description}
                </span>
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    </section>
  );
}
