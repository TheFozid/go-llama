package auth

import (
	"testing"
	"time"
)

const testSecret = "my_test_jwt_secret"

func TestGenerateAndParseJWT(t *testing.T) {
	userId := uint(42)
	username := "testuser"
	role := "user"
	exp := time.Hour

	// Generate token
	tokenString, err := GenerateJWT(testSecret, userId, username, role, exp)
	if err != nil {
		t.Fatalf("failed to generate JWT: %v", err)
	}
	if tokenString == "" {
		t.Fatalf("empty token string")
	}

	// Parse and validate token
	claims, err := ParseJWT(testSecret, tokenString)
	if err != nil {
		t.Fatalf("failed to parse JWT: %v", err)
	}
	if claims.UserID != userId {
		t.Errorf("expected userId=%d, got %d", userId, claims.UserID)
	}
	if claims.Username != username {
		t.Errorf("expected username=%s, got %s", username, claims.Username)
	}
	if claims.Role != role {
		t.Errorf("expected role=%s, got %s", role, claims.Role)
	}
	if claims.ExpiresAt == nil || claims.ExpiresAt.Time.Before(time.Now()) {
		t.Errorf("token should not be expired, got expiresAt=%v", claims.ExpiresAt)
	}
}

func TestParseJWT_InvalidToken(t *testing.T) {
	invalidToken := "this.is.not.a.valid.jwt"
	_, err := ParseJWT(testSecret, invalidToken)
	if err == nil {
		t.Errorf("expected error for invalid JWT, got nil")
	}
}

func TestParseJWT_WrongSecret(t *testing.T) {
	userId := uint(99)
	username := "wrongsecret"
	role := "admin"
	exp := time.Hour

	tokenString, err := GenerateJWT(testSecret, userId, username, role, exp)
	if err != nil {
		t.Fatalf("failed to generate JWT: %v", err)
	}

	_, err = ParseJWT("totally_wrong_secret", tokenString)
	if err == nil {
		t.Errorf("expected error for wrong secret, got nil")
	}
}
