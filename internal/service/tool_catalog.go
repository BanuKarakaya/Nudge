package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// Native model threads retain the tools supplied when they were created.
// Refresh them when an integration is added/removed or its schema changes.
func ToolCatalogFingerprint(tools AgentTools) (string, error) {
	defs := append([]ToolDefinition(nil), tools.Definitions()...)
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	raw, err := json.Marshal(defs)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
