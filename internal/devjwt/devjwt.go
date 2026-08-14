package devjwt

import (
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Mint 签发一个开发用 JWT（与 feeds user 服务同一签名密钥），供本地工具模拟登录。
// 正式登录由 feeds 的 user 服务发 token，这里只给 pomelo-client/stress 调试用。
func Mint(uid string) string {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "feeds-dev-secret"
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":  uid,
		"username": uid,
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
	})
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return ""
	}
	return signed
}
