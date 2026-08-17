package title

import (
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/conventional"
)

func mustResolve(t *testing.T, style Style) Format {
	t.Helper()
	f, err := Resolve(string(style), "", "", "", "")
	if err != nil {
		t.Fatalf("Resolve(%q) failed: %v", style, err)
	}
	return f
}

func TestResolveRejectsUnknownStyle(t *testing.T) {
	for _, style := range []string{"", "exfreso", "conventional-commits", "Exfresso"} {
		if _, err := Resolve(style, "", "", "", ""); err == nil {
			t.Errorf("Resolve(%q) accepted an unknown style", style)
		} else if !strings.Contains(err.Error(), "title.style") {
			t.Errorf("Resolve(%q) error %q does not name the offending key", style, err)
		}
	}
}

// --- exfresso ---------------------------------------------------------------

func TestExfressoKeepsCompliantTitleByteIdentical(t *testing.T) {
	f := mustResolve(t, StyleExfresso)
	for _, title := range []string{
		"[sc-1234] Add dim-weight rounding",
		"[sc-7] Fix rating workflow timeout",
		"[sc-1234567] Update the rate library: handle nil quotes",
		"[SC-1234] Add dim-weight rounding",
	} {
		if got := f.Normalize(title, "sc-9999-other-branch"); got != title {
			t.Errorf("Normalize(%q) = %q, want it byte-identical", title, got)
		}
		if !f.Compliant(title) {
			t.Errorf("Compliant(%q) = false, want true", title)
		}
	}
}

func TestExfressoPrefixesFromBranchToken(t *testing.T) {
	f := mustResolve(t, StyleExfresso)
	got := f.Normalize("Add dim-weight rounding", "sc-1234-dim-weight-rounding")
	if want := "[sc-1234] Add dim-weight rounding"; got != want {
		t.Errorf("Normalize = %q, want %q", got, want)
	}
}

func TestExfressoBranchTokenMatchIsCaseInsensitive(t *testing.T) {
	f := mustResolve(t, StyleExfresso)
	got := f.Normalize("Add dim-weight rounding", "SC-1234-Dim-Weight-Rounding")
	if want := "[sc-1234] Add dim-weight rounding"; got != want {
		t.Errorf("Normalize = %q, want %q", got, want)
	}
}

func TestExfressoMultiDigitToken(t *testing.T) {
	f := mustResolve(t, StyleExfresso)
	got := f.Normalize("Add rounding", "feature/sc-987654-rounding")
	if want := "[sc-987654] Add rounding"; got != want {
		t.Errorf("Normalize = %q, want %q", got, want)
	}
}

func TestExfressoWithoutBranchTokenInventsNothing(t *testing.T) {
	f := mustResolve(t, StyleExfresso)
	for _, branch := range []string{"dim-weight-rounding", "", "release/2026-08", "sc-abc-no-number"} {
		got := f.Normalize("Add dim-weight rounding", branch)
		if want := "Add dim-weight rounding"; got != want {
			t.Errorf("Normalize with branch %q = %q, want %q", branch, got, want)
		}
	}
}

func TestExfressoEmptyTitleStaysEmpty(t *testing.T) {
	f := mustResolve(t, StyleExfresso)
	for _, title := range []string{"", "   ", "\n\t "} {
		if got := f.Normalize(title, "sc-1234-branch"); got != "" {
			t.Errorf("Normalize(%q) = %q, want empty", title, got)
		}
	}
}

func TestExfressoTrimsBeforeNormalizing(t *testing.T) {
	f := mustResolve(t, StyleExfresso)
	got := f.Normalize("  Add rounding  ", "sc-1234-branch")
	if want := "[sc-1234] Add rounding"; got != want {
		t.Errorf("Normalize = %q, want %q", got, want)
	}
}

// A title that happens to contain a colon must not be mistaken for a
// conventional type prefix, and must not be treated as compliant either.
func TestExfressoTitleWithColonIsNotConventional(t *testing.T) {
	f := mustResolve(t, StyleExfresso)
	got := f.Normalize("fix: rating workflow timeout", "sc-1234-timeout")
	if want := "[sc-1234] fix: rating workflow timeout"; got != want {
		t.Errorf("Normalize = %q, want %q", got, want)
	}
	if f.Compliant("fix: rating workflow timeout") {
		t.Error("Compliant returned true for a conventional-commit title under exfresso")
	}
}

