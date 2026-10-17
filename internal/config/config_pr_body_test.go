package config

import "testing"

func TestMerge_PRBodyDefaultsToFull(t *testing.T) {
	got := Merge(&GlobalConfig{}, &RepoConfig{})
	if got.PR.ConciseBody() {
		t.Error("an unconfigured pr.body must keep the full body")
	}
}

func TestMerge_GlobalPRBodyApplies(t *testing.T) {
	got := Merge(&GlobalConfig{PR: GlobalPRRaw{Body: strPtr(PRBodyConcise)}}, &RepoConfig{})
	if !got.PR.ConciseBody() {
		t.Errorf("global pr.body must reach the resolved Config, got %q", got.PR.Body)
	}
}

func TestMerge_RepoPRBodyWinsOverGlobal(t *testing.T) {
	got := Merge(
		&GlobalConfig{PR: GlobalPRRaw{Body: strPtr(PRBodyConcise)}},
		&RepoConfig{PR: PRRaw{Body: strPtr(PRBodyFull)}},
	)
	if got.PR.ConciseBody() {
		t.Errorf("repo pr.body must win over the global one, got %q", got.PR.Body)
	}
}

func TestParseGlobalConfig_PRBody(t *testing.T) {
	cfg, err := LoadGlobalFromBytes([]byte("pr:\n  body: concise\n"))
	if err != nil {
		t.Fatalf("valid global pr.body must parse: %v", err)
	}
	if cfg.PR.Body == nil || *cfg.PR.Body != PRBodyConcise {
		t.Error("parsed global config must carry pr.body")
	}
	if _, err := LoadGlobalFromBytes([]byte("pr:\n  body: terse\n")); err == nil {
		t.Error("an unknown global pr.body must fail to parse")
	}
}

func TestParseRepoConfig_PRBody(t *testing.T) {
	cfg, err := LoadRepoFromBytes([]byte("pr:\n  body: concise\n"))
	if err != nil {
		t.Fatalf("valid repo pr.body must parse: %v", err)
	}
	if cfg.PR.Body == nil || *cfg.PR.Body != PRBodyConcise {
		t.Error("parsed repo config must carry pr.body")
	}
	if _, err := LoadRepoFromBytes([]byte("pr:\n  body: terse\n")); err == nil {
		t.Error("an unknown repo pr.body must fail to parse")
	}
}
