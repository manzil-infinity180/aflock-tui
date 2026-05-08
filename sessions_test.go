package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIdentityFromLatestAttestation_RealAttestation parses a real on-disk
// attestation (captured from a live aflock+SPIRE session on macOS) and asserts
// that the agent identity is extracted and shaped correctly. This is the path
// the inspect view falls back on when state.json lacks agent_identity_meta
// (the MCP-path session shape).
func TestIdentityFromLatestAttestation_RealAttestation(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "attestations"), 0o755); err != nil {
		t.Fatal(err)
	}

	src, err := os.ReadFile("/tmp/sample-attestation.intoto.json")
	if err != nil {
		t.Skipf("no fixture at /tmp/sample-attestation.intoto.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "attestations", "sample.intoto.json"), src, 0o644); err != nil {
		t.Fatal(err)
	}

	id := identityFromLatestAttestation(tmp)
	if id == nil {
		t.Fatal("identityFromLatestAttestation returned nil for a real attestation")
	}
	if id.IdentityHash == "" {
		t.Error("expected IdentityHash to be populated")
	}
	if id.Model == "" {
		t.Error("expected Model to be populated")
	}
	if id.BinaryDigest == "" {
		t.Error("expected BinaryDigest (binaryHash) to be populated")
	}
	t.Logf("Model=%s ModelVersion=%s Binary=%s Env=%s Hash=%s",
		id.Model, id.ModelVersion, id.BinaryName, id.Environment, id.IdentityHash)
}

// TestIdentityFromLatestAttestation_MissingDir returns nil instead of erroring
// when there's no attestations directory (e.g. brand-new session).
func TestIdentityFromLatestAttestation_MissingDir(t *testing.T) {
	if got := identityFromLatestAttestation(t.TempDir()); got != nil {
		t.Errorf("expected nil for empty session, got %+v", got)
	}
}

// TestIdentityFromLatestAttestation_EmptyDir returns nil when attestations
// directory exists but contains no .intoto.json files.
func TestIdentityFromLatestAttestation_EmptyDir(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "attestations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := identityFromLatestAttestation(tmp); got != nil {
		t.Errorf("expected nil for empty attestations dir, got %+v", got)
	}
}

// TestIdentityFromJWT_RealToken parses the body of a real aflock-minted JWT
// (captured live from get_token over UDS) and asserts the identity fields
// are extracted from sub + identity_hash. This is the third fallback for
// JWT-only sessions where get_token was called but no tool calls produced
// attestations.
func TestIdentityFromJWT_RealToken(t *testing.T) {
	// Real JWT minted on 2026-05-05 from the live aflock+SPIRE setup.
	// sub = spiffe://aflock.ai/agent/claude-opus-4-7/4-7-0/b4d15ed93f404e8b
	const token = "eyJhbGciOiJFUzI1NiIsImtpZCI6ImVwaGVtZXJhbC1lY2RzYS1wMjU2IiwidHlwIjoiSldUIn0.eyJpc3MiOiJhZmxvY2siLCJzdWIiOiJzcGlmZmU6Ly9hZmxvY2suYWkvYWdlbnQvY2xhdWRlLW9wdXMtNC03LzQtNy0wL2I0ZDE1ZWQ5M2Y0MDRlOGIiLCJhdWQiOlsibWNwLTk2ZjBlODIxLTQ4YzAtNDRkOC04YTU0LTBiMjdmMjMyYWMyZSJdLCJleHAiOjE3Nzc5OTUwMzksImlhdCI6MTc3Nzk5MTQzOSwianRpIjoibWNwLTk2ZjBlODIxLTQ4YzAtNDRkOC04YTU0LTBiMjdmMjMyYWMyZSIsImFnZW50X2lkIjoic3BpZmZlOi8vYWZsb2NrLmFpL2FnZW50L2NsYXVkZS1vcHVzLTQtNy80LTctMC9iNGQxNWVkOTNmNDA0ZThiIiwiaWRlbnRpdHlfaGFzaCI6ImI0ZDE1ZWQ5M2Y0MDRlOGI0NzE3YmNmOTAzMTNmZGE5ZDJhNjc3OTkzMTc0NjgyMTQ2MzRlNTY5NmY3MGI4NTQiLCJhbGxvd2VkX3Rvb2xzIjpbIlJlYWQiLCJXcml0ZSIsIkVkaXQiLCJHbG9iIiwiR3JlcCIsIkJhc2giLCJtY3BfX2FmbG9ja19fKiJdLCJkZW5pZWRfdG9vbHMiOlsiVGFzayIsIldlYkZldGNoIiwiV2ViU2VhcmNoIl0sImxpbWl0cyI6eyJtYXhTcGVuZFVTRCI6eyJ2YWx1ZSI6NSwiZW5mb3JjZW1lbnQiOiJmYWlsLWZhc3QifSwibWF4VHVybnMiOnsidmFsdWUiOjUwLCJlbmZvcmNlbWVudCI6InBvc3QtaG9jIn19LCJwb2xpY3lfZGlnZXN0IjoiMTdiMGNhMDQzNzk3NGE0NWJkZTQ2ZTdmZDgzZTg1NWQzNDIwY2I3ZWEwMWI4MjAwNzk3MTU4ZGQxMGFkZDU1ZiJ9.3fguazU_tQFbQ_bwlk_NOHSfwhBh-QqpmMyCXOnWTRZ2L3EjBOE9c3v-iMMbrOX4R3CSqkayUR9WVWQyiuoNRA"

	id := identityFromJWT(token)
	if id == nil {
		t.Fatal("identityFromJWT returned nil for a valid token")
	}
	if id.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q, want %q", id.Model, "claude-opus-4-7")
	}
	if id.ModelVersion != "4.7.0" {
		t.Errorf("ModelVersion = %q, want %q (dashes should map back to dots)", id.ModelVersion, "4.7.0")
	}
	if id.IdentityHash != "b4d15ed93f404e8b4717bcf90313fda9d2a67799317468214634e5696f70b854" {
		t.Errorf("IdentityHash mismatch: got %q", id.IdentityHash)
	}
}

func TestIdentityFromJWT_Malformed(t *testing.T) {
	cases := []string{
		"",
		"not-a-jwt",
		"only.two",
		"three.parts.but-middle-is-not-base64$$$",
	}
	for _, tok := range cases {
		if got := identityFromJWT(tok); got != nil {
			t.Errorf("identityFromJWT(%q) = %+v, want nil", tok, got)
		}
	}
}
