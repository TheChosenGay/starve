package gateway

import (
	"errors"
	"os"

	"github.com/golang-jwt/jwt/v5"
)

var ErrInvalidToken = errors.New("invalid token")

// TokenValidator isolates the gateway from a concrete account service.
// A remote account adapter can replace the local JWT implementation later.
type TokenValidator interface {
	Validate(token string) (userID string, err error)
}

// HMACTokenValidator validates the JWT contract currently shared with feeds.
type HMACTokenValidator struct {
	secret []byte
}

func NewHMACTokenValidator(secret []byte) *HMACTokenValidator {
	return &HMACTokenValidator{secret: append([]byte(nil), secret...)}
}

func NewHMACTokenValidatorFromEnv() *HMACTokenValidator {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "feeds-dev-secret"
	}
	return NewHMACTokenValidator([]byte(secret))
}

func (v *HMACTokenValidator) Validate(raw string) (string, error) {
	token, err := jwt.Parse(
		raw,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, ErrInvalidToken
			}
			return v.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	)
	if err != nil || !token.Valid {
		return "", ErrInvalidToken
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", ErrInvalidToken
	}
	userID, _ := claims["user_id"].(string)
	if userID == "" {
		return "", ErrInvalidToken
	}
	return userID, nil
}
