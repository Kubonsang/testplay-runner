package main

import (
	"os"
	"path/filepath"

	"github.com/Kubonsang/testplay-runner/internal/config"
)

// projectFileInfo obtains the operating system identity of an existing project
// directory. os.Stat follows symlinks and Windows junctions; os.SameFile can
// then compare the underlying volume/file IDs instead of guessing from path
// spelling, case, or drive aliases.
func projectFileInfo(path string) (os.FileInfo, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, false
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return nil, false
	}
	return info, true
}

// sameProjectPath reports both the comparison and whether it could be proven.
// UNKNOWN is never converted into a lexical guess.
func sameProjectPath(a, b string) (same bool, known bool) {
	aInfo, aOK := projectFileInfo(a)
	bInfo, bOK := projectFileInfo(b)
	if !aOK || !bOK {
		return false, false
	}
	return os.SameFile(aInfo, bInfo), true
}

// sharedProjectRoles reports which scenario roles require their own shadow
// workspace. An identity used by multiple roles is shared. An identity that
// cannot be proven is UNKNOWN and is isolated rather than assumed distinct.
func sharedProjectRoles(configs map[string]*config.Config) map[string]bool {
	identities := make(map[string]os.FileInfo, len(configs))
	shared := make(map[string]bool, len(configs))

	for role, cfg := range configs {
		if cfg == nil {
			shared[role] = true
			continue
		}
		identity, ok := projectFileInfo(cfg.ProjectPath)
		if !ok {
			shared[role] = true
			continue
		}
		identities[role] = identity
	}

	for role, identity := range identities {
		for otherRole, otherIdentity := range identities {
			if role != otherRole && os.SameFile(identity, otherIdentity) {
				shared[role] = true
				break
			}
		}
	}
	return shared
}
