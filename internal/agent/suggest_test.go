package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/title"
)

type stubAgent struct {
	result *Result
	err    error

	gotPrompt string
	gotCWD    string
	gotSchema json.RawMessage
}

func (s *stubAgent) Name() string { return "stub" }
func (s *stubAgent) Run(_ context.Context, opts RunOpts) (*Result, error) {
	s.gotPrompt = opts.Prompt
	s.gotCWD = opts.CWD
	s.gotSchema = opts.JSONSchema
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

// conventionalFormat is the title format the pre-existing assertions in this
// file were written against. Tests that exercise another style resolve their own.
func conventionalFormat(t *testing.T) title.Format {
	t.Helper()
	return resolveFormat(t, title.StyleConventional)
}

func resolveFormat(t *testing.T, style title.Style) title.Format {
	t.Helper()
	f, err := title.Resolve(string(style), "", "", "", "")
	if err != nil {
		t.Fatalf("resolve title style %q: %v", style, err)
	}
	return f
}

func (s *stubAgent) Close() error { return nil }

func TestSuggestBranchName(t *testing.T) {
	ag := &stubAgent{result: &Result{
		Output: json.RawMessage(`{"name":"feat/onboarding-wizard"}`),
	}}
	name, err := SuggestBranchName(context.Background(), ag, "/tmp/repo")
	if err != nil {
		t.Fatalf("SuggestBranchName failed: %v", err)
	}
	if name != "feat/onboarding-wizard" {
		t.Fatalf("expected feat/onboarding-wizard, got %q", name)
	}
	if ag.gotCWD != "/tmp/repo" {
		t.Fatalf("expected CWD to be forwarded, got %q", ag.gotCWD)
	}
	if len(ag.gotSchema) == 0 {
		t.Fatal("expected JSONSchema to be set on RunOpts")
	}
	if ag.gotPrompt == "" {
		t.Fatal("expected prompt to be non-empty")
	}
}

func TestSuggestBranchNameSanitizes(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"spaces to hyphen", "feat new wizard", "feat-new-wizard"},
		{"uppercase to lower", "FEAT/NewThing", "feat/newthing"},
		{"strip quotes", "\"fix/bug\"", "fix/bug"},
		{"collapse dashes", "fix--double---dash", "fix-double-dash"},
		{"strip trailing slash", "feat/", "feat"},
		{"strip bad chars", "fix: thing! @home", "fix-thing-home"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ag := &stubAgent{result: &Result{
				Output: json.RawMessage(`{"name":` + jsonQuote(tc.raw) + `}`),
			}}
			got, err := SuggestBranchName(context.Background(), ag, "/tmp")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestSuggestBranchNameRejectsInvalidGitRefs(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "double dot", raw: "feat/bug..fix"},
		{name: "double slash", raw: "feat//wizard"},
		{name: "lock suffix", raw: "feat/wizard.lock"},
		{name: "lock path component", raw: "feat/topic.lock/extra"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ag := &stubAgent{result: &Result{
				Output: json.RawMessage(`{"name":` + jsonQuote(tc.raw) + `}`),
			}}
			if _, err := SuggestBranchName(context.Background(), ag, "/tmp"); err == nil {
				t.Fatalf("expected invalid ref %q to be rejected", tc.raw)
			}
		})
	}
}

func TestSuggestBranchNameLengthCapped(t *testing.T) {
	long := "feat/really-long-branch-name-that-exceeds-the-limit-we-enforce-for-clarity"
	ag := &stubAgent{result: &Result{
		Output: json.RawMessage(`{"name":` + jsonQuote(long) + `}`),
	}}
	got, err := SuggestBranchName(context.Background(), ag, "/tmp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) > 60 {
		t.Fatalf("expected <= 60 chars, got %d: %q", len(got), got)
	}
}

