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
	// MCP-path sessions don't write agent_identity_meta into state.json.
	// Fall back in order: latest attestation's predicate.agentIdentity,
	// then JWT body claims. This way the inspect view shows identity for
	// any session that has signed evidence of it, even if claude only
	// called get_token and never produced an attestation.
	if state.Identity == nil {
		if id := identityFromLatestAttestation(sessionDir); id != nil {
			state.Identity = id
		}
	}
	if state.Identity == nil && state.AuthToken != "" {
		if id := identityFromJWT(state.AuthToken); id != nil {
			state.Identity = id
		}
	}
	return &state, nil
}

// jwtClaims captures the aflock-specific claims we surface in the inspect view.
// Other claims (allowed_tools, limits, etc.) stay accessible via "press t".
type jwtClaims struct {
	Sub          string `json:"sub"`
	AgentID      string `json:"agent_id"`
	IdentityHash string `json:"identity_hash"`
}

// identityFromJWT parses the body of an unverified JWT and returns the
// identity it asserts. We don't verify the signature here — the TUI is a
// read-only viewer; the verifier is what actually trusts the token.
//
// The aflock JWT subject follows the SPIFFE convention:
//
//	spiffe://aflock.ai/agent/<model>/<model-version-dashed>/<short-hash>
//
// e.g. spiffe://aflock.ai/agent/claude-opus-4-7/4-7-0/b4d15ed93f404e8b
//
// We parse Model and ModelVersion out of that path so the inspect view
// can render them the same way it would for an attestation-derived identity.
func identityFromJWT(token string) *IdentityMeta {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Some emitters pad with '='; try the padded variant.
		body, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil
		}
	}
	var c jwtClaims
	if err := json.Unmarshal(body, &c); err != nil {
		return nil
	}
	if c.IdentityHash == "" && c.Sub == "" && c.AgentID == "" {
		return nil
	}

	id := &IdentityMeta{IdentityHash: c.IdentityHash}

	// Prefer sub, fall back to agent_id (they are the same in aflock today).
	spiffeID := c.Sub
	if spiffeID == "" {
		spiffeID = c.AgentID
	}
	// Format: spiffe://aflock.ai/agent/<model>/<modelVersion>/<shortHash>
	const prefix = "spiffe://aflock.ai/agent/"
	if rest, ok := strings.CutPrefix(spiffeID, prefix); ok {
		segs := strings.Split(rest, "/")
		if len(segs) >= 1 {
			id.Model = segs[0]
		}
		if len(segs) >= 2 {
			// SPIFFE path segments can't contain '.', so aflock encodes
			// "4.7.0" as "4-7-0". Reverse for display.
			id.ModelVersion = strings.ReplaceAll(segs[1], "-", ".")
		}
	}
	return id
}

// attestationAgentIdentity matches the on-wire predicate.agentIdentity shape
// (camelCase, single binary path field, no separate binary_version).
type attestationAgentIdentity struct {
	Model        string `json:"model"`
	ModelVersion string `json:"modelVersion"`
	Binary       string `json:"binary"`
	BinaryHash   string `json:"binaryHash"`
	Environment  string `json:"environment"`
	IdentityHash string `json:"identityHash"`
}

// identityFromLatestAttestation walks the session's attestations dir, picks
// the most recently modified .intoto.json, decodes its DSSE payload, and
// returns the agent identity carried in the predicate. Returns nil if no
// attestations exist or none parse cleanly.
func identityFromLatestAttestation(sessionDir string) *IdentityMeta {
	attestDir := filepath.Join(sessionDir, "attestations")
	entries, err := os.ReadDir(attestDir)
	if err != nil {
		return nil
	}

	var newestPath string
	var newestMtime time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".intoto.json") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if fi.ModTime().After(newestMtime) {
			newestMtime = fi.ModTime()
			newestPath = filepath.Join(attestDir, e.Name())
		}
	}
	if newestPath == "" {
		return nil
	}

	data, err := os.ReadFile(newestPath)
	if err != nil {
		return nil
	}

	var envelope DSSEEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil
	}
	payloadBytes, err := base64.StdEncoding.DecodeString(envelope.Payload)
	if err != nil {
		return nil
	}
	var stmt InTotoStatement
	if err := json.Unmarshal(payloadBytes, &stmt); err != nil {
		return nil
	}
	var pred ActionPredicate
	if err := json.Unmarshal(stmt.Predicate, &pred); err != nil {
		return nil
	}
	if len(pred.AgentIdentity) == 0 {
		return nil
	}
	var aid attestationAgentIdentity
	if err := json.Unmarshal(pred.AgentIdentity, &aid); err != nil {
		return nil
	}
	if aid.IdentityHash == "" && aid.Model == "" && aid.Binary == "" {
		return nil
	}
	return &IdentityMeta{
		Model:        aid.Model,
		ModelVersion: aid.ModelVersion,
		BinaryName:   aid.Binary,
		BinaryDigest: aid.BinaryHash,
		Environment:  aid.Environment,
		IdentityHash: aid.IdentityHash,
	}
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

