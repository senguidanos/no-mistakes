// Package title resolves the repository's pull-request title format.
//
// The format is configuration rather than a constant so a repository can adopt
// its own convention without patching the pipeline. Conventional Commits is one
// selectable style among several, and it delegates to internal/conventional
// unchanged, which keeps upstream changes to that logic working for free.
//
// This package deliberately does not import internal/config: config imports it,
// the same direction config already uses for internal/evidence.
package title

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"text/template"
	"text/template/parse"
	"unicode"
	"unicode/utf8"

	"github.com/kunchenguid/no-mistakes/internal/conventional"
)

// Style names a title convention.
type Style string

const (
	// StyleExfresso is "[sc-<number>] <description>", where the story number
	// comes from the branch name.
	StyleExfresso Style = "exfresso"
	// StyleConventional is Conventional Commits, implemented by
	// internal/conventional.
	StyleConventional Style = "conventional"
	// StyleFree performs no validation and no rewriting.
	StyleFree Style = "free"
	// StyleCustom is a user-supplied pattern, branch token, and template.
	StyleCustom Style = "custom"
)

// Limits are byte-based because they bound repository-controlled parsing work
// and the rendered git argument. The template limits mirror commit.fix_message
// (see internal/config/commit.go) so both repository-authored templates are
// bounded the same way; that validation cannot be shared without inverting the
// config -> title import direction.
const (
	// MaxPatternBytes bounds title.pattern and title.branch_token. Go's regexp
	// is RE2, so a user-supplied pattern cannot backtrack catastrophically; the
	// cap bounds compile cost and keeps config diffs readable.
	MaxPatternBytes = 512
	// MaxTemplateBytes bounds title.template.
	MaxTemplateBytes = 1024
	// MaxTemplatePlaceholders bounds how many placeholders title.template may
	// expand, so placeholder expansion cannot amplify data.
	MaxTemplatePlaceholders = 16
	// MaxRenderedTitleBytes bounds a rendered title.
	MaxRenderedTitleBytes = 4096
	// MaxPromptRuleBytes bounds title.prompt_rule, which is injected into a
	// gate agent's prompt.
	MaxPromptRuleBytes = 4096
)

// Styles lists every selectable style, in the order errors report them.
var Styles = []Style{StyleExfresso, StyleConventional, StyleFree, StyleCustom}

// exfresso preset. The pattern is case-insensitive so a title that already
// carries "[SC-1234]" is left byte-identical rather than acquiring a second
// prefix: a differently-cased story reference still resolves.
const (
	exfressoPattern     = `(?i)^\[sc-(\d+)\] .+$`
	exfressoBranchToken = `(?i)sc-(\d+)`
	exfressoTemplate    = `[sc-{{.Token}}] {{.Description}}`
	exfressoPromptRule  = `- Title must use the format "[sc-<number>] <concise title describing the change>", where <number> is the Shortcut story number. Take the number from the branch name: branch "sc-1234-dim-weight-rounding" gives the prefix "[sc-1234]". Capitalize the description normally.
- If the branch name carries no sc-<number> token, omit the bracketed prefix entirely and return only the description. Never invent or guess a story number.`
)

// conventionalPromptRule carries every format instruction the conventional
// style needs, including the release-type rule and the scope rules that used to
// live inline in the PR and commit-subject prompts. They belong to this style:
// a repository on another style must not be told to pick a scope.
const conventionalPromptRule = `- Use the conventional commit format: "type(scope): description" or "type: description". Valid types: feat, fix, docs, style, refactor, perf, test, build, ci, chore, revert. Scope is optional. Do not capitalize the type.
` + conventional.ReleaseTypeRule + `
- When including a scope, it MUST be a real package/module name that exists in the codebase (for example, a directory under internal/, cmd/, or the equivalent top-level grouping for this project), identified by inspecting the changed paths. Pick the primary module affected by the change, not a secondary or incidental one.
- Keep the scope at a coarse level, not too granular: a codebase typically has fewer than 10 distinct scopes in use across its history. Prefer a broad module name (e.g. "daemon", "pipeline", "cli") over a narrow file or sub-feature name. If you cannot confidently identify a real primary module, omit the scope and use "type: description".`

