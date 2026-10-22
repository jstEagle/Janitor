package janitor

import (
	"path/filepath"
	"sort"
	"strings"
)

// A symlink in one discovered workspace can make a different workspace's
// installation live infrastructure. Retain the destination workspace even when
// the consumer is not enrolled, pinned, or too young for cleanup. Do not follow
// links during discovery: use their lexical destinations, including broken links.
func protectInboundReferences(workspaces []Workspace) {
	owners := map[string]int{}
	paths := make([]string, 0, len(workspaces))
	for i, w := range workspaces {
		owners[w.Path] = i
		paths = append(paths, w.Path)
	}
	sort.Strings(paths)
	for _, source := range workspaces {
		if source.referencesUnknown {
			for i := range workspaces {
				if workspaces[i].Path != source.Path {
					workspaces[i].Blocked = append(workspaces[i].Blocked, "inbound reference check incomplete: "+source.Path)
				}
			}
			continue
		}
		referenced := map[int]bool{}
		for destination := range source.linkTargets {
			// Find containing workspaces without comparing every link to every
			// workspace. Large package installations can contain thousands of links.
			for parent := destination; ; parent = filepath.Dir(parent) {
				if i, ok := owners[parent]; ok {
					referenced[i] = true
				}
				if filepath.Dir(parent) == parent {
					break
				}
			}
			// A link to a common ancestor also exposes its descendant workspaces.
			prefix := strings.TrimSuffix(destination, string(filepath.Separator)) + string(filepath.Separator)
			for i := sort.SearchStrings(paths, prefix); i < len(paths) && strings.HasPrefix(paths[i], prefix); i++ {
				referenced[owners[paths[i]]] = true
			}
		}
		for i := range workspaces {
			if referenced[i] && workspaces[i].Path != source.Path {
				workspaces[i].Blocked = append(workspaces[i].Blocked, "referenced by another workspace: "+source.Path)
			}
		}
	}
}
