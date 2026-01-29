package handlers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type sendSMSRequest struct {
	Phone string `json:"phone"`
}

type verifySMSRequest struct {
	Phone string `json:"phone"`
	Code  string `json:"code"`
}

func (s *Server) SendSMSCode(c *gin.Context) {
	var req sendSMSRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Phone == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid phone"})
		return
	}
	code := randomCode(6)
	key := "sms:code:" + req.Phone
	ctx := context.Background()
	if err := s.Redis.Set(ctx, key, code, 5*time.Minute).Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "redis error"})
		return
	}
	if _, err := s.SMS.SendCode(req.Phone, code); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sms send failed", "detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) VerifySMSCode(c *gin.Context) {
	var req verifySMSRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Phone == "" || req.Code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	ctx := context.Background()
	key := "sms:code:" + req.Phone
	val, err := s.Redis.Get(ctx, key).Result()
	if err != nil || val != req.Code {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid code"})
		return
	}
	_ = s.Redis.Del(ctx, key).Err()

	user, err := s.getOrCreateUser(req.Phone)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "user error"})
		return
	}

	token, err := s.SignToken(user.ID, user.Phone, user.IsAdmin)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token, "user": user})
}

func (s *Server) getOrCreateUser(phone string) (*userRow, error) {
	user, err := s.getUserByPhone(phone)
	if err == nil {
		return user, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	isAdmin := s.Cfg.AdminPhones[phone]
	res, err := s.DB.Exec(`INSERT INTO users (phone, nickname, avatar_url, is_admin, created_at, updated_at) VALUES (?, ?, ?, ?, NOW(), NOW())`,
		phone, "", "", boolToInt(isAdmin))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &userRow{ID: id, Phone: phone, IsAdmin: isAdmin}, nil
}

type userRow struct {
	ID        int64  `json:"id"`
	Phone     string `json:"phone"`
	Nickname  string `json:"nickname"`
	AvatarURL string `json:"avatar_url"`
	IsAdmin   bool   `json:"is_admin"`
}

func (s *Server) getUserByPhone(phone string) (*userRow, error) {
	row := s.DB.QueryRow(`SELECT id, phone, nickname, avatar_url, is_admin FROM users WHERE phone = ?`, phone)
	var u userRow
	var isAdmin int
	if err := row.Scan(&u.ID, &u.Phone, &u.Nickname, &u.AvatarURL, &isAdmin); err != nil {
		return nil, err
	}
	u.IsAdmin = isAdmin == 1
	return &u, nil
}

func (s *Server) getUserByID(uid int64) (*userRow, error) {
	row := s.DB.QueryRow(`SELECT id, phone, nickname, avatar_url, is_admin FROM users WHERE id = ?`, uid)
	var u userRow
	var isAdmin int
	if err := row.Scan(&u.ID, &u.Phone, &u.Nickname, &u.AvatarURL, &isAdmin); err != nil {
		return nil, err
	}
	u.IsAdmin = isAdmin == 1
	return &u, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func randomCode(n int) string {
	const digits = "0123456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "123456"
	}
	for i := range b {
		b[i] = digits[int(b[i])%len(digits)]
	}
	return string(b)
}

func (s *Server) GetMe(c *gin.Context) {
	userID := c.GetInt64("uid")
	user, err := s.getUserByID(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "user not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":         user.ID,
		"phone":      user.Phone,
		"nickname":   user.Nickname,
		"avatar_url": user.AvatarURL,
		"is_admin":   user.IsAdmin,
	})
}

const maxNicknameLen = 32

func normalizeNickname(val string) (string, error) {
	name := strings.TrimSpace(val)
	if name == "" {
		return "", nil
	}
	if len([]rune(name)) > maxNicknameLen {
		return "", errors.New("nickname too long")
	}
	return name, nil
}

func (s *Server) upsertUserProfile(phone, nickname, avatar string) (*userRow, error) {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return nil, errors.New("invalid phone")
	}
	nickname, err := normalizeNickname(nickname)
	if err != nil {
		return nil, err
	}
	avatar = strings.TrimSpace(avatar)
	if len(avatar) > 255 {
		return nil, errors.New("avatar_url too long")
	}
	user, err := s.getUserByPhone(phone)
	if err == sql.ErrNoRows {
		isAdmin := s.Cfg.AdminPhones[phone]
		res, err := s.DB.Exec(`INSERT INTO users (phone, nickname, avatar_url, is_admin, created_at, updated_at)
			VALUES (?, ?, ?, ?, NOW(), NOW())`, phone, nickname, avatar, boolToInt(isAdmin))
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		return &userRow{ID: id, Phone: phone, Nickname: nickname, AvatarURL: avatar, IsAdmin: isAdmin}, nil
	}
	if err != nil {
		return nil, err
	}
	updates := make([]string, 0, 2)
	args := make([]interface{}, 0, 3)
	if nickname != "" {
		updates = append(updates, "nickname=?")
		args = append(args, nickname)
	}
	if avatar != "" {
		updates = append(updates, "avatar_url=?")
		args = append(args, avatar)
	}
	if len(updates) > 0 {
		args = append(args, user.ID)
		query := "UPDATE users SET " + strings.Join(updates, ",") + ", updated_at=NOW() WHERE id=?"
		if _, err := s.DB.Exec(query, args...); err != nil {
			return nil, err
		}
		return s.getUserByID(user.ID)
	}
	return user, nil
}
