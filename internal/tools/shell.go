package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/sandbox"
)

// Dangerous command patterns to deny by default.
var defaultDenyPatterns = []*regexp.Regexp{
	// Destructive file operations
	regexp.MustCompile(`\brm\s+-[rf]{1,2}\b`),
	regexp.MustCompile(`\bdel\s+/[fq]\b`),
	regexp.MustCompile(`\brmdir\s+/s\b`),
	regexp.MustCompile(`\b(mkfs|diskpart)\b|\bformat\s`), // "format C:" but not "?format=3"
	regexp.MustCompile(`\bdd\s+if=`),
	regexp.MustCompile(`>\s*/dev/sd[a-z]\b`),
	regexp.MustCompile(`\b(shutdown|reboot|poweroff)\b`),
	regexp.MustCompile(`:\(\)\s*\{.*\};\s*:`), // fork bomb

	// Network exfiltration / remote code execution
	regexp.MustCompile(`\bcurl\b.*\|\s*(ba)?sh\b`),          // curl | sh
	regexp.MustCompile(`\bwget\b.*-O\s*-\s*\|\s*(ba)?sh\b`), // wget -O - | sh
	regexp.MustCompile(`/dev/tcp/`),                           // bash tcp redirect
	regexp.MustCompile(`\b(nc|ncat|netcat)\b.*-[el]\b`),      // reverse shell listeners

	// Dangerous eval from untrusted sources
	regexp.MustCompile(`\beval\s*\$`),                      // eval $(...)
	regexp.MustCompile(`\bbase64\s+-d\b.*\|\s*(ba)?sh\b`),  // base64 -d | sh

	// Bypass-resistant patterns (long flags)
	regexp.MustCompile(`\brm\s+.*--recursive`), // rm --recursive (bypasses rm -rf check)
	regexp.MustCompile(`\brm\s+.*--force`),     // rm --force

	// Privilege escalation
	regexp.MustCompile(`\bsudo\b`),    // sudo
	regexp.MustCompile(`\bsu\s+-`),    // su - (switch user)
	regexp.MustCompile(`\bnsenter\b`), // namespace escape
	regexp.MustCompile(`\bunshare\b`), // namespace manipulation

	// Dangerous path operations on root filesystem
	regexp.MustCompile(`\bchmod\s+[0-7]{3,4}\s+/`), // chmod 777 /...
	regexp.MustCompile(`\bchown\b.*\s+/`),           // chown ... /...

	// Environment variable injection
	regexp.MustCompile(`\bLD_PRELOAD\s*=`),               // Linux library injection
	regexp.MustCompile(`\bDYLD_INSERT_LIBRARIES\s*=`),     // macOS library injection
	regexp.MustCompile(`\bLD_LIBRARY_PATH\s*=`),           // library path hijack
}

// ExecTool executes shell commands, optionally inside a sandbox container.
type ExecTool struct {
	workingDir   string
	timeout      time.Duration
	denyPatterns []*regexp.Regexp
	restrict     bool
	sandboxMgr   sandbox.Manager        // nil = no sandbox, execute on host
	approvalMgr  *ExecApprovalManager   // nil = no approval needed
	agentID      string                  // for approval request context
}

// NewExecTool creates an exec tool that runs commands directly on the host.
func NewExecTool(workingDir string, restrict bool) *ExecTool {
	return &ExecTool{
		workingDir:   workingDir,
		timeout:      60 * time.Second,
		denyPatterns: defaultDenyPatterns,
		restrict:     restrict,
	}
}

// NewSandboxedExecTool creates an exec tool that routes commands through a sandbox container.
func NewSandboxedExecTool(workingDir string, restrict bool, mgr sandbox.Manager) *ExecTool {
	return &ExecTool{
		workingDir:   workingDir,
		timeout:      300 * time.Second, // sandbox allows longer timeout
		denyPatterns: defaultDenyPatterns,
		restrict:     restrict,
		sandboxMgr:   mgr,
	}
}

// SetSandboxKey is a no-op; sandbox key is now read from ctx (thread-safe).
func (t *ExecTool) SetSandboxKey(key string) {}

// SetApprovalManager sets the exec approval manager for this tool.
func (t *ExecTool) SetApprovalManager(mgr *ExecApprovalManager, agentID string) {
	t.approvalMgr = mgr
	t.agentID = agentID
}

