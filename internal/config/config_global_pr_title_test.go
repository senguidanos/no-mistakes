package config

import "testing"

func strPtr(s string) *string { return &s }

// TestMerge_GlobalPRTitleFormatApplies proves an operator can set the PR title
// convention once in the global config, without a .no-mistakes.yaml in every
// repository.
func TestMerge_GlobalPRTitleFormatApplies(t *testing.T) {
	got := Merge(
		&GlobalConfig{PR: GlobalPRRaw{TitleFormat: strPtr("[{{.Branch}}] {{.Title}}")}},
		&RepoConfig{},
	)
	if got.PR.TitleFormat != "[{{.Branch}}] {{.Title}}" {
		t.Errorf("global title_format must reach the resolved Config, got %q", got.PR.TitleFormat)
	}
}

// TestMerge_RepoPRTitleFormatWinsOverGlobal proves the global value is only a
// default: a repository that states its own convention still decides.
func TestMerge_RepoPRTitleFormatWinsOverGlobal(t *testing.T) {
	got := Merge(
		&GlobalConfig{PR: GlobalPRRaw{TitleFormat: strPtr("[{{.Branch}}] {{.Title}}")}},
		&RepoConfig{PR: PRRaw{TitleFormat: strPtr("{{.Branch}}: {{.Title}}")}},
	)
	if got.PR.TitleFormat != "{{.Branch}}: {{.Title}}" {
		t.Errorf("repo title_format must win over the global one, got %q", got.PR.TitleFormat)
	}
}

// TestMerge_NoPRTitleFormatLeavesItUnset proves nothing is imposed when neither
// side configures a convention, so upstream's default titling is unchanged.
func TestMerge_NoPRTitleFormatLeavesItUnset(t *testing.T) {
	got := Merge(&GlobalConfig{}, &RepoConfig{})
	if got.PR.TitleFormat != "" {
		t.Errorf("title_format must stay unset by default, got %q", got.PR.TitleFormat)
	}
}

// TestParseGlobalConfig_RejectsBadPRTitleFormat proves the global value is
// validated on the same terms as the repository one, so a broken template fails
// at load rather than at PR time.
func TestParseGlobalConfig_RejectsBadPRTitleFormat(t *testing.T) {
	_, err := LoadGlobalFromBytes([]byte("pr:\n  title_format: \"{{.Nope}}\"\n"))
	if err == nil {
		t.Fatal("a template with an unknown placeholder must fail to parse")
	}
}

// TestParseGlobalConfig_AcceptsPRTitleFormat proves the supported placeholders
// load from the global file.
func TestParseGlobalConfig_AcceptsPRTitleFormat(t *testing.T) {
	cfg, err := LoadGlobalFromBytes([]byte("pr:\n  title_format: \"[{{.Branch}}] {{.Title}}\"\n"))
	if err != nil {
		t.Fatalf("valid global title_format must parse: %v", err)
	}
	if cfg.PR.TitleFormat == nil || *cfg.PR.TitleFormat != "[{{.Branch}}] {{.Title}}" {
		t.Error("parsed global config must carry title_format")
	}
}