// Format is a resolved title format. The zero value behaves as StyleFree, so a
// caller that forgets to set it cannot silently re-enable rewriting.
type Format struct {
	style       Style
	pattern     *regexp.Regexp
	branchToken *regexp.Regexp
	tmpl        *template.Template
	promptRule  string
}

// templateData is what title.template renders against.
type templateData struct {
	Token       string
	Description string
}

// Resolve builds a Format from raw configuration values. The pattern,
// branchToken, and tmpl arguments are honored only for StyleCustom; the presets
// carry their own and ignore them, so a stale custom field left in the config
// cannot break a preset style.
//
// An unknown or empty style is an error: a style can only be selected by name.
// That is independent of the zero-value Format, which is free precisely because
// nobody selected a style for it.
func Resolve(style, pattern, branchToken, tmpl, promptRule string) (Format, error) {
	promptRule = strings.TrimSpace(promptRule)
	if len(promptRule) > MaxPromptRuleBytes {
		return Format{}, fmt.Errorf("title.prompt_rule must not exceed %d bytes, got %d", MaxPromptRuleBytes, len(promptRule))
	}
	if containsUnsafeRune(promptRule) {
		return Format{}, fmt.Errorf("title.prompt_rule must not contain control or unsafe Unicode format characters")
	}

	switch Style(style) {
	case StyleExfresso:
		return preset(StyleExfresso, exfressoPattern, exfressoBranchToken, exfressoTemplate, exfressoPromptRule)
	case StyleConventional:
		return Format{style: StyleConventional, promptRule: conventionalPromptRule}, nil
	case StyleFree:
		return Format{style: StyleFree}, nil
	case StyleCustom:
		return resolveCustom(pattern, branchToken, tmpl, promptRule)
	default:
		return Format{}, fmt.Errorf("title.style must be one of %s, got %q", strings.Join(styleNames(), ", "), style)
	}
}

func styleNames() []string {
	names := make([]string, 0, len(Styles))
	for _, s := range Styles {
		names = append(names, string(s))
	}
	return names
}

// preset builds a built-in style. Its inputs are compile-time constants, so a
// failure here is a programming error rather than a configuration one.
func preset(style Style, pattern, branchToken, tmpl, promptRule string) (Format, error) {
	f, err := build(style, pattern, branchToken, tmpl, promptRule)
	if err != nil {
		return Format{}, fmt.Errorf("built-in title style %q is invalid: %w", style, err)
	}
	return f, nil
}

func resolveCustom(pattern, branchToken, tmpl, promptRule string) (Format, error) {
	if strings.TrimSpace(pattern) == "" {
		return Format{}, fmt.Errorf("title.pattern is required when title.style is %q", StyleCustom)
	}
	f, err := build(StyleCustom, pattern, branchToken, tmpl, promptRule)
	if err != nil {
		return Format{}, err
	}
	if f.promptRule == "" {
		// A custom style whose trusted prompt_rule is absent still has to tell
		// the agent something. A rule derived from the validated pattern carries
		// no injection surface and degrades gracefully; it is a weaker
		// instruction than a hand-written rule, not an equivalent one.
		f.promptRule = derivedPromptRule(pattern)
	}
	return f, nil
}

func derivedPromptRule(pattern string) string {
	return "- The title must match this regular expression: `" + pattern + "`"
}

// ValidateCustomFields applies every field-level and pairing rule for the
// custom-style fields, so a config parser can reject a bad pattern, branch
// token, or template in the file that carries it.
//
// It deliberately does NOT apply the "pattern is required" rule that Resolve
// enforces for StyleCustom: the style may be selected in the global config while
// the fields live in a repository's, and a parser sees only one file at a time.
// The combination is enforced when Resolve builds the merged format.
func ValidateCustomFields(pattern, branchToken, tmpl string) error {
	_, err := buildFields(pattern, branchToken, tmpl)
	return err
}

type compiledFields struct {
	pattern     *regexp.Regexp
	branchToken *regexp.Regexp
	tmpl        *template.Template
}

