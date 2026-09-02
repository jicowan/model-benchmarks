package api

import (
	"context"
	"net/http"

	"github.com/accelbench/accelbench/internal/auth"
)

// PRD-68 P4: per-object ownership.
//
// Route-level role gates (PRD-44/48) decide WHICH endpoints a role may hit;
// this file decides WHOSE objects a non-admin may mutate. Reads stay open to
// every authenticated role, matching PRD-48's "viewer sees everything"
// model — only cancel and delete are owner-scoped.

// principalSub returns the caller's stable user id, or "" when the request
// carries no principal (tests, or auth disabled before SetAuth ran).
func principalSub(ctx context.Context) *string {
	p := auth.PrincipalFromContext(ctx)
	if p == nil || p.Sub == "" {
		return nil
	}
	sub := p.Sub
	return &sub
}

// canMutate reports whether the caller may cancel/delete an object created
// by createdBy. Rules:
//   - admins may mutate anything;
//   - legacy rows (NULL created_by) may be mutated by any non-viewer —
//     there is nobody else to attribute them to;
//   - otherwise the caller's sub must match.
func canMutate(ctx context.Context, createdBy *string) bool {
	p := auth.PrincipalFromContext(ctx)
	if p == nil {
		// No principal at all only happens with auth fully disabled in tests;
		// the production AUTH_DISABLED path injects a synthetic admin.
		return true
	}
	if p.Role == "admin" {
		return true
	}
	if createdBy == nil || *createdBy == "" {
		return true
	}
	return p.Sub == *createdBy
}

// forbidNotOwner writes the standard 403 for a failed canMutate check.
func forbidNotOwner(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, "only the run's creator or an admin may do that")
}
