import { useState } from "react";
import { Sparkles } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";

interface RegenerateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onRegenerate: (prompt: string) => Promise<void>;
}

export function RegenerateDialog({
  open,
  onOpenChange,
  onRegenerate,
}: RegenerateDialogProps) {
  const [prompt, setPrompt] = useState("");
  const [regenerating, setRegenerating] = useState(false);

  const handleSubmit = async () => {
    if (!prompt.trim()) return;
    setRegenerating(true);
    try {
      await onRegenerate(prompt.trim());
      onOpenChange(false);
      setPrompt("");
    } finally {
      setRegenerating(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Sparkles className="h-4 w-4" />
            Edit with AI
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-3 py-2">
          <p className="text-sm text-muted-foreground">
            Describe what you want to change. AI will read the current files and
            update them accordingly.
          </p>
          <Textarea
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            placeholder="e.g. Make the agent more formal, add Vietnamese language support, change the name to Luna..."
            className="min-h-[100px] max-h-[300px] resize-none"
          />
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={regenerating}
          >
            Cancel
          </Button>
          <Button
            onClick={handleSubmit}
            disabled={!prompt.trim() || regenerating}
            className="gap-1.5"
          >
            <Sparkles className="h-3.5 w-3.5" />
            {regenerating ? "Sending..." : "Regenerate"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
