package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ReplayAction is a tool call extracted from a Claude session .jsonl.
type ReplayAction struct {
	Index   int
	Tool    string
	ID      string
	Input   map[string]any
	RawInput json.RawMessage
	Detail  string // human-readable summary of the input
}

// ReplayResult is the policy evaluation result for a single action.
type ReplayResult struct {
	Action   ReplayAction
	Decision string // ALLOW, DENY, ASK, ERROR
	Reason   string
}

// ReplaySession holds parsed session metadata.
type ReplaySession struct {
	Path       string
	Model      string
	Turns      int
	TokensIn   int64
	TokensOut  int64
	ToolCalls  int
	Actions    []ReplayAction
}

// ReplayReport is the full replay output.
type ReplayReport struct {
	Session    ReplaySession
	PolicyName string
	PolicyPath string
	Results    []ReplayResult
	AllowCount int
	DenyCount  int
	AskCount   int
}

// parseSessionFile parses a Claude .jsonl session file and extracts tool calls.
func parseSessionFile(path string) (*ReplaySession, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}
	defer f.Close()

	session := &ReplaySession{Path: path}
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024) // 10MB max line

	actionIdx := 0

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		msg, ok := entry["message"].(map[string]any)
		if !ok {
			continue
		}

		role, _ := msg["role"].(string)

		if role == "user" {
			session.Turns++
		}

		if role == "assistant" {
			// Extract model
			if m, ok := msg["model"].(string); ok && m != "" && m != "<synthetic>" {
				session.Model = m
			}

			// Extract usage
			if usage, ok := msg["usage"].(map[string]any); ok {
				if v, ok := usage["input_tokens"].(float64); ok {
					session.TokensIn += int64(v)
				}
				if v, ok := usage["output_tokens"].(float64); ok {
					session.TokensOut += int64(v)
				}
			}

			// Extract tool calls
			content, ok := msg["content"].([]any)
			if !ok {
				continue
			}
			for _, block := range content {
				b, ok := block.(map[string]any)
				if !ok {
					continue
				}
				if b["type"] != "tool_use" {
					continue
				}

				toolName, _ := b["name"].(string)
				toolID, _ := b["id"].(string)
				input, _ := b["input"].(map[string]any)

				rawInput, _ := json.Marshal(input)

				actionIdx++
				action := ReplayAction{
					Index:    actionIdx,
					Tool:     toolName,
					ID:       toolID,
					Input:    input,
					RawInput: rawInput,
					Detail:   extractDetail(toolName, input),
				}

				session.Actions = append(session.Actions, action)
			}
		}
	}

	session.ToolCalls = len(session.Actions)
	return session, nil
}

// extractDetail returns a human-readable summary of a tool call input.
func extractDetail(tool string, input map[string]any) string {
	switch tool {
	case "Read", "Write", "Edit":
		if fp, ok := input["file_path"].(string); ok {
			return fp
		}
	case "Bash":
		if cmd, ok := input["command"].(string); ok {
			if len(cmd) > 70 {
				return cmd[:70] + "..."
			}
			return cmd
		}
	case "Glob":
		if p, ok := input["pattern"].(string); ok {
			return p
		}
	case "Grep":
		if p, ok := input["pattern"].(string); ok {
			return p
		}
	case "Agent":
		if p, ok := input["prompt"].(string); ok {
			if len(p) > 60 {
				return p[:60] + "..."
			}
			return p
		}
	}

	data, _ := json.Marshal(input)
	s := string(data)
	if len(s) > 70 {
		return s[:70] + "..."
	}
	return s
}

// runReplay executes the replay: feeds each action through aflock's policy evaluator.
func runReplay(session *ReplaySession, policyPath string, aflockBin string) (*ReplayReport, error) {
	// Resolve policy
	absPolicy, err := filepath.Abs(policyPath)
	if err != nil {
		return nil, fmt.Errorf("resolve policy path: %w", err)
	}
	policyDir := filepath.Dir(absPolicy)

	// Read policy name
	policyName := "unknown"
	if data, err := os.ReadFile(absPolicy); err == nil {
		var pol struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(data, &pol) == nil && pol.Name != "" {
			policyName = pol.Name
		}
	}

	// Initialize session
	sessionID := fmt.Sprintf("replay-%d", time.Now().Unix())
	startInput, _ := json.Marshal(map[string]string{
		"cwd":        policyDir,
		"session_id": sessionID,
	})

	cmd := exec.Command(aflockBin, "hook", "SessionStart")
	cmd.Stdin = strings.NewReader(string(startInput))
	cmd.Run() // ignore errors on start

	report := &ReplayReport{
		Session:    *session,
		PolicyName: policyName,
		PolicyPath: absPolicy,
	}

	// Replay each action
	for _, action := range session.Actions {
		hookInput, _ := json.Marshal(map[string]any{
			"tool_name":   action.Tool,
			"tool_input":  action.Input,
			"tool_use_id": action.ID,
			"session_id":  sessionID,
			"cwd":         policyDir,
		})

		cmd := exec.Command(aflockBin, "hook", "PreToolUse")
		cmd.Stdin = strings.NewReader(string(hookInput))
		out, err := cmd.Output()

		result := ReplayResult{Action: action}

		if err != nil {
			result.Decision = "ERROR"
			result.Reason = err.Error()
		} else {
			var hookOut struct {
				HookSpecificOutput struct {
					PermissionDecision string `json:"permissionDecision"`
					UserFacingMessage  string `json:"userFacingMessage"`
				} `json:"hookSpecificOutput"`
			}
			if json.Unmarshal(out, &hookOut) == nil {
				switch hookOut.HookSpecificOutput.PermissionDecision {
				case "deny":
					result.Decision = "DENY"
					result.Reason = hookOut.HookSpecificOutput.UserFacingMessage
				case "ask":
					result.Decision = "ASK"
					result.Reason = hookOut.HookSpecificOutput.UserFacingMessage
				default:
					result.Decision = "ALLOW"
				}
			} else {
				result.Decision = "ALLOW"
			}
		}

		switch result.Decision {
		case "ALLOW":
			report.AllowCount++
		case "DENY":
			report.DenyCount++
		case "ASK":
			report.AskCount++
		}

		report.Results = append(report.Results, result)
	}

	// Cleanup replay session
	homeDir, _ := os.UserHomeDir()
	os.RemoveAll(filepath.Join(homeDir, ".aflock", "sessions", sessionID))

	return report, nil
}

