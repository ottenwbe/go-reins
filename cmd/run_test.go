package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// setYes flips the package-level resolved config for one test and
// restores it after; stdinApprover reads the global, like the
// commands do.
func setYes(t *testing.T, yes bool) {
	t.Helper()
	conf.Yes = yes
	t.Cleanup(func() { conf.Yes = false })
}

func TestStdinApproverDecisions(t *testing.T) {
	setYes(t, false)

	tests := map[string]struct {
		in      string
		allow   bool
		wantOut string
	}{
		"allow on y":         {"y\n", true, ""},
		"allow on YES":       {"YES\n", true, ""},
		"allow on yes":       {"yes\n", true, ""},
		"deny on n":          {"n\n", false, "denied"},
		"deny on empty line": {"\n", false, "denied"},
		"deny on junk":       {"maybe\n", false, "denied"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			allow := stdinApprover(strings.NewReader(tc.in), &out)("shell", `{"command":"ls"}`)
			if allow != tc.allow {
				t.Errorf("allow = %v, want %v", allow, tc.allow)
			}
			if !strings.Contains(out.String(), tc.wantOut) {
				t.Errorf("output %q does not contain %q", out.String(), tc.wantOut)
			}
		})
	}
}

// TestStdinApproverEOFDenyWithHint pins the unattended-run case: no
// answer on stdin must deny the call, and the message must tell the
// operator that automation opts in via --yes.
func TestStdinApproverEOFDenyWithHint(t *testing.T) {
	setYes(t, false)

	var out bytes.Buffer
	if stdinApprover(strings.NewReader(""), &out)("shell", `{"command":"ls"}`) {
		t.Error("EOF must deny the call")
	}
	msg := out.String()
	for _, want := range []string{"defaulting to deny", "--yes"} {
		if !strings.Contains(msg, want) {
			t.Errorf("hint %q missing from %q", want, msg)
		}
	}
}

// TestStdinApproverKeepsPipedAnswers covers the scripted-approval
// pattern from the README: several answers piped in must each reach
// their own call, not just the first.
func TestStdinApproverKeepsPipedAnswers(t *testing.T) {
	setYes(t, false)

	var out bytes.Buffer
	approve := stdinApprover(strings.NewReader("y\ny\nn\n"), &out)
	if !approve("shell", `{"command":"ls"}`) {
		t.Error("first piped answer lost")
	}
	if !approve("shell", `{"command":"uname -a"}`) {
		t.Error("second piped answer lost")
	}
	if approve("shell", `{"command":"rm -rf /"}`) {
		t.Error("third answer should deny")
	}
}

// TestStdinApproverYesFlag: with --yes the approver is nil, the
// agent's signal that every call passes the gate.
func TestStdinApproverYesFlag(t *testing.T) {
	setYes(t, true)

	var out bytes.Buffer
	if ap := stdinApprover(strings.NewReader(""), &out); ap != nil {
		t.Error("want nil approver when --yes is set")
	}
	if out.Len() != 0 {
		t.Errorf("--yes must not prompt, got %q", out.String())
	}
}
