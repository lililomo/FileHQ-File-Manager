package admin

import (
	"context"
	"sync"
	"testing"
)

// Review F (review E m-11): the "last active admin" rule must hold under
// concurrent demotions. Two admins demoting each other at the same time must
// leave at least one active admin, so the count is taken inside the write
// transaction (BEGIN IMMEDIATE serialises it).
func TestReviewFLastAdminRuleSurvivesConcurrentDemotion(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	second, _ := e.create("admin2", "admin")
	actor1 := e.actor
	actor2 := Actor{ID: second.ID, Username: "admin2", IP: "203.0.113.2", UserAgent: "test"}
	role, adminRole := "user", "admin"
	for round := 0; round < 25; round++ {
		var wg sync.WaitGroup
		var err1, err2 error
		wg.Add(2)
		go func() { defer wg.Done(); _, err1 = e.svc.UpdateUser(ctx, actor2, e.admin.ID, UpdateInput{Role: &role}) }()
		go func() { defer wg.Done(); _, err2 = e.svc.UpdateUser(ctx, actor1, second.ID, UpdateInput{Role: &role}) }()
		wg.Wait()
		n, err := e.store.CountActiveAdmins(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			t.Fatalf("round %d: no active admin left (err1=%v err2=%v)", round, err1, err2)
		}
		// restore both to admin for the next round
		for _, id := range []string{e.admin.ID, second.ID} {
			if _, err := e.svc.UpdateUser(ctx, actor1, id, UpdateInput{Role: &adminRole}); err != nil {
				t.Fatalf("restore %s: %v", id, err)
			}
		}
	}
}