// replayCLIOutput mirrors the JSON schema of `aflock replay --format json`.
type replayCLIOutput struct {
	Policy     string              `json:"policy"`
	PolicyPath string              `json:"policyPath"`
	Model      string              `json:"model"`
	Turns      int                 `json:"turns"`
	ToolCalls  int                 `json:"toolCalls"`
	TokensIn   int64               `json:"tokensIn"`
	TokensOut  int64               `json:"tokensOut"`
	AllowCount int                 `json:"allowCount"`
	DenyCount  int                 `json:"denyCount"`
	AskCount   int                 `json:"askCount"`
	Verdict    string              `json:"verdict"`
	Actions    []replayCLIAction   `json:"actions"`
}

type replayCLIAction struct {
	Index    int            `json:"index"`
	Tool     string         `json:"tool"`
	ID       string         `json:"id"`
	Input    map[string]any `json:"input"`
	Decision string         `json:"decision"`
	Reason   string         `json:"reason"`
}

// replayCLISupported checks whether `aflock replay` is available.
func replayCLISupported(aflockBin string) bool {
	cmd := exec.Command(aflockBin, "replay", "--help")
	out, err := cmd.CombinedOutput()
	_ = out
	return err == nil
}

// runReplayCLI runs `aflock replay --session <path> --policy <path> --format json`
// and converts the output into a ReplayReport.
func runReplayCLI(sessionPath, policyPath, aflockBin string) (*ReplayReport, error) {
	absSession, err := filepath.Abs(sessionPath)
	if err != nil {
		return nil, fmt.Errorf("resolve session path: %w", err)
	}
	absPolicy, err := filepath.Abs(policyPath)
	if err != nil {
		return nil, fmt.Errorf("resolve policy path: %w", err)
	}

	cmd := exec.Command(aflockBin, "replay",
		"--session", absSession,
		"--policy", absPolicy,
		"--format", "json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("aflock replay: %w", err)
	}

	var cliOut replayCLIOutput
	if err := json.Unmarshal(out, &cliOut); err != nil {
		return nil, fmt.Errorf("parse replay JSON: %w", err)
	}

	session := ReplaySession{
		Path:      absSession,
		Model:     cliOut.Model,
		Turns:     cliOut.Turns,
		TokensIn:  cliOut.TokensIn,
		TokensOut: cliOut.TokensOut,
		ToolCalls: cliOut.ToolCalls,
	}

	report := &ReplayReport{
		Session:    session,
		PolicyName: cliOut.Policy,
		PolicyPath: cliOut.PolicyPath,
		AllowCount: cliOut.AllowCount,
		DenyCount:  cliOut.DenyCount,
		AskCount:   cliOut.AskCount,
	}

	for _, a := range cliOut.Actions {
		rawInput, _ := json.Marshal(a.Input)
		action := ReplayAction{
			Index:    a.Index,
			Tool:     a.Tool,
			ID:       a.ID,
			Input:    a.Input,
			RawInput: rawInput,
			Detail:   extractDetail(a.Tool, a.Input),
		}
		session.Actions = append(session.Actions, action)

		decision := strings.ToUpper(a.Decision)
		result := ReplayResult{
			Action:   action,
			Decision: decision,
			Reason:   a.Reason,
		}
		report.Results = append(report.Results, result)
	}

	report.Session = session
	return report, nil
}

// findAflockBin locates the aflock binary.
func findAflockBin() string {
	// Check common locations
	candidates := []string{
		"/Users/rahulxf/work-dir/aflock/bin/aflock",
	}

	// Also check PATH
	if p, err := exec.LookPath("aflock"); err == nil {
		candidates = append([]string{p}, candidates...)
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}
