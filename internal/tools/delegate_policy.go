package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nextlevelbuilder/goclaw/internal/hooks"
)

func checkUserPermission(settings json.RawMessage, userID string) error {
	if len(settings) == 0 || string(settings) == "{}" {
		return nil
	}
	var s linkSettings
	if json.Unmarshal(settings, &s) != nil {
		return nil // malformed = fail open
	}
	for _, denied := range s.UserDeny {
		if denied == userID {
			return fmt.Errorf("you are not authorized to use this delegation link")
		}
	}
	if len(s.UserAllow) > 0 {
		for _, allowed := range s.UserAllow {
			if allowed == userID {
				return nil
			}
		}
		return fmt.Errorf("you are not authorized to use this delegation link")
	}
	return nil
}

func parseMaxDelegationLoad(otherConfig json.RawMessage) int {
	if len(otherConfig) == 0 {
		return defaultMaxDelegationLoad
	}
	var cfg struct {
		MaxDelegationLoad int `json:"max_delegation_load"`
	}
	if json.Unmarshal(otherConfig, &cfg) != nil || cfg.MaxDelegationLoad <= 0 {
		return defaultMaxDelegationLoad
	}
	return cfg.MaxDelegationLoad
}

func parseQualityGates(otherConfig json.RawMessage) []hooks.HookConfig {
	if len(otherConfig) == 0 {
		return nil
	}
	var cfg struct {
		QualityGates []hooks.HookConfig `json:"quality_gates"`
	}
	if json.Unmarshal(otherConfig, &cfg) != nil {
		return nil
	}
	return cfg.QualityGates
}

// applyQualityGates evaluates quality gates on a delegation result.
// Returns the (possibly revised) result. If a blocking gate fails after all retries,
// returns the last result anyway with a logged warning (does not hard-fail the delegation).
// Only returns error on catastrophic failures (e.g. context cancelled).
func (dm *DelegateManager) applyQualityGates(
	ctx context.Context, task *DelegationTask, opts DelegateOpts,
	result *DelegateRunResult,
) (*DelegateRunResult, error) {
	if dm.hookEngine == nil || hooks.SkipHooksFromContext(ctx) {
		return result, nil
	}

	sourceAgent, err := dm.agentStore.GetByID(ctx, task.SourceAgentID)
	if err != nil || sourceAgent == nil {
		return result, nil
	}

	gates := parseQualityGates(sourceAgent.OtherConfig)
	if len(gates) == 0 {
		return result, nil
	}

	hctx := hooks.HookContext{
		Event:          "delegation.completed",
		SourceAgentKey: task.SourceAgentKey,
		TargetAgentKey: task.TargetAgentKey,
		UserID:         task.UserID,
		Content:        result.Content,
		Task:           opts.Task,
	}

	for _, gate := range gates {
		if gate.Event != "delegation.completed" {
			continue
		}

		currentResult := result
		retries := gate.MaxRetries

		for attempt := 0; attempt <= retries; attempt++ {
			hctx.Content = currentResult.Content

			hookResult, evalErr := dm.hookEngine.EvaluateSingleHook(ctx, gate, hctx)
			if evalErr != nil {
				slog.Warn("quality_gate: evaluator error, skipping",
					"type", gate.Type, "delegation", task.ID, "error", evalErr)
				break
			}

			if hookResult.Passed {
				result = currentResult
				break
			}

			// Gate failed
			if !gate.BlockOnFailure {
				slog.Warn("quality_gate: non-blocking gate failed",
					"type", gate.Type, "delegation", task.ID)
				break
			}

			if attempt >= retries {
				slog.Warn("quality_gate: max retries exceeded, accepting result",
					"type", gate.Type, "delegation", task.ID, "retries", retries)
				result = currentResult
				break
			}

			// Retry: re-run target agent with feedback
			slog.Info("quality_gate: retrying delegation",
				"type", gate.Type, "delegation", task.ID,
				"attempt", attempt+1, "max_retries", retries)

			feedbackMsg := fmt.Sprintf(
				"[Quality Gate Feedback — Retry %d/%d]\n"+
					"Your previous output did not pass quality review.\n\n"+
					"Feedback: %s\n\n"+
					"Original task: %s\n\n"+
					"Please revise your output addressing the feedback.",
				attempt+1, retries, hookResult.Feedback, opts.Task)

			rerunResult, rerunErr := dm.runAgent(ctx, opts.TargetAgentKey, dm.buildRunRequest(task, feedbackMsg))
			if rerunErr != nil {
				slog.Warn("quality_gate: retry run failed, accepting previous result",
					"delegation", task.ID, "error", rerunErr)
				result = currentResult
				break
			}
			currentResult = rerunResult
			result = currentResult
		}
	}

	return result, nil
}
