package steps

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/agent"
	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/scm"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

const conciseTestDescription = "The export dropped rows past the first page. It now follows the cursor to the end.\n\n- `exportAll` pages until the cursor is empty."

func newConcisePRContext(t *testing.T, runFn func(context.Context, agent.RunOpts) (*agent.Result, error)) (*pipeline.StepContext, *mockAgent, string) {
	t.Helper()
	dir, base, head := setupGitRepo(t)
	ag := &mockAgent{name: "test", runFn: runFn}
	sctx := newTestContextWithDBRecords(t, ag, dir, base, head, config.Commands{})
	sctx.Config.PR.Body = config.PRBodyConcise
	sctx.UserIntent = "Exports of large accounts are missing rows."

	reviewFindings := `{"findings":[],"summary":"clean","risk_level":"medium","risk_rationale":"touches export paging"}`
	reviewStep, err := sctx.DB.InsertStepResult(sctx.Run.ID, types.StepReview)
	if err != nil {
		t.Fatal(err)
	}
	if err := sctx.DB.UpdateStepStatus(reviewStep.ID, types.StepStatusCompleted); err != nil {
		t.Fatal(err)
	}
	if err := sctx.DB.SetStepFindings(reviewStep.ID, reviewFindings); err != nil {
		t.Fatal(err)
	}
	if _, err := sctx.DB.InsertStepRound(reviewStep.ID, 1, "initial", &reviewFindings, nil, 500); err != nil {
		t.Fatal(err)
	}
	testFindings := `{"findings":[{"id":"t1","severity":"warning","description":"pre-existing flaky export spinner"}],"summary":"Drove three export scenarios live."}`
	testStep, err := sctx.DB.InsertStepResult(sctx.Run.ID, types.StepTest)
	if err != nil {
		t.Fatal(err)
	}
	if err := sctx.DB.UpdateStepStatus(testStep.ID, types.StepStatusCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := sctx.DB.InsertStepRound(testStep.ID, 1, "initial", &testFindings, nil, 800); err != nil {
		t.Fatal(err)
	}
	return sctx, ag, base
}

func conciseAgentReturning(body string) func(context.Context, agent.RunOpts) (*agent.Result, error) {
	return func(context.Context, agent.RunOpts) (*agent.Result, error) {
		payload, _ := json.Marshal(prContent{Title: "fix: export every page", Body: body})
		return &agent.Result{Output: payload}, nil
	}
}

func TestConcisePRBody_PublishesOnlyTheDescriptionAndAttestation(t *testing.T) {
	t.Parallel()
	sctx, ag, base := newConcisePRContext(t, conciseAgentReturning(conciseTestDescription))

	got, err := (&PRStep{}).buildPRContent(sctx, "feature", "main", base, scm.ProviderGitHub, 0)
	if err != nil {
		t.Fatal(err)
	}

	steps, rounds, _ := loadRunStepRounds(sctx)
	want := conciseTestDescription + "\n\n" + buildPipelineAttestation(steps, rounds, sctx.Run.HeadSHA)
	if got.Body != want {
		t.Fatalf("body =\n%s\n\nwant\n%s", got.Body, want)
	}
	prompt := ag.calls[len(ag.calls)-1].Prompt
	if !strings.Contains(prompt, sctx.UserIntent) {
		t.Error("the drafting prompt must still carry the user intent")
	}
	if strings.Contains(prompt, "## What Changed") {
		t.Error("the concise prompt must not ask for the full body's What Changed section")
	}
}

func TestConcisePRBody_StripsSectionsTheAgentAddsAnyway(t *testing.T) {
	t.Parallel()
	agentBody := conciseTestDescription + "\n\n## Testing\n\nRan everything.\n\n## Risk Assessment\n\nLow."
	sctx, _, base := newConcisePRContext(t, conciseAgentReturning(agentBody))

	got, err := (&PRStep{}).buildPRContent(sctx, "feature", "main", base, scm.ProviderGitHub, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"## Testing", "Ran everything", "## Risk Assessment", "## Intent", "## Pipeline", "flaky export spinner"} {
		if strings.Contains(got.Body, unwanted) {
			t.Errorf("concise body contains %q:\n%s", unwanted, got.Body)
		}
	}
}

func TestConcisePRBody_FallbackKeepsAttestation(t *testing.T) {
	t.Parallel()
	sctx, _, base := newConcisePRContext(t, func(context.Context, agent.RunOpts) (*agent.Result, error) {
		return nil, errors.New("draft unavailable")
	})

	got, err := (&PRStep{}).buildPRContent(sctx, "feature", "main", base, scm.ProviderGitHub, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.Body, "No description was generated. Changed paths:") {
		t.Errorf("fallback body should list changed paths:\n%s", got.Body)
	}
	if strings.Count(got.Body, pipelineAttestationCommentPrefix) != 1 {
		t.Errorf("fallback body must carry exactly one attestation:\n%s", got.Body)
	}
	if strings.Contains(got.Body, "## ") {
		t.Errorf("fallback body must not add sections:\n%s", got.Body)
	}
}

func TestConcisePRBody_AttestationRebindsAfterCIRepair(t *testing.T) {
	t.Parallel()
	sctx, _, base := newConcisePRContext(t, conciseAgentReturning(conciseTestDescription))
	got, err := (&PRStep{}).buildPRContent(sctx, "feature", "main", base, scm.ProviderGitHub, 0)
	if err != nil {
		t.Fatal(err)
	}

	newHead := strings.Repeat("b", 40)
	rebound, ok, err := rebindOwnedPRAttestation(got.Body, newHead, nil, pipelineAttestationPolicy{})
	if err != nil || !ok {
		t.Fatalf("rebind failed: ok=%v err=%v", ok, err)
	}
	if !strings.HasPrefix(rebound, conciseTestDescription) || !strings.Contains(rebound, newHead) {
		t.Errorf("rebind must keep the description and stamp the new head:\n%s", rebound)
	}
}

func TestConcisePRBody_BitbucketHasNoAttestation(t *testing.T) {
	t.Parallel()
	sctx, _, base := newConcisePRContext(t, conciseAgentReturning(conciseTestDescription))

	got, err := (&PRStep{}).buildPRContent(sctx, "feature", "main", base, scm.ProviderBitbucket, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != conciseTestDescription {
		t.Errorf("Bitbucket body =\n%s\n\nwant the description alone", got.Body)
	}
}

func TestJoinConcisePRBody_ClampsDescriptionAndKeepsAttestation(t *testing.T) {
	t.Parallel()
	limit := scm.MaxPRBodyChars(scm.ProviderAzureDevOps)
	steps := []*db.StepResult{{StepName: types.StepReview, Status: types.StepStatusCompleted}}
	attestation := buildPipelineAttestation(steps, nil, testPipelineHeadSHA)

	got := joinConcisePRBody(strings.Repeat("change 😀 ", 1000), attestation, limit)

	if scm.PRBodyLen(got) > limit {
		t.Fatalf("body = %d units, want <= %d", scm.PRBodyLen(got), limit)
	}
	if !strings.HasSuffix(got, "\n\n"+attestation) {
		t.Errorf("clamping must keep the attestation intact:\n%s", got)
	}
}
