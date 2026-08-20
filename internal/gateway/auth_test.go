package gateway

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func TestHMACTokenValidator(t *testing.T) {
	secret := []byte("test-secret")
	validator := NewHMACTokenValidator(secret)

	valid := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": "u-42"})
	raw, err := valid.SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := validator.Validate(raw)
	if err != nil || userID != "u-42" {
		t.Fatalf("Validate() = %q, %v", userID, err)
	}

	missingUser := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"role": "player"})
	raw, err = missingUser.SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validator.Validate(raw); err == nil {
		t.Fatal("token without user_id should be rejected")
	}

	wrongSecret := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": "u-42"})
	raw, err = wrongSecret.SignedString([]byte("other-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validator.Validate(raw); err == nil {
		t.Fatal("token with wrong secret should be rejected")
	}
}