func TestExfressoPromptRuleDescribesTheFormat(t *testing.T) {
	rule := mustResolve(t, StyleExfresso).PromptRule()
	if rule == "" {
		t.Fatal("PromptRule is empty")
	}
	if !strings.Contains(rule, "[sc-") {
		t.Errorf("PromptRule does not show the format: %q", rule)
	}
	if strings.Contains(rule, "feat") || strings.Contains(rule, "scope") {
		t.Errorf("PromptRule leaks conventional-commit guidance: %q", rule)
	}
}

// UsesBranchToken lets a caller that cannot know the branch yet (the wizard
// suggests a branch and a commit subject in one call) tell the agent to read the
// branch itself.
func TestUsesBranchToken(t *testing.T) {
	if !mustResolve(t, StyleExfresso).UsesBranchToken() {
		t.Error("exfresso should report that it reads a token from the branch")
	}
	for _, style := range []Style{StyleConventional, StyleFree} {
		if mustResolve(t, style).UsesBranchToken() {
			t.Errorf("%s should not report a branch token", style)
		}
	}
	var zero Format
	if zero.UsesBranchToken() {
		t.Error("the zero value should not report a branch token")
	}
	withToken, err := resolveCustomStyle(t, `^\[x-\d+\] .+$`, `(x-\d+)`, "[{{.Token}}] {{.Description}}", "rule")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if !withToken.UsesBranchToken() {
		t.Error("a custom style with a branch token should report one")
	}
	withoutToken, err := resolveCustomStyle(t, `^\[x-\d+\] .+$`, "", "", "rule")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if withoutToken.UsesBranchToken() {
		t.Error("a custom style with no branch token should not report one")
	}
}

// --- conventional -----------------------------------------------------------

// conventionalGolden freezes upstream v1.53.0 title behavior. It is the guard
// that makes a rebase onto a newer upstream safe to reason about: if upstream
// changes TightenTitle, this table is where it shows up.
var conventionalGolden = []struct{ in, want string }{
	{"fix: rating workflow timeout", "fix: rating workflow timeout"},
	{"feat(rating): add dim-weight rounding", "feat(rating): add dim-weight rounding"},
	{"chore: bump deps", "chore: bump deps"},
	{"Add dim-weight rounding", "feat: Add dim-weight rounding"},
	{"Fix rating workflow timeout", "fix: Fix rating workflow timeout"},
	{"Update README for rate library", "docs: Update README for rate library"},
	{"[sc-1234] Add dim-weight rounding", "chore: [sc-1234] Add dim-weight rounding"},
	{"wip: something", "chore: wip: something"},
	{"", ""},
}