func (t *ExecTool) Name() string        { return "exec" }
func (t *ExecTool) Description() string { return "Execute a shell command and return its output" }
func (t *ExecTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{
				"type":        "string",
				"description": "The shell command to execute",
			},
			"working_dir": map[string]interface{}{
				"type":        "string",
				"description": "Optional working directory for the command",
			},
		},
		"required": []string{"command"},
	}
}

func (t *ExecTool) Execute(ctx context.Context, args map[string]interface{}) *Result {
	command, _ := args["command"].(string)
	if command == "" {
		return ErrorResult("command is required")
	}

	// Check for dangerous commands (applies to both host and sandbox)
	for _, pattern := range t.denyPatterns {
		if pattern.MatchString(command) {
			return ErrorResult(fmt.Sprintf("command denied by safety policy: matches pattern %s", pattern.String()))
		}
	}

	// Exec approval check (matching TS exec-approval.ts pipeline)
	if t.approvalMgr != nil {
		switch t.approvalMgr.CheckCommand(command) {
		case "deny":
			return ErrorResult("command denied by exec approval policy")
		case "ask":
			decision, err := t.approvalMgr.RequestApproval(command, t.agentID, 2*time.Minute)
			if err != nil {
				return ErrorResult(fmt.Sprintf("exec approval: %v", err))
			}
			if decision == ApprovalDeny {
				return ErrorResult("command denied by user")
			}
		}
	}

	cwd := t.workingDir
	if wd, _ := args["working_dir"].(string); wd != "" {
		if t.restrict {
			resolved, err := resolvePath(wd, t.workingDir, true)
			if err != nil {
				return ErrorResult(err.Error())
			}
			cwd = resolved
		} else {
			cwd = wd
		}
	}

	// Sandbox routing (sandboxKey from ctx — thread-safe)
	sandboxKey := ToolSandboxKeyFromCtx(ctx)
	if t.sandboxMgr != nil && sandboxKey != "" {
		return t.executeInSandbox(ctx, command, cwd, sandboxKey)
	}

	// Host execution
	return t.executeOnHost(ctx, command, cwd)
}

// executeOnHost runs a command directly on the host (original behavior).
func (t *ExecTool) executeOnHost(ctx context.Context, command, cwd string) *Result {
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = cwd

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	var result string
	if stdout.Len() > 0 {
		result = stdout.String()
	}
	if stderr.Len() > 0 {
		if result != "" {
			result += "\n"
		}
		result += "STDERR:\n" + stderr.String()
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return ErrorResult(fmt.Sprintf("command timed out after %s", t.timeout))
		}
		if result == "" {
			result = err.Error()
		}
		return ErrorResult(result)
	}

	if result == "" {
		result = "(command completed with no output)"
	}

	return SilentResult(result)
}

// executeInSandbox routes a command through a Docker sandbox container.
func (t *ExecTool) executeInSandbox(ctx context.Context, command, cwd, sandboxKey string) *Result {
	sb, err := t.sandboxMgr.Get(ctx, sandboxKey, t.workingDir)
	if err != nil {
		if err == sandbox.ErrSandboxDisabled {
			return t.executeOnHost(ctx, command, cwd) // fallback to host
		}
		return ErrorResult(fmt.Sprintf("sandbox error: %v", err))
	}

	// Map host workdir to container workdir
	containerCwd := "/workspace"
	if cwd != t.workingDir {
		rel, relErr := filepath.Rel(t.workingDir, cwd)
		if relErr == nil {
			containerCwd = filepath.Join("/workspace", rel)
		}
	}

	result, err := sb.Exec(ctx, []string{"sh", "-c", command}, containerCwd)
	if err != nil {
		return ErrorResult(fmt.Sprintf("sandbox exec: %v", err))
	}

	// Format output same as host execution
	output := result.Stdout
	if result.Stderr != "" {
		if output != "" {
			output += "\n"
		}
		output += "STDERR:\n" + result.Stderr
	}
	if result.ExitCode != 0 {
		if output == "" {
			output = fmt.Sprintf("command exited with code %d", result.ExitCode)
		}
		return ErrorResult(output)
	}
	if output == "" {
		output = "(command completed with no output)"
	}

	return SilentResult(output)
}