func buildFields(pattern, branchToken, tmpl string) (compiledFields, error) {
	var out compiledFields

	compiled, err := compilePattern("title.pattern", pattern)
	if err != nil {
		return compiledFields{}, err
	}
	out.pattern = compiled

	branchToken = strings.TrimSpace(branchToken)
	tmpl = strings.TrimSpace(tmpl)
	switch {
	case branchToken != "" && tmpl == "":
		return compiledFields{}, fmt.Errorf("title.template is required when title.branch_token is set")
	case branchToken == "" && tmpl != "":
		return compiledFields{}, fmt.Errorf("title.template has no effect without title.branch_token; remove one or set both")
	case branchToken == "":
		return out, nil
	}

	token, err := compilePattern("title.branch_token", branchToken)
	if err != nil {
		return compiledFields{}, err
	}
	if n := token.NumSubexp(); n != 1 {
		return compiledFields{}, fmt.Errorf("title.branch_token must have exactly one capture group, got %d in %q", n, branchToken)
	}
	out.branchToken = token

	parsed, err := parseTemplate(tmpl)
	if err != nil {
		return compiledFields{}, err
	}
	out.tmpl = parsed
	return out, nil
}

func build(style Style, pattern, branchToken, tmpl, promptRule string) (Format, error) {
	fields, err := buildFields(pattern, branchToken, tmpl)
	if err != nil {
		return Format{}, err
	}
	return Format{
		style:       style,
		pattern:     fields.pattern,
		branchToken: fields.branchToken,
		tmpl:        fields.tmpl,
		promptRule:  promptRule,
	}, nil
}

func compilePattern(key, pattern string) (*regexp.Regexp, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, nil
	}
	if len(pattern) > MaxPatternBytes {
		return nil, fmt.Errorf("%s must not exceed %d bytes, got %d", key, MaxPatternBytes, len(pattern))
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		// Fail closed: an uncompilable pattern must not silently disable
		// validation for the repository.
		return nil, fmt.Errorf("%s %q does not compile: %w", key, pattern, err)
	}
	return compiled, nil
}

// parseTemplate validates title.template the way commit.fix_message is
// validated: literal text and known placeholders only, no functions, no control
// actions, bounded size, and a single-line rendering.
func parseTemplate(source string) (*template.Template, error) {
	if len(source) > MaxTemplateBytes {
		return nil, fmt.Errorf("title.template must not exceed %d bytes, got %d", MaxTemplateBytes, len(source))
	}
	if !utf8.ValidString(source) {
		return nil, fmt.Errorf("title.template must contain valid UTF-8")
	}
	if containsUnsafeRune(source) {
		return nil, fmt.Errorf("title.template must not contain control or unsafe Unicode format characters or line separators")
	}
	tmpl, err := template.New("title.template").Option("missingkey=error").Parse(source)
	if err != nil {
		return nil, fmt.Errorf("parse title.template: %w", err)
	}
	if err := validateTemplateNodes(tmpl); err != nil {
		return nil, err
	}
	probe := templateData{Token: "1234", Description: "probe description"}
	rendered, err := render(tmpl, probe)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(rendered) == "" {
		return nil, fmt.Errorf("title.template must render to a non-empty title")
	}
	return tmpl, nil
}

func validateTemplateNodes(tmpl *template.Template) error {
	unsupported := fmt.Errorf("title.template supports only literal text and {{.Token}} or {{.Description}} placeholders")
	if len(tmpl.Templates()) != 1 || tmpl.Tree == nil || tmpl.Tree.Root == nil {
		return unsupported
	}
	placeholders := 0
	for _, node := range tmpl.Tree.Root.Nodes {
		switch node := node.(type) {
		case *parse.TextNode:
		case *parse.ActionNode:
			if _, ok := placeholderName(node.Pipe); !ok {
				return unsupported
			}
			placeholders++
			if placeholders > MaxTemplatePlaceholders {
				return fmt.Errorf("title.template must not contain more than %d placeholders", MaxTemplatePlaceholders)
			}
		default:
			return unsupported
		}
	}
	return nil
}