func TestSuggestBranchNameEmpty(t *testing.T) {
	ag := &stubAgent{result: &Result{
		Output: json.RawMessage(`{"name":""}`),
	}}
	_, err := SuggestBranchName(context.Background(), ag, "/tmp")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestSuggestBranchNameOnlyInvalidChars(t *testing.T) {
	ag := &stubAgent{result: &Result{
		Output: json.RawMessage(`{"name":"!@#$%"}`),
	}}
	_, err := SuggestBranchName(context.Background(), ag, "/tmp")
	if err == nil {
		t.Fatal("expected error when all chars are stripped")
	}
}

func TestSuggestBranchNameAgentError(t *testing.T) {
	ag := &stubAgent{err: errors.New("boom")}
	_, err := SuggestBranchName(context.Background(), ag, "/tmp")
	if err == nil {
		t.Fatal("expected error from agent failure")
	}
}

func TestSuggestCommitMessage(t *testing.T) {
	ag := &stubAgent{result: &Result{
		Output: json.RawMessage(`{"subject":"feat(cli): add onboarding wizard"}`),
	}}
	got, err := SuggestCommitMessage(context.Background(), ag, "/tmp", conventionalFormat(t))
	if err != nil {
		t.Fatalf("SuggestCommitMessage failed: %v", err)
	}
	if got != "feat(cli): add onboarding wizard" {
		t.Fatalf("unexpected subject: %q", got)
	}
}

func TestSuggestCommitMessageTrimsNewlines(t *testing.T) {
	ag := &stubAgent{result: &Result{
		Output: json.RawMessage(`{"subject":"fix: thing\n\nbody"}`),
	}}
	got, err := SuggestCommitMessage(context.Background(), ag, "/tmp", conventionalFormat(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "fix: thing" {
		t.Fatalf("expected first line only, got %q", got)
	}
}

func TestSuggestCommitMessageKeepsConventionalNonReleaseType(t *testing.T) {
	ag := &stubAgent{result: &Result{
		Output: json.RawMessage(`{"subject":"refactor: improve CLI output"}`),
	}}
	got, err := SuggestCommitMessage(context.Background(), ag, "/tmp", conventionalFormat(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "refactor: improve CLI output" {
		t.Fatalf("subject = %q, want conventional agent subject unchanged", got)
	}
}

func TestSuggestCommitMessagePromptRequiresReleaseTypesForProductImpact(t *testing.T) {
	ag := &stubAgent{result: &Result{
		Output: json.RawMessage(`{"subject":"fix: improve CLI output"}`),
	}}
	if _, err := SuggestCommitMessage(context.Background(), ag, "/tmp", conventionalFormat(t)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(ag.gotPrompt, "user-facing product impact") {
		t.Fatalf("prompt should mention user-facing product impact rule, got:\n%s", ag.gotPrompt)
	}
	if !strings.Contains(ag.gotPrompt, "must use feat or fix") {
		t.Fatalf("prompt should require feat or fix for product impact, got:\n%s", ag.gotPrompt)
	}
}

func TestSuggestCommitMessageEmpty(t *testing.T) {
	ag := &stubAgent{result: &Result{
		Output: json.RawMessage(`{"subject":"   "}`),
	}}
	_, err := SuggestCommitMessage(context.Background(), ag, "/tmp", conventionalFormat(t))
	if err == nil {
		t.Fatal("expected error for empty subject")
	}
}

func TestSuggestBranchAndCommit(t *testing.T) {
	ag := &stubAgent{result: &Result{
		Output: json.RawMessage(`{"branch":"feat/onboarding-wizard","subject":"feat(cli): add onboarding wizard"}`),
	}}
	branch, subject, err := SuggestBranchAndCommit(context.Background(), ag, "/tmp/repo", conventionalFormat(t))
	if err != nil {
		t.Fatalf("SuggestBranchAndCommit failed: %v", err)
	}
	if branch != "feat/onboarding-wizard" {
		t.Fatalf("expected feat/onboarding-wizard, got %q", branch)
	}
	if subject != "feat(cli): add onboarding wizard" {
		t.Fatalf("expected commit subject, got %q", subject)
	}
	if ag.gotCWD != "/tmp/repo" {
		t.Fatalf("expected CWD to be forwarded, got %q", ag.gotCWD)
	}
	if len(ag.gotSchema) == 0 {
		t.Fatal("expected JSONSchema to be set on RunOpts")
	}
	if ag.gotPrompt == "" {
		t.Fatal("expected prompt to be non-empty")
	}
}

func TestSuggestBranchAndCommitSanitizes(t *testing.T) {
	ag := &stubAgent{result: &Result{
		Output: json.RawMessage(`{"branch":"FEAT/New Thing","subject":"feat(cli): clean up thing\n\nbody paragraph"}`),
	}}
	branch, subject, err := SuggestBranchAndCommit(context.Background(), ag, "/tmp", conventionalFormat(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch != "feat/new-thing" {
		t.Fatalf("expected branch to be sanitized, got %q", branch)
	}
	if subject != "feat(cli): clean up thing" {
		t.Fatalf("expected first-line commit subject, got %q", subject)
	}
}

func TestSuggestBranchAndCommitEmptyBranchErrors(t *testing.T) {
	ag := &stubAgent{result: &Result{
		Output: json.RawMessage(`{"branch":"","subject":"feat: something"}`),
	}}
	if _, _, err := SuggestBranchAndCommit(context.Background(), ag, "/tmp", conventionalFormat(t)); err == nil {
		t.Fatal("expected error when branch is empty")
	}
}

func TestSuggestBranchAndCommitEmptySubjectReturnsBranch(t *testing.T) {
	// An empty commit subject is tolerated: callers can fall back to a
	// dedicated SuggestCommitMessage call rather than failing the wizard.
	ag := &stubAgent{result: &Result{
		Output: json.RawMessage(`{"branch":"fix/bug","subject":""}`),
	}}
	branch, subject, err := SuggestBranchAndCommit(context.Background(), ag, "/tmp", conventionalFormat(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch != "fix/bug" {
		t.Fatalf("expected fix/bug, got %q", branch)
	}
	if subject != "" {
		t.Fatalf("expected empty subject, got %q", subject)
	}
}

func TestSuggestBranchAndCommitAgentError(t *testing.T) {
	ag := &stubAgent{err: errors.New("boom")}
	if _, _, err := SuggestBranchAndCommit(context.Background(), ag, "/tmp", conventionalFormat(t)); err == nil {
		t.Fatal("expected error from agent failure")
	}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestSuggestCommitMessageUsesConfiguredFormat(t *testing.T) {
	ag := &stubAgent{result: &Result{
		Output: json.RawMessage(`{"subject":"Add onboarding wizard"}`),
	}}
	got, err := SuggestCommitMessage(context.Background(), ag, "/tmp", resolveFormat(t, title.StyleFree))
	if err != nil {
		t.Fatalf("SuggestCommitMessage failed: %v", err)
	}
	if got != "Add onboarding wizard" {
		t.Fatalf("subject = %q, want it unchanged under the free style", got)
	}
}

func TestSuggestCommitMessagePromptCarriesFormatRule(t *testing.T) {
	cases := []struct {
		style      title.Style
		wantInside []string
		wantAbsent []string
	}{
		{
			style:      title.StyleExfresso,
			wantInside: []string{"[sc-<number>]", "read the current branch"},
			wantAbsent: []string{"conventional commit format"},
		},
		{
			style:      title.StyleConventional,
			wantInside: []string{"conventional commit format", "must use feat or fix"},
			wantAbsent: []string{"[sc-<number>]", "read the current branch"},
		},
		{
			style:      title.StyleFree,
			wantAbsent: []string{"conventional commit format", "[sc-<number>]"},
		},
	}
	for _, tc := range cases {
		t.Run(string(tc.style), func(t *testing.T) {
			ag := &stubAgent{result: &Result{Output: json.RawMessage(`{"subject":"a subject"}`)}}
			if _, err := SuggestCommitMessage(context.Background(), ag, "/tmp", resolveFormat(t, tc.style)); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, want := range tc.wantInside {
				if !strings.Contains(ag.gotPrompt, want) {
					t.Errorf("prompt omits %q, got:\n%s", want, ag.gotPrompt)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(ag.gotPrompt, absent) {
					t.Errorf("prompt should not contain %q, got:\n%s", absent, ag.gotPrompt)
				}
			}
			// Format-independent rules stay in every prompt.
			for _, want := range []string{"One line only, under 72 characters", "Do not invent behavior"} {
				if !strings.Contains(ag.gotPrompt, want) {
					t.Errorf("prompt omits the format-independent rule %q, got:\n%s", want, ag.gotPrompt)
				}
			}
		})
	}
}

func TestSuggestBranchAndCommitPromptCarriesFormatRule(t *testing.T) {
	ag := &stubAgent{result: &Result{
		Output: json.RawMessage(`{"branch":"sc-1234-onboarding-wizard","subject":"[sc-1234] Add onboarding wizard"}`),
	}}
	branch, subject, err := SuggestBranchAndCommit(context.Background(), ag, "/tmp", resolveFormat(t, title.StyleExfresso))
	if err != nil {
		t.Fatalf("SuggestBranchAndCommit failed: %v", err)
	}
	if branch != "sc-1234-onboarding-wizard" {
		t.Errorf("branch = %q", branch)
	}
	if subject != "[sc-1234] Add onboarding wizard" {
		t.Errorf("subject = %q, want it unchanged", subject)
	}
	if !strings.Contains(ag.gotPrompt, "[sc-<number>]") {
		t.Errorf("prompt omits the configured format rule, got:\n%s", ag.gotPrompt)
	}
}

// The wizard cannot supply a branch: it asks for the branch and the subject in
// one call, so the agent is told to read the branch itself when the format takes
// a token from it. sanitizeCommitSubject therefore normalizes with no branch and
// must not invent a token.
func TestSuggestCommitSubjectInventsNoTokenWithoutABranch(t *testing.T) {
	ag := &stubAgent{result: &Result{
		Output: json.RawMessage(`{"subject":"Add onboarding wizard"}`),
	}}
	got, err := SuggestCommitMessage(context.Background(), ag, "/tmp", resolveFormat(t, title.StyleExfresso))
	if err != nil {
		t.Fatalf("SuggestCommitMessage failed: %v", err)
	}
	if got != "Add onboarding wizard" {
		t.Fatalf("subject = %q, want no invented story number", got)
	}
}

// Branch naming is deliberately not configurable in this pass, but the exfresso
// style depends on the branch carrying the story token, so the branch rules say so.
func TestBranchNameRulesMentionTheStoryToken(t *testing.T) {
	if !strings.Contains(branchNameRules, "sc-") {
		t.Errorf("branchNameRules should state the sc-<number> coupling, got:\n%s", branchNameRules)
	}
}
