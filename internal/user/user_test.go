package user

import (
	"testing"
)

func TestPasswordHashing(t *testing.T) {
	pw := "supersecret"
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("hash error: %v", err)
	}
	if err := CheckPassword(hash, pw); err != nil {
		t.Errorf("check should succeed: %v", err)
	}
	if err := CheckPassword(hash, "wrongpw"); err == nil {
		t.Errorf("expected failure for wrong password")
	}
}