func placeholderName(pipe *parse.PipeNode) (string, bool) {
	if pipe == nil || pipe.IsAssign || len(pipe.Decl) != 0 || len(pipe.Cmds) != 1 {
		return "", false
	}
	command := pipe.Cmds[0]
	if command == nil || len(command.Args) != 1 {
		return "", false
	}
	field, ok := command.Args[0].(*parse.FieldNode)
	if !ok || len(field.Ident) != 1 {
		return "", false
	}
	name := field.Ident[0]
	return name, name == "Token" || name == "Description"
}

func render(tmpl *template.Template, data templateData) (string, error) {
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("render title.template: %w", err)
	}
	rendered := out.String()
	if len(rendered) > MaxRenderedTitleBytes {
		return "", fmt.Errorf("title.template must not render to more than %d bytes", MaxRenderedTitleBytes)
	}
	if !utf8.ValidString(rendered) {
		return "", fmt.Errorf("title.template must render valid UTF-8")
	}
	if containsUnsafeRune(rendered) || strings.ContainsAny(rendered, "\n\r") {
		return "", fmt.Errorf("title.template must render a single line without control or unsafe Unicode format characters")
	}
	return strings.TrimSpace(rendered), nil
}

// containsUnsafeRune mirrors the commit.fix_message rune policy: control
// characters, bidi controls, line separators, and known invisible spoofing
// controls are rejected, while legitimate ZWNJ and ZWJ text shaping stays
// available.
func containsUnsafeRune(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) ||
			r == '\u2028' || r == '\u2029' || isUnsafeInvisibleRune(r) {
			return true
		}
	}
	return false
}

func isUnsafeInvisibleRune(r rune) bool {
	switch r {
	case '\u00ad', // soft hyphen
		'\u180e', // Mongolian vowel separator
		'\u200b', // zero width space
		'\u2060', // word joiner
		'\ufeff': // zero width no-break space / byte order mark
		return true
	}
	return r >= '\u2061' && r <= '\u2064' || // invisible mathematical operators
		r >= '\u206a' && r <= '\u206f' || // deprecated bidi formatting controls
		r >= '\ufff9' && r <= '\ufffb' || // interlinear annotation controls
		r >= '\U000e0000' && r <= '\U000e007f' // deprecated language tags and tag characters
}

// Style reports which convention this format implements. The zero value reports
// StyleFree.
func (f Format) Style() Style {
	if f.style == "" {
		return StyleFree
	}
	return f.style
}

// Compliant reports whether title already satisfies the format. A style with no
// pattern treats every title as compliant.
func (f Format) Compliant(title string) bool {
	if f.style == StyleConventional {
		return conventional.IsTitle(title)
	}
	if f.pattern == nil {
		return true
	}
	return f.pattern.MatchString(strings.TrimSpace(title))
}

// Normalize returns the title to use for a change on branch.
//
//  1. Trim. Empty in, empty out.
//  2. A title that already matches the pattern is returned byte-identical.
//     A compliant title is never rewritten.
//  3. Otherwise, when a branch token is configured and the branch yields a
//     capture, the template is rendered with that token and the title.
//  4. Otherwise the trimmed title is returned unchanged. A token is never
//     invented: a wrong story reference is worse than a missing one.
func (f Format) Normalize(title, branch string) string {
	if f.style == StyleConventional {
		return conventional.TightenTitle(title)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	if f.pattern == nil || f.pattern.MatchString(title) {
		return title
	}
	if f.branchToken == nil || f.tmpl == nil {
		return title
	}
	match := f.branchToken.FindStringSubmatch(branch)
	if len(match) != 2 || match[1] == "" {
		return title
	}
	// The rendered value becomes a PR title and a commit subject, so the
	// description is collapsed to a single line the same way commit.fix_message
	// collapses its summary.
	data := templateData{Token: match[1], Description: strings.Join(strings.Fields(title), " ")}
	rendered, err := render(f.tmpl, data)
	if err != nil || rendered == "" {
		return title
	}
	return rendered
}

// PromptRule is the format instruction handed to the drafting agent. It is
// empty when the style imposes no format.
func (f Format) PromptRule() string {
	return f.promptRule
}
