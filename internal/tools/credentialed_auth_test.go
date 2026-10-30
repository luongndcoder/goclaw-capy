package tools

import (
	"strings"
	"testing"
)

// TestCredentialedAuthError_NoRetryDirective is the regression guard for the
// production incident where the agent retried a gws call 5 times against dead
// credentials: auth-shaped failures must come back with an explicit
// do-not-retry directive plus a user-facing message.
func TestCredentialedAuthError_NoRetryDirective(t *testing.T) {
	deadOutputs := []string{
		`{"error": {"code": 401, "message": "Access denied. No credentials provided. Run gws auth login"}}`,
		"STDERR:\nerror[auth]: Access denied (invalid_grant)",
		"Unauthorized: 401 calling the API",
		"Login required, please run gh auth login",
		"ExpiredToken: The security token included in the request is expired",
	}
	for _, out := range deadOutputs {
		r := credentialedExecFailError("gws", []string{"drive", "about", "get"}, 1, out)
		if !r.IsError {
			t.Errorf("auth failure must be an error: %q", out)
			continue
		}
		if !strings.Contains(r.ForLLM, "DO NOT retry") {
			t.Errorf("missing no-retry directive: %q", out)
		}
		if r.ForUser == "" {
			t.Errorf("missing user-facing message: %q", out)
		}
		if !strings.Contains(r.ForLLM, out) {
			t.Errorf("original output must be preserved for diagnosis: %q", out)
		}
	}
}

// TestCredentialedExecFailError_OrdinaryFailureUnchanged ensures non-auth
// failures keep the classic retryable shape (no false positives).
func TestCredentialedExecFailError_OrdinaryFailureUnchanged(t *testing.T) {
	ordinary := []string{
		"STDERR:\nerror[exec]: binary not found in PATH",
		"command timed out waiting for input",
		"file not found: /tmp/missing.csv",
		"exit status 2: invalid argument --format",
	}
	for _, out := range ordinary {
		r := credentialedExecFailError("gws", []string{"sheets"}, 1, out)
		if !r.IsError {
			t.Errorf("ordinary failure must stay an error: %q", out)
		}
		if strings.Contains(r.ForLLM, "DO NOT retry") {
			t.Errorf("false positive auth detection: %q", out)
		}
	}
}
