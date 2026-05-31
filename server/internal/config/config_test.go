package config

import (
	"testing"

	"moesekai/server/internal/db"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(t.TempDir() + "/config.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestSecretEncryption(t *testing.T) {
	database := openTestDB(t)
	c, err := New(database, "master-key-123")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Set(KeyOpenAIAPIKey, "sk-secret-value"); err != nil {
		t.Fatal(err)
	}
	// Stored value in DB must NOT be the plaintext.
	var stored string
	var enc int
	if err := database.QueryRow(`SELECT value, encrypted FROM settings WHERE key = ?`,
		KeyOpenAIAPIKey).Scan(&stored, &enc); err != nil {
		t.Fatal(err)
	}
	if enc != 1 {
		t.Errorf("expected encrypted=1, got %d", enc)
	}
	if stored == "sk-secret-value" {
		t.Error("secret stored in plaintext")
	}
	// A fresh Config with the same key must decrypt it.
	c2, err := New(database, "master-key-123")
	if err != nil {
		t.Fatal(err)
	}
	if got := c2.Get(KeyOpenAIAPIKey); got != "sk-secret-value" {
		t.Errorf("decrypt mismatch: got %q", got)
	}
}

func TestNonSecretPlaintext(t *testing.T) {
	database := openTestDB(t)
	c, _ := New(database, "k")
	if err := c.Set(KeyLLMType, "openai"); err != nil {
		t.Fatal(err)
	}
	var stored string
	var enc int
	database.QueryRow(`SELECT value, encrypted FROM settings WHERE key = ?`, KeyLLMType).Scan(&stored, &enc)
	if enc != 0 || stored != "openai" {
		t.Errorf("non-secret should be plaintext: enc=%d val=%q", enc, stored)
	}
}

func TestSeedIfAbsent(t *testing.T) {
	database := openTestDB(t)
	c, _ := New(database, "k")
	ok, err := c.SetIfAbsent(KeyUpstreamRepo, "owner/repo")
	if err != nil || !ok {
		t.Fatalf("first seed should write: ok=%v err=%v", ok, err)
	}
	// Simulate admin override, then re-seed: must not overwrite.
	c.Set(KeyUpstreamRepo, "admin/changed")
	ok, _ = c.SetIfAbsent(KeyUpstreamRepo, "owner/repo")
	if ok {
		t.Error("second seed should not overwrite existing value")
	}
	if c.Get(KeyUpstreamRepo) != "admin/changed" {
		t.Errorf("admin value lost: %q", c.Get(KeyUpstreamRepo))
	}
}

func TestSecretWithoutMasterKey(t *testing.T) {
	database := openTestDB(t)
	c, _ := New(database, "") // no master key
	if err := c.Set(KeyOpenAIAPIKey, "x"); err == nil {
		t.Error("expected error storing secret without master key")
	}
}

func TestAllMasksSecrets(t *testing.T) {
	database := openTestDB(t)
	c, _ := New(database, "k")
	c.Set(KeyOpenAIAPIKey, "sk-xyz")
	c.Set(KeyLLMType, "openai")
	masked := c.All(false)
	if masked[KeyOpenAIAPIKey] != "********" {
		t.Errorf("secret not masked: %q", masked[KeyOpenAIAPIKey])
	}
	if masked[KeyLLMType] != "openai" {
		t.Errorf("non-secret should not be masked: %q", masked[KeyLLMType])
	}
	revealed := c.All(true)
	if revealed[KeyOpenAIAPIKey] != "sk-xyz" {
		t.Errorf("reveal failed: %q", revealed[KeyOpenAIAPIKey])
	}
}
