package api

import (
	"context"
	"testing"

	"github.com/accelbench/accelbench/internal/auth"
)

func ctxWith(sub, role string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{Sub: sub, Email: sub + "@x", Role: role})
}

func TestCanMutate(t *testing.T) {
	owner := "user-1"
	cases := []struct {
		name      string
		ctx       context.Context
		createdBy *string
		want      bool
	}{
		{"admin any", ctxWith("admin-9", "admin"), &owner, true},
		{"owner", ctxWith("user-1", "user"), &owner, true},
		{"other user", ctxWith("user-2", "user"), &owner, false},
		{"viewer other", ctxWith("viewer-3", "viewer"), &owner, false},
		{"legacy row nil created_by", ctxWith("user-2", "user"), nil, true},
		{"legacy row empty created_by", ctxWith("user-2", "user"), strp(""), true},
		{"no principal", context.Background(), &owner, true},
	}
	for _, tc := range cases {
		if got := canMutate(tc.ctx, tc.createdBy); got != tc.want {
			t.Errorf("%s: canMutate=%v want %v", tc.name, got, tc.want)
		}
	}
}

func TestPrincipalSub(t *testing.T) {
	if principalSub(context.Background()) != nil {
		t.Fatal("expected nil without principal")
	}
	if s := principalSub(ctxWith("abc", "user")); s == nil || *s != "abc" {
		t.Fatalf("got %v", s)
	}
}
