package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SessionInfo holds metadata about a session directory.
type SessionInfo struct {
	ID          string
	Dir         string
	ModTime     time.Time
	Size        int64 // total bytes across all files
	FileCount   int
	AttestCount int
	HasJWT      bool
	// Preview cache (lazy loaded)
	Preview *SessionPreview
}

// SessionPreview is a lightweight summary for the session list preview bar.
type SessionPreview struct {
	PolicyName string
	ToolCalls  int
	AllowCount int
	DenyCount  int
	CostUSD    float64
	Tools      map[string]int
}

// SessionState is the parsed state.json.
type SessionState struct {
	SessionID string          `json:"session_id"`
	StartedAt time.Time       `json:"started_at"`
	Policy    *Policy         `json:"policy,omitempty"`
	PolicyPath string         `json:"policy_path,omitempty"`
	Metrics   *SessionMetrics `json:"metrics,omitempty"`
	Actions   []ActionRecord  `json:"actions,omitempty"`
	AuthToken string          `json:"auth_token,omitempty"`
	Identity  *IdentityMeta   `json:"agent_identity_meta,omitempty"`
}

type Policy struct {
	Version string       `json:"version"`
	Name    string       `json:"name"`
	Limits  *Limits      `json:"limits,omitempty"`
	Tools   *ToolsPolicy `json:"tools,omitempty"`
	Files   *FilesPolicy `json:"files,omitempty"`
}

type Limits struct {
	MaxSpendUSD *LimitValue `json:"maxSpendUSD,omitempty"`
	MaxTurns    *LimitValue `json:"maxTurns,omitempty"`
}

type LimitValue struct {
	Value       float64 `json:"value"`
	Enforcement string  `json:"enforcement"`
}

type ToolsPolicy struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

type FilesPolicy struct {
	Allow    []string `json:"allow,omitempty"`
	Deny     []string `json:"deny,omitempty"`
	ReadOnly []string `json:"readOnly,omitempty"`
}

type SessionMetrics struct {
	TokensIn     int64          `json:"tokensIn"`
	TokensOut    int64          `json:"tokensOut"`
	CostUSD      float64        `json:"costUSD"`
	Turns        int            `json:"turns"`
	ToolCalls    int            `json:"toolCalls"`
	Tools        map[string]int `json:"tools,omitempty"`
	FilesRead    []string       `json:"filesRead,omitempty"`
	FilesWritten []string       `json:"filesWritten,omitempty"`
}

