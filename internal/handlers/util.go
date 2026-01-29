package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
)

func getBearerToken(r *http.Request) string {
	val := r.Header.Get("Authorization")
	if val == "" {
		return ""
	}
	const prefix = "Bearer "
	if len(val) <= len(prefix) {
		return ""
	}
	if val[:len(prefix)] != prefix {
		return ""
	}
	return val[len(prefix):]
}

func whitelistKey(roundID int64) string {
	return "round:" + strconv.FormatInt(roundID, 10) + ":whitelist"
}

func scoreZSetKey(roundID int64) string {
	return "round:" + strconv.FormatInt(roundID, 10) + ":scores"
}

func scoreSumKey(roundID int64) string {
	return "round:" + strconv.FormatInt(roundID, 10) + ":score_sum"
}

func scoreMember(userID int64) string {
	return "u:" + strconv.FormatInt(userID, 10)
}

func onlineUsersKey() string {
	return "online:users"
}

func onlineUserIDsKey() string {
	return "online:user:ids"
}

func sessionKey(userID int64) string {
	return "session:uid:" + strconv.FormatInt(userID, 10)
}

var errInvalidSession = errors.New("invalid session")

func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "fallback-session"
	}
	return hex.EncodeToString(b)
}
