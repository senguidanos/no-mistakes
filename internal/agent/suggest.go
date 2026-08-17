package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kunchenguid/no-mistakes/internal/title"
)

// branchNameRules is shared between the single-purpose prompt and the combined
// prompt so behavior stays in lock-step.
//
// Branch naming is deliberately NOT configurable: unlike the title format there
// is no branch style setting. It is nonetheless coupled to the title format,
// because a story-number title style can only recover the number from the branch
// name - so the rules say so explicitly rather than leaving the connection
// implicit. Changing the title format does not change these rules.
const branchNameRules = `- Use kebab-case.
- If the change has a Shortcut story, start the branch with its token: "sc-<number>-<short-description>" (for example "sc-1234-dim-weight-rounding"). The PR title format reads the story number back out of the branch name, so a branch without the token yields a title without it.
- Otherwise prefer a conventional prefix: "feat/", "fix/", "chore/", "refactor/", "docs/", or "test/".
- Keep it under 40 characters.`

// commitSubjectRulesFor assembles the commit-subject rules for a title format.
// The format-independent rules are static; the format half comes from the
// configured title style (see internal/title), which is what keeps a commit made
// through the wizard and the PR title generated later consistent.
func commitSubjectRulesFor(f title.Format) string {
	rules := "- One line only, under 72 characters."
	if rule := f.PromptRule(); rule != "" {
		rules += "\n" + rule
	}
	if f.UsesBranchToken() {
		// The wizard asks for a branch and a subject in one call, so at suggestion
		// time the branch may not exist yet. The agent has git access and is
		// already told to inspect the working tree, so it can read the token
		// itself - and must not invent one when there is none.
		rules += "\n- To find the token the format needs, read the current branch name with git and reuse its token. If the branch has no token, omit the prefix entirely rather than inventing one."
	}
	return rules + "\n- Do not invent behavior."
}

const branchNamePrompt = `Suggest a short, descriptive git branch name for the current working-tree changes in this repository.

Inspect the state yourself (e.g. git status, git diff HEAD, git diff --staged) in the working directory.

Rules:
` + branchNameRules + `
- Return JSON: {"name":"..."}`

func commitSubjectPromptFor(f title.Format) string {
	return `Suggest a commit subject line summarizing the current working-tree changes.

Inspect the state yourself (e.g. git status, git diff HEAD, git diff --staged) in the working directory.

Rules:
` + commitSubjectRulesFor(f) + `
- Return JSON: {"subject":"..."}`
}

func branchAndCommitPromptFor(f title.Format) string {
	return `Suggest a git branch name and a commit subject for the current working-tree changes in this repository.

Inspect the state yourself (e.g. git status, git diff HEAD, git diff --staged) in the working directory.

Branch name rules:
` + branchNameRules + `

Commit subject rules:
` + commitSubjectRulesFor(f) + `

Return JSON: {"branch":"...","subject":"..."}`
}

var branchNameSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string"}
	},
	"required": ["name"]
}`)

var commitSubjectSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"subject": {"type": "string"}
	},
	"required": ["subject"]
}`)

var branchAndCommitSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"branch": {"type": "string"},
		"subject": {"type": "string"}
	},
	"required": ["branch", "subject"]
}`)

type branchSuggestion struct {
	Name string `json:"name"`
}

type commitSuggestion struct {
	Subject string `json:"subject"`
}

type branchAndCommitSuggestion struct {
	Branch  string `json:"branch"`
	Subject string `json:"subject"`
}

// SuggestBranchName asks the agent to propose a short git branch name for the
// current working-tree state in dir. The suggestion is sanitized so it's safe
// to pass to `git checkout -b`.
func SuggestBranchName(ctx context.Context, ag Agent, dir string) (string, error) {
	result, err := ag.Run(ctx, RunOpts{
		Prompt:     branchNamePrompt,
		CWD:        dir,
		JSONSchema: branchNameSchema,
	})
	if err != nil {
		return "", fmt.Errorf("suggest branch name: %w", err)
	}
	var parsed branchSuggestion
	if err := unmarshalSuggestion(result, &parsed); err != nil {
		return "", fmt.Errorf("parse branch name suggestion: %w", err)
	}
	name := sanitizeBranchName(parsed.Name)
	if name == "" {
		return "", fmt.Errorf("agent returned empty or unusable branch name")
	}
	return name, nil
}

// SuggestBranchAndCommit asks the agent to propose both a git branch name and
// a commit subject in the configured title format for the current working-tree
// state in a single call. Combining the two saves one full agent round-trip when the
// wizard needs both (new branch + dirty tree).
//
// The branch name must be present and sanitizes to a valid git ref; otherwise
// an error is returned. The commit subject is best-effort: if the agent
// returns an empty subject, this function returns an empty string with no
// error so the caller can fall back to SuggestCommitMessage.
func SuggestBranchAndCommit(ctx context.Context, ag Agent, dir string, f title.Format) (branch, subject string, err error) {
	result, err := ag.Run(ctx, RunOpts{
		Prompt:     branchAndCommitPromptFor(f),
		CWD:        dir,
		JSONSchema: branchAndCommitSchema,
	})
	if err != nil {
		return "", "", fmt.Errorf("suggest branch and commit: %w", err)
	}
	var parsed branchAndCommitSuggestion
	if err := unmarshalSuggestion(result, &parsed); err != nil {
		return "", "", fmt.Errorf("parse branch and commit suggestion: %w", err)
	}
	branch = sanitizeBranchName(parsed.Branch)
	if branch == "" {
		return "", "", fmt.Errorf("agent returned empty or unusable branch name")
	}
	subject = sanitizeCommitSubject(parsed.Subject, f)
	return branch, subject, nil
}

// SuggestCommitMessage asks the agent to propose a single-line commit subject
// summarizing the current working-tree state at dir.
func SuggestCommitMessage(ctx context.Context, ag Agent, dir string, f title.Format) (string, error) {
	result, err := ag.Run(ctx, RunOpts{
		Prompt:     commitSubjectPromptFor(f),
		CWD:        dir,
		JSONSchema: commitSubjectSchema,
	})
	if err != nil {
		return "", fmt.Errorf("suggest commit message: %w", err)
	}
	var parsed commitSuggestion
	if err := unmarshalSuggestion(result, &parsed); err != nil {
		return "", fmt.Errorf("parse commit message suggestion: %w", err)
	}
	subject := sanitizeCommitSubject(parsed.Subject, f)
	if subject == "" {
		return "", fmt.Errorf("agent returned empty commit subject")
	}
	return subject, nil
}

func unmarshalSuggestion(result *Result, v any) error {
	if result == nil {
		return fmt.Errorf("agent returned no result")
	}
	if len(result.Output) > 0 {
		return json.Unmarshal(result.Output, v)
	}
	if result.Text != "" {
		return json.Unmarshal([]byte(result.Text), v)
	}
	return fmt.Errorf("agent returned no output")
}

// sanitizeBranchName normalizes an agent-suggested branch name: lowercase,
// ASCII-only, alphanumerics plus - / . separators, length-capped.
func sanitizeBranchName(raw string) string {
	s := strings.TrimSpace(strings.Trim(raw, "\"'"))
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '/', r == '.':
			b.WriteRune(r)
		case r == '-', r == '_', r == ' ':
			b.WriteRune('-')
		}
	}
	s = b.String()
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-/.")
	if len(s) > 60 {
		s = s[:60]
		s = strings.Trim(s, "-/.")
	}
	if !isValidBranchName(s) {
		return ""
	}
	return s
}

func isValidBranchName(name string) bool {
	if name == "" || name == "@" {
		return false
	}
	if strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") {
		return false
	}
	if strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") {
		return false
	}
	if strings.Contains(name, "..") || strings.Contains(name, "//") || strings.Contains(name, "@{") {
		return false
	}
	for _, r := range name {
		if r < ' ' || r == 0x7f {
			return false
		}
		switch r {
		case ' ', '~', '^', ':', '?', '*', '[', '\\':
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

// sanitizeCommitSubject trims whitespace, keeps only the first line, and
// normalizes the result to the configured title format.
//
// The branch is empty: these suggestions are made before the branch exists (the
// wizard proposes both in one call), so a format that reads a token from the
// branch name leaves the subject unprefixed rather than inventing a token. The
// agent is told to read the branch itself when it can (see commitSubjectRulesFor).
func sanitizeCommitSubject(raw string, f title.Format) string {
	s := strings.TrimSpace(raw)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return f.Normalize(s, "")
}