type ActionRecord struct {
	Timestamp time.Time       `json:"timestamp"`
	ToolName  string          `json:"tool_name"`
	ToolUseID string          `json:"tool_use_id"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
	Decision  string          `json:"decision"`
	Reason    string          `json:"reason,omitempty"`
}

type IdentityMeta struct {
	Model         string `json:"model"`
	ModelVersion  string `json:"model_version"`
	BinaryName    string `json:"binary_name"`
	BinaryVersion string `json:"binary_version"`
	BinaryDigest  string `json:"binary_digest"`
	Environment   string `json:"environment"`
	IdentityHash  string `json:"identity_hash"`
}

// DSSEEnvelope is a DSSE signed envelope.
type DSSEEnvelope struct {
	PayloadType string          `json:"payloadType"`
	Payload     string          `json:"payload"`
	Signatures  []DSSESignature `json:"signatures"`
}

type DSSESignature struct {
	KeyID       string `json:"keyid,omitempty"`
	Sig         string `json:"sig"`
	Certificate string `json:"certificate,omitempty"`
}

// InTotoStatement is the decoded payload.
type InTotoStatement struct {
	Type          string            `json:"_type"`
	Subject       []InTotoSubject   `json:"subject"`
	PredicateType string            `json:"predicateType"`
	Predicate     json.RawMessage   `json:"predicate"`
}

type InTotoSubject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// ActionPredicate is the aflock-specific predicate.
type ActionPredicate struct {
	Action        string          `json:"action"`
	SessionID     string          `json:"sessionId"`
	ToolName      string          `json:"toolName"`
	ToolUseID     string          `json:"toolUseId"`
	ToolInput     json.RawMessage `json:"toolInput,omitempty"`
	Decision      string          `json:"decision"`
	Timestamp     time.Time       `json:"timestamp"`
	AgentID       string          `json:"agentId,omitempty"`
	AgentIdentity json.RawMessage `json:"agentIdentity,omitempty"`
	Metrics       json.RawMessage `json:"metrics,omitempty"`
}

// AttestationInfo holds parsed attestation data.
type AttestationInfo struct {
	Filename  string
	Path      string
	ModTime   time.Time
	Size      int64
	Envelope  *DSSEEnvelope
	Statement *InTotoStatement
	Predicate *ActionPredicate
	RawJSON   string
	Decoded   string // pretty-printed decoded payload
}

func sessionsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".aflock", "sessions")
}

func loadSessions() ([]SessionInfo, error) {
	dir := sessionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read sessions dir: %w", err)
	}

	var sessions []SessionInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionDir := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		si := SessionInfo{
			ID:      entry.Name(),
			Dir:     sessionDir,
			ModTime: info.ModTime(),
		}

		// Walk files for size/count
		filepath.Walk(sessionDir, func(path string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() {
				return nil
			}
			si.Size += fi.Size()
			si.FileCount++
			if strings.HasSuffix(fi.Name(), ".intoto.json") {
				si.AttestCount++
			}
			// Update modtime to most recent file
			if fi.ModTime().After(si.ModTime) {
				si.ModTime = fi.ModTime()
			}
			return nil
		})

		// Quick check for JWT
		stateData, err := os.ReadFile(filepath.Join(sessionDir, "state.json"))
		if err == nil {
			si.HasJWT = strings.Contains(string(stateData), `"auth_token"`)
		}

		sessions = append(sessions, si)
	}

	// Sort by modification time, newest first
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ModTime.After(sessions[j].ModTime)
	})

	return sessions, nil
}

func loadSessionPreview(sessionDir string) *SessionPreview {
	data, err := os.ReadFile(filepath.Join(sessionDir, "state.json"))
	if err != nil {
		return nil
	}
	var state SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}
	p := &SessionPreview{}
	if state.Policy != nil {
		p.PolicyName = state.Policy.Name
	}
	if state.Metrics != nil {
		p.ToolCalls = state.Metrics.ToolCalls
		p.CostUSD = state.Metrics.CostUSD
		p.Tools = state.Metrics.Tools
	}
	for _, a := range state.Actions {
		if a.Decision == "allow" {
			p.AllowCount++
		} else {
			p.DenyCount++
		}
	}
	return p
}

func loadSessionState(sessionDir string) (*SessionState, error) {
	data, err := os.ReadFile(filepath.Join(sessionDir, "state.json"))
	if err != nil {
		return nil, err
	}
	var state SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func loadAttestations(sessionDir string) ([]AttestationInfo, error) {
	attestDir := filepath.Join(sessionDir, "attestations")
	entries, err := os.ReadDir(attestDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var attestations []AttestationInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".intoto.json") {
			continue
		}

		path := filepath.Join(attestDir, entry.Name())
		info, _ := entry.Info()

		ai := AttestationInfo{
			Filename: entry.Name(),
			Path:     path,
			Size:     info.Size(),
			ModTime:  info.ModTime(),
		}

		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		ai.RawJSON = string(data)

		// Parse DSSE envelope
		var envelope DSSEEnvelope
		if err := json.Unmarshal(data, &envelope); err == nil {
			ai.Envelope = &envelope

			// Decode payload
			payloadBytes, err := base64.StdEncoding.DecodeString(envelope.Payload)
			if err == nil {
				// Pretty-print the decoded payload
				var pretty json.RawMessage
				if json.Unmarshal(payloadBytes, &pretty) == nil {
					prettyBytes, _ := json.MarshalIndent(pretty, "", "  ")
					ai.Decoded = string(prettyBytes)
				}

				// Parse in-toto statement
				var stmt InTotoStatement
				if json.Unmarshal(payloadBytes, &stmt) == nil {
					ai.Statement = &stmt

					// Parse action predicate
					var pred ActionPredicate
					if json.Unmarshal(stmt.Predicate, &pred) == nil {
						ai.Predicate = &pred
					}
				}
			}
		}

		attestations = append(attestations, ai)
	}

	// Sort by mod time
	sort.Slice(attestations, func(i, j int) bool {
		return attestations[i].ModTime.Before(attestations[j].ModTime)
	})

	return attestations, nil
}

func decodeJWT(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid JWT: expected 3 parts, got %d", len(parts))
	}

	// Decode header
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("decode header: %w", err)
	}
	var header json.RawMessage
	json.Unmarshal(headerBytes, &header)
	headerPretty, _ := json.MarshalIndent(header, "", "  ")

	// Decode payload
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode payload: %w", err)
	}
	var payload json.RawMessage
	json.Unmarshal(payloadBytes, &payload)
	payloadPretty, _ := json.MarshalIndent(payload, "", "  ")

	sigPreview := parts[2]
	if len(sigPreview) > 40 {
		sigPreview = sigPreview[:40]
	}

	return fmt.Sprintf("  HEADER\n%s\n\n  CLAIMS\n%s\n\n  SIGNATURE\n  %s...",
		colorizeJSON(string(headerPretty)),
		colorizeJSON(string(payloadPretty)),
		sigPreview), nil
}

func humanSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fK", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fM", float64(bytes)/(1024*1024))
}

