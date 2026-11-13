package coding

import (
	"path"
	"strings"
)

// Automation entrypoints can execute with repository CI credentials on push.
// Keep this provider independent. Indirect build scripts still require review.
func protectedAutomationPath(name string) bool {
	name = strings.ToLower(name)
	for _, prefix := range []string{".github/", ".gitlab/", ".circleci/", ".buildkite/", ".husky/", "scripts/", "ci/", ".devcontainer/"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	base := path.Base(name)
	for _, file := range []string{".gitlab-ci.yml", "jenkinsfile", "makefile", "gnumakefile", "justfile", "taskfile.yml", "taskfile.yaml", "bitbucket-pipelines.yml", "azure-pipelines.yml", "package.json", "dockerfile", "docker-compose.yml", "compose.yml"} {
		if base == file {
			return true
		}
	}
	return strings.HasSuffix(base, ".sh") || strings.HasSuffix(base, ".ps1") || strings.HasSuffix(base, ".bat") || strings.HasSuffix(base, ".cmd")
}
