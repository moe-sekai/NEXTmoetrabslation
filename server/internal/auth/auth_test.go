package auth

import (
	"testing"
	"time"

	"moesekai/server/internal/db"
)

func openTestAuth(t *testing.T) *Auth {
	t.Helper()
	database, err := db.Open(t.TempDir() + "/auth.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return New(database, "jwt-secret", time.Hour)
}

func TestCreateAndAuthenticate(t *testing.T) {
	a := openTestAuth(t)
	if _, err := a.CreateUser("alice", "pw123", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	u, err := a.Authenticate("alice", "pw123")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if u.Role != RoleAdmin {
		t.Errorf("role: got %q", u.Role)
	}
	if _, err := a.Authenticate("alice", "wrong"); err != ErrInvalidCreds {
		t.Errorf("expected ErrInvalidCreds, got %v", err)
	}
}

func TestDuplicateUser(t *testing.T) {
	a := openTestAuth(t)
	a.CreateUser("bob", "pw", RoleEditor)
	if _, err := a.CreateUser("bob", "pw2", RoleEditor); err != ErrUserExists {
		t.Errorf("expected ErrUserExists, got %v", err)
	}
}

func TestJWTRoundTrip(t *testing.T) {
	a := openTestAuth(t)
	u, _ := a.CreateUser("carol", "pw", RoleEditor)
	token, _, err := a.IssueToken(u)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := a.VerifyToken(token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Username != "carol" || claims.Role != RoleEditor {
		t.Errorf("claims mismatch: %+v", claims)
	}
	if _, err := a.VerifyToken("garbage.token.here"); err == nil {
		t.Error("expected error for garbage token")
	}
}

func TestExpiredToken(t *testing.T) {
	database, _ := db.Open(t.TempDir() + "/exp.db")
	defer database.Close()
	a := New(database, "s", time.Hour)
	a.tokenTTL = -time.Hour // force already-expired tokens (bypasses New's clamp)
	u, _ := a.CreateUser("dave", "pw", RoleEditor)
	token, _, _ := a.IssueToken(u)
	if _, err := a.VerifyToken(token); err == nil {
		t.Error("expected expired token to fail verification")
	}
}

func TestLastAdminGuard(t *testing.T) {
	a := openTestAuth(t)
	a.CreateUser("admin1", "pw", RoleAdmin)
	a.CreateUser("editor1", "pw", RoleEditor)

	// Demoting the only admin must fail.
	if err := a.SetRole("admin1", RoleEditor); err != ErrLastAdmin {
		t.Errorf("expected ErrLastAdmin on demote, got %v", err)
	}
	// Deleting the only admin must fail.
	if err := a.DeleteUser("admin1"); err != ErrLastAdmin {
		t.Errorf("expected ErrLastAdmin on delete, got %v", err)
	}
	// With a second admin, demotion is allowed.
	a.CreateUser("admin2", "pw", RoleAdmin)
	if err := a.SetRole("admin1", RoleEditor); err != nil {
		t.Errorf("demote with 2 admins should succeed: %v", err)
	}
}

func TestWrongSecretRejected(t *testing.T) {
	a := openTestAuth(t)
	u, _ := a.CreateUser("eve", "pw", RoleEditor)
	token, _, _ := a.IssueToken(u)
	other := New(a.db, "different-secret", time.Hour)
	if _, err := other.VerifyToken(token); err == nil {
		t.Error("token signed with one secret must not verify under another")
	}
}
