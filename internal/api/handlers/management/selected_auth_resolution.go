package management

import (
	"strings"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

type selectedAuthPolicyRef struct {
	ID    string
	Index string
}

type selectedAuthResolution struct {
	auth       *coreauth.Auth
	resolvedBy string
	mismatch   bool
	missing    bool
}

func (h *Handler) liveAuths() []*coreauth.Auth {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	manager := h.authManager
	h.mu.Unlock()
	if manager == nil {
		return nil
	}
	return manager.List()
}

func canonicalizeSelectedAuthRef(auths []*coreauth.Auth, ref selectedAuthPolicyRef) selectedAuthPolicyRef {
	resolved := resolveSelectedAuthRef(auths, ref)
	if resolved.auth == nil {
		if strings.TrimSpace(ref.ID) == "" && strings.TrimSpace(ref.Index) == "" {
			return selectedAuthPolicyRef{}
		}
		return selectedAuthPolicyRef{
			ID:    strings.TrimSpace(ref.ID),
			Index: strings.TrimSpace(ref.Index),
		}
	}
	return selectedAuthPolicyRef{
		ID:    strings.TrimSpace(resolved.auth.ID),
		Index: strings.TrimSpace(resolved.auth.EnsureIndex()),
	}
}

func resolveSelectedAuthRef(auths []*coreauth.Auth, ref selectedAuthPolicyRef) selectedAuthResolution {
	ref.ID = strings.TrimSpace(ref.ID)
	ref.Index = strings.TrimSpace(ref.Index)
	if ref.ID == "" && ref.Index == "" {
		return selectedAuthResolution{}
	}
	byID := make(map[string]*coreauth.Auth, len(auths))
	byIndex := make(map[string]*coreauth.Auth, len(auths))
	for _, auth := range auths {
		if auth == nil {
			continue
		}
		if id := strings.TrimSpace(auth.ID); id != "" {
			byID[id] = auth
		}
		if index := strings.TrimSpace(auth.EnsureIndex()); index != "" {
			byIndex[index] = auth
		}
	}
	if ref.ID != "" {
		resolved, ok := byID[ref.ID]
		if !ok {
			return selectedAuthResolution{missing: true, mismatch: ref.Index != ""}
		}
		currentIndex := strings.TrimSpace(resolved.EnsureIndex())
		return selectedAuthResolution{
			auth:       resolved,
			resolvedBy: "id",
			mismatch:   ref.Index != "" && currentIndex != ref.Index,
		}
	}
	if resolved, ok := byIndex[ref.Index]; ok {
		return selectedAuthResolution{auth: resolved, resolvedBy: "index"}
	}
	return selectedAuthResolution{missing: true}
}
