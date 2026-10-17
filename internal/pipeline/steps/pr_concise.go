package steps

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/kunchenguid/no-mistakes/internal/agent"
	"github.com/kunchenguid/no-mistakes/internal/git"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/scm"
)

var concisePRContentSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"title": {"type": "string", "description": "Concise pull request title following repository configuration"},
		"body": {"type": "string", "description": "Plain GitHub-flavored markdown description of the change, with no headings. Plain text, NOT JSON."}
	},
	"required": ["title", "body"]
}`)

// concisePRBodyRules is the drafting brief for pr.body: concise. The body is
// the only prose a reviewer sees, so the brief spends its words on what to
// leave out: testing, risk, and pipeline detail are recorded elsewhere.
const concisePRBodyRules = `- Body: a short description of the change for a reviewer who will read the diff next. It is the whole PR description; nothing else is added to it.
- Open with one or two plain sentences: what was wrong or missing, then what the branch does about it. Put no heading before them.
- Add bullets only when they help the reviewer find their way through the diff: at most three, each a single short sentence naming one concrete behavior or code change. Leave out minor polish a reviewer will see in the diff. A small change needs no bullets.
- Mention a design decision, or something deliberately left alone, only when a reviewer would otherwise question it or get it wrong. One sentence each, at most two. Most changes have none worth mentioning.
- Leave out testing, verification, screenshots, risk, review or pipeline results, file inventories, and how the work was done. Those are recorded elsewhere.
- Describe the branch as it stands against the base branch. Never narrate the work, and never refer to an author, a user, a requester, or an agent.
- Use short declarative sentences and ordinary verbs. Put an identifier in backticks only when the reviewer needs it to find the code.
- Use no headings, tables, images, collapsible blocks, horizontal rules, or emoji.
- Stay under 100 words; a small change needs far fewer. Before answering, count the words and delete every sentence the reviewer would not miss.
- The body value must be plain markdown text, never a JSON object or serialized JSON string.

Example body for a branch that moves a page's tab choice from local storage into the URL:

The Agents page kept its Connected / All tab only in local storage, so the tab couldn't be linked, and the dashboard's connected-agent counts opened whichever tab was last picked. The tab is now a ` + "`scope`" + ` query parameter, and those dashboard counts open the Connected tab.

- A tab named in the URL wins and is saved; without one, the saved tab is written into the URL.
- Switching tabs or changing filters keeps the other's query parameters.`

// draftConcisePRContent drafts a PR whose body is only the description of the
// change, followed by the invisible pipeline attestation that CI repairs and
// the required-check action read.
func (s *PRStep) draftConcisePRContent(sctx *pipeline.StepContext, branch, baseBranch, baseSHA string, provider scm.Provider, bodyLimit int) (prContent, error) {
	ctx := sctx.Ctx
	diffStat, _ := git.Run(ctx, sctx.WorkDir, "diff", "--stat", baseSHA+".."+sctx.Run.HeadSHA)
	if len(diffStat) > maxPRPromptInventoryBytes {
		diffStat, _ = git.Run(ctx, sctx.WorkDir, "diff", "--shortstat", baseSHA+".."+sctx.Run.HeadSHA)
	}
	finalDiff, err := git.Run(ctx, sctx.WorkDir, "diff", "--name-status", baseSHA+".."+sctx.Run.HeadSHA)
	if err != nil {
		return prContent{}, fmt.Errorf("read final branch diff: %w", err)
	}
	attestation := s.concisePRAttestation(sctx, provider)
	if (bodyLimit > 0 && scm.PRBodyLen(attestation) > bodyLimit) || len(attestation) > maxPullRequestBodyBytes {
		return prContent{}, fmt.Errorf("pipeline attestation exceeds PR body limit")
	}

	prompt := fmt.Sprintf(`Draft a pull request title and description for the full branch delta.

Context:
- branch: %s
- base commit: %s
- target commit: %s
- PR base branch: %s

Rules:
- Cover the full branch delta, not just the latest commit.
%s
%s
%s
- Derive every claim from the final diff. Inspect it directly when the paths and statuses below do not provide enough detail.
- Use the user intent, when present, to explain why the change exists. Do not copy it.
- Do not invent behavior.

Diff stat:
%s

Final diff paths and statuses:
%s%s%s`, branch, baseSHA, sctx.Run.HeadSHA, baseBranch, prTitlePromptRules(sctx), prTitleScopeRules(sctx), concisePRBodyRules, diffStat, prPromptInventory(sctx, finalDiff, baseSHA), prDraftIntentPromptSection(sctx), executionContextPromptSection(sctx.WorkDir))
	prompt += agent.MemoryFilesRule

	var content prContent
	result, err := sctx.RunAgentContext(ctx, agent.RunOpts{
		Prompt:     prompt,
		CWD:        sctx.WorkDir,
		JSONSchema: concisePRContentSchema,
		OnChunk:    sctx.LogChunk,
	})
	if err != nil {
		slog.Warn("agent failed for PR content, using fallback", "error", err)
	} else if result != nil && result.Output != nil {
		if err := json.Unmarshal(result.Output, &content); err != nil {
			slog.Warn("agent returned invalid PR content, using fallback", "error", err)
		}
	}
	content.Title = strings.TrimSpace(content.Title)
	content.Body = strings.TrimSpace(unwrapNestedPRBody(strings.TrimSpace(content.Body)))
	content.Body = neutralizeAttestationMarkers(provider, stripGeneratedSections(content.Body))
	if content.Title == "" || content.Body == "" {
		content = prContent{Title: "update pull request", Body: concisePRFallbackBody(provider, finalDiff)}
	}

	content.Title, err = renderPRTitle(sctx, content.Title)
	if err != nil {
		return prContent{}, err
	}
	content.Body = joinConcisePRBody(content.Body, attestation, bodyLimit)
	return content, nil
}

func concisePRFallbackBody(provider scm.Provider, finalDiff string) string {
	paths := strings.TrimSpace(finalDiff)
	if paths == "" {
		return "No description was generated and the final diff was unavailable."
	}
	return neutralizeAttestationMarkers(provider, "No description was generated. Changed paths:\n\n```text\n"+escapeMarkdownFence(paths)+"\n```")
}

// concisePRAttestation is the hidden attestation comment alone. Hosts whose
// bodies cannot hide an HTML comment get none, as in the full body.
func (s *PRStep) concisePRAttestation(sctx *pipeline.StepContext, provider scm.Provider) string {
	if prBodyFlavorFor(provider) != prBodyHTML {
		return ""
	}
	steps, rounds, ok := loadRunStepRounds(sctx)
	if !ok || len(steps) == 0 {
		return ""
	}
	return buildPipelineAttestationWithPolicy(steps, rounds, sctx.Run.HeadSHA, attestationPolicy(sctx))
}

// joinConcisePRBody trims the description, never the attestation, to fit the
// host's body limit.
func joinConcisePRBody(description, attestation string, bodyLimit int) string {
	if attestation == "" {
		description = truncateTextAtLineBoundary(description, maxPullRequestBodyBytes, essentialPRBodyTruncationMarker())
		return scm.ClampPRBody(description, bodyLimit)
	}
	const separator = "\n\n"
	description = truncateTextAtLineBoundary(description, maxPullRequestBodyBytes-len(separator)-len(attestation), essentialPRBodyTruncationMarker())
	if bodyLimit > 0 {
		description = scm.ClampPRBody(description, bodyLimit-scm.PRBodyLen(separator+attestation))
	}
	return description + separator + attestation
}