func TestConventionalStyleMatchesUpstreamGolden(t *testing.T) {
	f := mustResolve(t, StyleConventional)
	for _, tc := range conventionalGolden {
		if got := f.Normalize(tc.in, "sc-1234-branch"); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestConventionalStyleDelegatesToConventionalPackage proves the style is a
// pass-through rather than a reimplementation, so upstream changes to
// internal/conventional are picked up without touching this package.
func TestConventionalStyleDelegatesToConventionalPackage(t *testing.T) {
	f := mustResolve(t, StyleConventional)
	for _, in := range []string{
		"Add dim-weight rounding", "fix: timeout", "[sc-1234] Add rounding",
		"Introduce a new rating command", "Corrected the output", "docs: tidy",
		"  Add rounding  ", "",
	} {
		if got, want := f.Normalize(in, "sc-1234-branch"), conventional.TightenTitle(in); got != want {
			t.Errorf("Normalize(%q) = %q, conventional.TightenTitle = %q", in, got, want)
		}
	}
}

// The branch is irrelevant under conventional: it must never contribute a token.
func TestConventionalStyleIgnoresBranch(t *testing.T) {
	f := mustResolve(t, StyleConventional)
	got := f.Normalize("Add rounding", "sc-1234-rounding")
	if want := "feat: Add rounding"; got != want {
		t.Errorf("Normalize = %q, want %q", got, want)
	}
}

func TestConventionalCompliantMatchesIsTitle(t *testing.T) {
	f := mustResolve(t, StyleConventional)
	for _, in := range []string{"fix: x", "feat(a): x", "wip: x", "no colon here", ""} {
		if got, want := f.Compliant(in), conventional.IsTitle(in); got != want {
			t.Errorf("Compliant(%q) = %v, conventional.IsTitle = %v", in, got, want)
		}
	}
}

func TestConventionalPromptRuleCarriesReleaseAndScopeRules(t *testing.T) {
	rule := mustResolve(t, StyleConventional).PromptRule()
	if !strings.Contains(rule, conventional.ReleaseTypeRule) {
		t.Error("PromptRule omits conventional.ReleaseTypeRule")
	}
	for _, want := range []string{"feat, fix, docs", "scope"} {
		if !strings.Contains(rule, want) {
			t.Errorf("PromptRule omits %q: %q", want, rule)
		}
	}
}

// --- free and the zero value ------------------------------------------------

func TestFreeStyleNeverRewrites(t *testing.T) {
	f := mustResolve(t, StyleFree)
	for _, in := range []string{"Add rounding", "[sc-1234] Add rounding", "fix: x"} {
		if got := f.Normalize(in, "sc-1234-branch"); got != in {
			t.Errorf("Normalize(%q) = %q, want it unchanged", in, got)
		}
		if !f.Compliant(in) {
			t.Errorf("Compliant(%q) = false, want true under free", in)
		}
	}
	if rule := f.PromptRule(); rule != "" {
		t.Errorf("PromptRule = %q, want empty under free", rule)
	}
}

func TestZeroValueFormatBehavesAsFree(t *testing.T) {
	var f Format
	for _, in := range []string{"Add rounding", "[sc-1234] Add rounding", "fix: x"} {
		if got := f.Normalize(in, "sc-1234-branch"); got != in {
			t.Errorf("zero-value Normalize(%q) = %q, want it unchanged", in, got)
		}
		if !f.Compliant(in) {
			t.Errorf("zero-value Compliant(%q) = false, want true", in)
		}
	}
	if rule := f.PromptRule(); rule != "" {
		t.Errorf("zero-value PromptRule = %q, want empty", rule)
	}
}

func TestFreeStyleTrimsOnly(t *testing.T) {
	f := mustResolve(t, StyleFree)
	if got := f.Normalize("  Add rounding  ", ""); got != "Add rounding" {
		t.Errorf("Normalize = %q, want %q", got, "Add rounding")
	}
}

// --- custom -----------------------------------------------------------------

func resolveCustomStyle(t *testing.T, pattern, branchToken, tmpl, promptRule string) (Format, error) {
	t.Helper()
	return Resolve(string(StyleCustom), pattern, branchToken, tmpl, promptRule)
}

func TestCustomStyleNormalizesFromBranchToken(t *testing.T) {
	f, err := resolveCustomStyle(t, `^\[[A-Z]+-\d+\] .+$`, `(?i)([a-z]+-\d+)`, "[{{.Token}}] {{.Description}}", "The title must start with a ticket key.")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if got, want := f.Normalize("Add rounding", "abc-42-rounding"), "[abc-42] Add rounding"; got != want {
		t.Errorf("Normalize = %q, want %q", got, want)
	}
	if got, want := f.Normalize("[ABC-42] Add rounding", "abc-42-rounding"), "[ABC-42] Add rounding"; got != want {
		t.Errorf("compliant Normalize = %q, want %q", got, want)
	}
	if got, want := f.PromptRule(), "The title must start with a ticket key."; got != want {
		t.Errorf("PromptRule = %q, want %q", got, want)
	}
}

func TestCustomStyleWithoutBranchTokenLeavesTitleAlone(t *testing.T) {
	f, err := resolveCustomStyle(t, `^\[[A-Z]+-\d+\] .+$`, "", "", "rule")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if got, want := f.Normalize("Add rounding", "abc-42-rounding"), "Add rounding"; got != want {
		t.Errorf("Normalize = %q, want %q", got, want)
	}
}

// A custom style whose trusted prompt_rule is absent degrades to a rule derived
// from the validated pattern rather than failing the run.
func TestCustomStyleDerivesPromptRuleFromPattern(t *testing.T) {
	pattern := `^\[[A-Z]+-\d+\] .+$`
	f, err := resolveCustomStyle(t, pattern, "", "", "")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	rule := f.PromptRule()
	if rule == "" {
		t.Fatal("PromptRule is empty; expected a derived rule")
	}
	if !strings.Contains(rule, pattern) {
		t.Errorf("derived PromptRule does not carry the pattern: %q", rule)
	}
}

func TestCustomStyleRequiresPattern(t *testing.T) {
	if _, err := resolveCustomStyle(t, "", "", "", "rule"); err == nil {
		t.Error("custom style accepted an empty pattern")
	}
}

func TestCustomStyleValidationRejects(t *testing.T) {
	long := strings.Repeat("a", 513)
	cases := []struct {
		name        string
		pattern     string
		branchToken string
		tmpl        string
		wantInError string
	}{
		{"uncompilable pattern", `^\[(unclosed`, "", "", "title.pattern"},
		{"oversized pattern", "^" + long + "$", "", "", "title.pattern"},
		{"uncompilable branch token", `^\[.+$`, `(unclosed`, "[{{.Token}}] {{.Description}}", "title.branch_token"},
		{"oversized branch token", `^\[.+$`, "(" + long + ")", "[{{.Token}}] {{.Description}}", "title.branch_token"},
		{"zero capture groups", `^\[.+$`, `sc-\d+`, "[{{.Token}}] {{.Description}}", "title.branch_token"},
		{"two capture groups", `^\[.+$`, `(sc)-(\d+)`, "[{{.Token}}] {{.Description}}", "title.branch_token"},
		{"template without branch token", `^\[.+$`, "", "[{{.Token}}] {{.Description}}", "title.template"},
		{"branch token without template", `^\[.+$`, `(sc-\d+)`, "", "title.template"},
		{"unknown placeholder", `^\[.+$`, `(sc-\d+)`, "[{{.Story}}] {{.Description}}", "title.template"},
		{"template function", `^\[.+$`, `(sc-\d+)`, "{{ printf \"%s\" .Token }} {{.Description}}", "title.template"},
		{"template control action", `^\[.+$`, `(sc-\d+)`, "{{if .Token}}x{{end}} {{.Description}}", "title.template"},
		{"unsafe unicode in template", `^\[.+$`, `(sc-\d+)`, "[‮{{.Token}}] {{.Description}}", "title.template"},
		{"multiline template", `^\[.+$`, `(sc-\d+)`, "[{{.Token}}]\n{{.Description}}", "title.template"},
		{"oversized template", `^\[.+$`, `(sc-\d+)`, strings.Repeat("x", 1025) + "{{.Description}}", "title.template"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveCustomStyle(t, tc.pattern, tc.branchToken, tc.tmpl, "rule")
			if err == nil {
				t.Fatalf("Resolve accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantInError) {
				t.Errorf("error %q does not name %q", err, tc.wantInError)
			}
		})
	}
}

// ValidateCustomFields is what config calls on a parsed file, where the style
// may be set in a different file than the pattern. It applies every field-level
// and pairing rule, but not the "pattern is required" rule that only makes sense
// once the style is known to be custom.
func TestValidateCustomFieldsAcceptsAbsentFields(t *testing.T) {
	if err := ValidateCustomFields("", "", ""); err != nil {
		t.Errorf("ValidateCustomFields with no fields set failed: %v", err)
	}
	if err := ValidateCustomFields(`^\[sc-(\d+)\] .+$`, "", ""); err != nil {
		t.Errorf("ValidateCustomFields with only a pattern failed: %v", err)
	}
}

func TestValidateCustomFieldsRejectsBadFields(t *testing.T) {
	cases := []struct {
		name        string
		pattern     string
		branchToken string
		tmpl        string
		wantInError string
	}{
		{"uncompilable pattern", `^\[(unclosed`, "", "", "title.pattern"},
		{"zero capture groups", "", `sc-\d+`, "[{{.Token}}] {{.Description}}", "title.branch_token"},
		{"two capture groups", "", `(sc)-(\d+)`, "[{{.Token}}] {{.Description}}", "title.branch_token"},
		{"template without branch token", "", "", "[{{.Token}}] {{.Description}}", "title.template"},
		{"branch token without template", "", `(sc-\d+)`, "", "title.template"},
		{"unknown placeholder", "", `(sc-\d+)`, "[{{.Story}}] {{.Description}}", "title.template"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCustomFields(tc.pattern, tc.branchToken, tc.tmpl)
			if err == nil {
				t.Fatalf("ValidateCustomFields accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantInError) {
				t.Errorf("error %q does not name %q", err, tc.wantInError)
			}
		})
	}
}

// Presets ignore the custom-only fields rather than validating them, so a stale
// pattern left in the config cannot break a preset style.
func TestPresetsIgnoreCustomFields(t *testing.T) {
	for _, style := range []Style{StyleExfresso, StyleConventional, StyleFree} {
		f, err := Resolve(string(style), `(unclosed`, `(unclosed`, "{{.Nope}}", "ignored")
		if err != nil {
			t.Fatalf("Resolve(%q) with custom fields failed: %v", style, err)
		}
		if style == StyleExfresso {
			if got, want := f.Normalize("Add rounding", "sc-1-x"), "[sc-1] Add rounding"; got != want {
				t.Errorf("Normalize = %q, want %q", got, want)
			}
		}
	}
}

// A rendered title must stay a single line even when the description arrives
// with embedded newlines, because it becomes a PR title and a commit subject.
func TestNormalizeKeepsRenderedTitleSingleLine(t *testing.T) {
	f := mustResolve(t, StyleExfresso)
	got := f.Normalize("Add rounding\nand more", "sc-1234-branch")
	if strings.Contains(got, "\n") {
		t.Errorf("Normalize returned a multi-line title: %q", got)
	}
}
