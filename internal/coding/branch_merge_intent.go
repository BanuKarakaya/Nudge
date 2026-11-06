package coding

import (
	"regexp"
	"strings"
)

type branchMergeIntent struct {
	Provider, Repository, Source, Target string
	Current                              bool
}

const mergeCourtesy = `(?i:(?:tamam[,\.]?\s+)?(?:bence olmuş[,\.]?\s+)?(?:lütfen\s+)?)`
const mergeVerb = `(?i:merge et|mergele|birleştir|mergeleyebilir misin|merge eder misin|birleştirir misin)[?!.]?`
const targetSuffix = `(?:\s+(?i:dalına|branchine|branch'ine|branch’ine|branch ine)|['’](?i:a|e))`

var namedBranchMerge = regexp.MustCompile(`^` + mergeCourtesy + `(?i:(github|gitlab))\s+(\S+)\s+(\S+)\s+(?:->|(?i:into))\s+(\S+)\s+` + mergeVerb + `$`)
var namedNaturalMerge = regexp.MustCompile(`^` + mergeCourtesy + `(?i:(github|gitlab))\s+(\S+)\s+(\S+)\s+(?i:dalını|branchini|branchi)\s+(\S+?)` + targetSuffix + `\s+` + mergeVerb + `$`)
var currentBranchMerge = regexp.MustCompile(`^` + mergeCourtesy + `(?i:bu dalı|bu branchi|bu branchı|bu branchini|bu branch'i|bu branch’i)\s+(\S+?)` + targetSuffix + `\s+` + mergeVerb + `$`)
var contextualNamedBranchMerge = regexp.MustCompile(`^` + mergeCourtesy + `(\S+)\s+(?i:bu dalı|bu branchi|bu branchı|bu branchini|dalını|branchini)\s+(\S+?)` + targetSuffix + `\s+` + mergeVerb + `$`)

func branchToken(value string) string {
	if strings.HasPrefix(value, "`") && strings.HasSuffix(value, "`") && len(value) > 2 {
		value = value[1 : len(value)-1]
	}
	return value
}

// An anchored grammar preserves branch case and rejects extra commands, quoted
// instructions, conditions and negations. Only the signed current message enters.
func parseBranchMerge(request string) (branchMergeIntent, bool) {
	text := normalizeMergeText(request)
	if m := currentBranchMerge.FindStringSubmatch(text); m != nil {
		i := branchMergeIntent{Current: true, Target: branchToken(m[1])}
		return i, ValidBranch(i.Target)
	}
	if m := contextualNamedBranchMerge.FindStringSubmatch(text); m != nil {
		i := branchMergeIntent{Current: true, Source: m[1], Target: m[2]}
		switch strings.ToLower(i.Source) {
		case "sakın", "asla", "örnek", "örneğin", "example", "quote", "not", "don't":
			return branchMergeIntent{}, false
		}
		return i, ValidBranch(i.Source) && ValidBranch(i.Target) && i.Source != i.Target
	}
	for _, pattern := range []*regexp.Regexp{namedBranchMerge, namedNaturalMerge} {
		if m := pattern.FindStringSubmatch(text); m != nil {
			i := branchMergeIntent{Provider: strings.ToLower(m[1]), Repository: m[2], Source: branchToken(m[3]), Target: branchToken(m[4])}
			validRepo := ValidRepository(i.Repository)
			if i.Provider == "gitlab" {
				validRepo = ValidGitLabRepository(i.Repository)
			}
			return i, validRepo && ValidBranch(i.Source) && ValidBranch(i.Target) && i.Source != i.Target
		}
	}
	return branchMergeIntent{}, false
}
