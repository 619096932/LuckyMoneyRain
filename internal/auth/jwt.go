package auth

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	UserID  int64  `json:"uid"`
	Phone   string `json:"phone"`
	IsAdmin bool   `json:"admin"`
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

func GenerateToken(secret []byte, userID int64, phone string, isAdmin bool, sessionID string, ttl time.Duration) (string, error) {
	claims := Claims{
		UserID:  userID,
		Phone:   phone,
		IsAdmin: isAdmin,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

func ParseToken(secret []byte, tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}
	return nil, jwt.ErrTokenInvalidClaims
}
