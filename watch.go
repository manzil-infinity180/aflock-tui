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

// watchState holds the incremental parsing state for watch mode.
type watchState struct {
	sessionPath string
	policyPath  string
	aflockBin   string
	lastOffset  int64
	actionIdx   int
	sessionID   string
	policyDir   string
	session     ReplaySession
}

// newWatchState creates a new watch state and initializes the aflock session.
func newWatchState(sessionPath, policyPath, aflockBin string) (*watchState, error) {
	absPolicy, err := filepath.Abs(policyPath)
	if err != nil {
		return nil, fmt.Errorf("resolve policy path: %w", err)
	}
	policyDir := filepath.Dir(absPolicy)

	sessionID := fmt.Sprintf("watch-%d", time.Now().Unix())

	// Initialize aflock session
	startInput, _ := json.Marshal(map[string]string{
		"cwd":        policyDir,
		"session_id": sessionID,
	})
	cmd := exec.Command(aflockBin, "hook", "SessionStart")
	cmd.Stdin = strings.NewReader(string(startInput))
	cmd.Run() // best-effort

	return &watchState{
		sessionPath: sessionPath,
		policyPath:  policyPath,
		aflockBin:   aflockBin,
		lastOffset:  0,
		actionIdx:   0,
		sessionID:   sessionID,
		policyDir:   policyDir,
		session: ReplaySession{
			Path: sessionPath,
		},
	}, nil
}

// watchParseNew reads new lines from the session file starting at lastOffset,
// extracts tool_use blocks, and returns new ReplayActions along with updated session metadata.
func (ws *watchState) watchParseNew() ([]ReplayAction, error) {
	f, err := os.Open(ws.sessionPath)
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}
	defer f.Close()

	if _, err := f.Seek(ws.lastOffset, 0); err != nil {
		return nil, fmt.Errorf("seek: %w", err)
	}

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var newActions []ReplayAction

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
			ws.session.Turns++
		}

		if role == "assistant" {
			// Extract model
			if m, ok := msg["model"].(string); ok && m != "" && m != "<synthetic>" {
				ws.session.Model = m
			}

			// Extract usage
			if usage, ok := msg["usage"].(map[string]any); ok {
				if v, ok := usage["input_tokens"].(float64); ok {
					ws.session.TokensIn += int64(v)
				}
				if v, ok := usage["output_tokens"].(float64); ok {
					ws.session.TokensOut += int64(v)
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

				ws.actionIdx++
				action := ReplayAction{
					Index:    ws.actionIdx,
					Tool:     toolName,
					ID:       toolID,
					Input:    input,
					RawInput: rawInput,
					Detail:   extractDetail(toolName, input),
				}
				newActions = append(newActions, action)
			}
		}
	}

	// Update offset to current end of file
	pos, err := f.Seek(0, 2) // seek to end
	if err == nil {
		ws.lastOffset = pos
	}

	ws.session.ToolCalls += len(newActions)
	ws.session.Actions = append(ws.session.Actions, newActions...)

	return newActions, nil
}

// watchEvaluateAction evaluates a single action against the policy using aflock hook PreToolUse.
func (ws *watchState) watchEvaluateAction(action ReplayAction) ReplayResult {
	hookInput, _ := json.Marshal(map[string]any{
		"tool_name":   action.Tool,
		"tool_input":  action.Input,
		"tool_use_id": action.ID,
		"session_id":  ws.sessionID,
		"cwd":         ws.policyDir,
	})

	cmd := exec.Command(ws.aflockBin, "hook", "PreToolUse")
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

	return result
}

// cleanup removes the watch session directory.
func (ws *watchState) cleanup() {
	homeDir, _ := os.UserHomeDir()
	os.RemoveAll(filepath.Join(homeDir, ".aflock", "sessions", ws.sessionID))
}

// policyName reads the policy name from the policy file.
func (ws *watchState) policyName() string {
	absPolicy, err := filepath.Abs(ws.policyPath)
	if err != nil {
		return "unknown"
	}
	data, err := os.ReadFile(absPolicy)
	if err != nil {
		return "unknown"
	}
	var pol struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(data, &pol) == nil && pol.Name != "" {
		return pol.Name
	}
	return filepath.Base(ws.policyPath)
}

// watchPollAndEvaluate reads new actions and evaluates them. Returns new results.
func (ws *watchState) watchPollAndEvaluate() ([]ReplayResult, error) {
	newActions, err := ws.watchParseNew()
	if err != nil {
		return nil, fmt.Errorf("parse new: %w", err)
	}

	var results []ReplayResult
	for _, action := range newActions {
		result := ws.watchEvaluateAction(action)
		results = append(results, result)
	}

	return results, nil
}
