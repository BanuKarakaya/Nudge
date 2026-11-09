package coding

import "context"

// MergeIntent is resolved independently of the executing agent's tool arguments.
// Only the current user's request grants authority; history resolves references.
type MergeIntent struct {
	Action       string `json:"action"` // merge, none, or clarify
	Provider     string `json:"provider"`
	Repository   string `json:"repository"`
	Number       int    `json:"number"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
}

func (i MergeIntent) Valid() bool {
	if i.Action == "none" || i.Action == "clarify" {
		return i.Provider == "" && i.Repository == "" && i.Number == 0 && i.SourceBranch == "" && i.TargetBranch == ""
	}
	if i.Action != "merge" || (i.Provider != "github" && i.Provider != "gitlab") {
		return false
	}
	validRepo := ValidRepository(i.Repository)
	if i.Provider == "gitlab" {
		validRepo = ValidGitLabRepository(i.Repository)
	}
	if !validRepo {
		return false
	}
	return (i.Number > 0 && i.SourceBranch == "" && i.TargetBranch == "") ||
		(i.Number == 0 && ValidBranch(i.SourceBranch) && ValidBranch(i.TargetBranch) && i.SourceBranch != i.TargetBranch)
}

type mergeResolverKey struct{}
type MergeIntentResolver func(context.Context, Identity) (MergeIntent, error)

// WithMergeIntentResolver is installed by the host for one signed Slack event.
// It is neither a model tool nor a serializable authorization argument.
func WithMergeIntentResolver(ctx context.Context, resolve MergeIntentResolver) context.Context {
	return context.WithValue(ctx, mergeResolverKey{}, resolve)
}

func semanticMergeIntent(ctx context.Context, i Identity) (MergeIntent, bool, error) {
	resolve, ok := ctx.Value(mergeResolverKey{}).(MergeIntentResolver)
	if !ok {
		return MergeIntent{}, false, nil
	}
	intent, err := resolve(ctx, i)
	if err != nil || !intent.Valid() {
		return MergeIntent{}, true, ErrMergeIntentUnavailable
	}
	return intent, true, nil
}
