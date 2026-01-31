package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
)

type bindAlipayRequest struct {
	Name    string `json:"name"`
	Account string `json:"account"`
	IDCard  string `json:"id_card"`
}

type withdrawRequest struct {
	Amount    int64  `json:"amount"`
	RequestID string `json:"request_id"`
}

func (s *Server) GetWallet(c *gin.Context) {
	uid := c.GetInt64("uid")
	balance := int64(0)
	row := s.DB.QueryRow(`SELECT balance FROM wallets WHERE user_id=?`, uid)
	if err := row.Scan(&balance); err != nil && err != sql.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	resp := gin.H{"balance": balance}
	resp["withdraw_enabled"] = s.IsWithdrawEnabled()
	if acc, _ := s.getAlipayAccount(uid); acc != nil {
		resp["alipay"] = gin.H{
			"name":           acc.Name,
			"account_masked": maskKeep(acc.Account, 3, 3),
			"id_card_masked": maskKeep(acc.IDCard, 3, 4),
			"bound":          true,
		}
	} else {
		resp["alipay"] = gin.H{"bound": false}
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) BindAlipay(c *gin.Context) {
	var req bindAlipayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Account = strings.TrimSpace(req.Account)
	req.IDCard = strings.TrimSpace(req.IDCard)
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "姓名为必填"})
		return
	}
	if req.Account == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "支付宝账号为必填"})
		return
	}
	uid := c.GetInt64("uid")
	_, err := s.DB.Exec(`INSERT INTO user_alipay_accounts (user_id, name, account, id_card, created_at, updated_at)
		VALUES (?, ?, ?, ?, NOW(), NOW())
		ON DUPLICATE KEY UPDATE name=VALUES(name), account=VALUES(account), id_card=VALUES(id_card), updated_at=NOW()`,
		uid, req.Name, req.Account, req.IDCard)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) CreateWithdraw(c *gin.Context) {
	var req withdrawRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid amount"})
		return
	}
	if req.Amount < 10 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "min withdraw is 0.1 yuan"})
		return
	}
	if !s.IsWithdrawEnabled() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "withdraw disabled"})
		return
	}
	uid := c.GetInt64("uid")
	acc, err := s.getAlipayAccount(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	if acc == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alipay not bound"})
		return
	}
	reqID := normalizeRequestID(req.RequestID)
	if reqID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "request_id required"})
		return
	}
	outBizNo := buildOutBizNo(uid, reqID)
	if outBizNo == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request_id"})
		return
	}
	autoPay := s.autoWithdrawEnabled(req.Amount)
	status := WithdrawStatusPending
	autoPayFlag := 0
	if autoPay {
		autoPayFlag = 1
	}

	tx, err := s.DB.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	var balance int64
	row := tx.QueryRow(`SELECT balance FROM wallets WHERE user_id=? FOR UPDATE`, uid)
	if err := row.Scan(&balance); err != nil {
		_ = tx.Rollback()
		if err == sql.ErrNoRows {
			c.JSON(http.StatusBadRequest, gin.H{"error": "insufficient balance"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}

	var existingID int64
	var existingStatus string
	var existingAmount int64
	row = tx.QueryRow(`SELECT id, status, amount FROM withdraw_requests WHERE user_id=? AND out_biz_no=? ORDER BY id DESC LIMIT 1 FOR UPDATE`,
		uid, outBizNo)
	if err := row.Scan(&existingID, &existingStatus, &existingAmount); err == nil {
		if err := tx.Commit(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":  strings.ToLower(existingStatus),
			"id":      existingID,
			"balance": balance,
			"amount":  existingAmount,
		})
		return
	} else if err != sql.ErrNoRows {
		_ = tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}

	if balance < req.Amount {
		_ = tx.Rollback()
		c.JSON(http.StatusBadRequest, gin.H{"error": "insufficient balance"})
		return
	}

	newBalance := balance - req.Amount
	if _, err := tx.Exec(`UPDATE wallets SET balance=?, updated_at=NOW() WHERE user_id=?`, newBalance, uid); err != nil {
		_ = tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}

	res, err := tx.Exec(`INSERT INTO withdraw_requests (user_id, amount, status, out_biz_no, auto_pay, alipay_name, alipay_account, id_card, next_attempt_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		uid, req.Amount, status, outBizNo, autoPayFlag, acc.Name, acc.Account, acc.IDCard, sql.NullTime{Valid: autoPay, Time: time.Now()})
	if err != nil {
		if isDuplicateKey(err) {
			_ = tx.Rollback()
			var existingID int64
			var existingStatus string
			var existingAmount int64
			row := s.DB.QueryRow(`SELECT id, status, amount FROM withdraw_requests WHERE user_id=? AND out_biz_no=? ORDER BY id DESC LIMIT 1`,
				uid, outBizNo)
			if scanErr := row.Scan(&existingID, &existingStatus, &existingAmount); scanErr == nil {
				currentBalance := int64(0)
				_ = s.DB.QueryRow(`SELECT balance FROM wallets WHERE user_id=?`, uid).Scan(&currentBalance)
				c.JSON(http.StatusOK, gin.H{
					"status":  strings.ToLower(existingStatus),
					"id":      existingID,
					"balance": currentBalance,
					"amount":  existingAmount,
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
			return
		}
		_ = tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	withdrawID, _ := res.LastInsertId()
	if _, err := tx.Exec(`INSERT INTO wallet_ledger (user_id, amount, reason, ref_id, created_at) VALUES (?, ?, ?, ?, NOW())`,
		uid, -req.Amount, "WITHDRAW", withdrawID); err != nil {
		_ = tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":  strings.ToLower(status),
		"id":      withdrawID,
		"balance": newBalance,
		"amount":  req.Amount,
	})
}

func (s *Server) ListWithdraws(c *gin.Context) {
	uid := c.GetInt64("uid")
	rows, err := s.DB.Query(`SELECT id, amount, status, alipay_status, fail_reason, alipay_account, id_card, created_at, paid_at
		FROM withdraw_requests WHERE user_id=? ORDER BY id DESC LIMIT 20`, uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	defer rows.Close()
	list := make([]gin.H, 0)
	for rows.Next() {
		var id int64
		var amount int64
		var status, account, idCard string
		var aliStatus, failReason sql.NullString
		var createdAt time.Time
		var paidAt sql.NullTime
		if err := rows.Scan(&id, &amount, &status, &aliStatus, &failReason, &account, &idCard, &createdAt, &paidAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
			return
		}
		item := gin.H{
			"id":             id,
			"amount":         amount,
			"status":         status,
			"alipay_status":  aliStatus.String,
			"fail_reason":    failReason.String,
			"account_masked": maskKeep(account, 3, 3),
			"id_card_masked": maskKeep(idCard, 3, 4),
			"created_at":     createdAt.UnixMilli(),
		}
		if paidAt.Valid {
			item["paid_at"] = paidAt.Time.UnixMilli()
		}
		list = append(list, item)
	}
	c.JSON(http.StatusOK, gin.H{"items": list})
}

type alipayAccount struct {
	Name    string
	Account string
	IDCard  string
}

func (s *Server) getAlipayAccount(uid int64) (*alipayAccount, error) {
	row := s.DB.QueryRow(`SELECT name, account, id_card FROM user_alipay_accounts WHERE user_id=?`, uid)
	var acc alipayAccount
	if err := row.Scan(&acc.Name, &acc.Account, &acc.IDCard); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &acc, nil
}

func maskKeep(s string, head, tail int) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= head+tail {
		return s
	}
	masked := make([]rune, 0, len(runes))
	masked = append(masked, runes[:head]...)
	for i := 0; i < len(runes)-head-tail; i++ {
		masked = append(masked, '*')
	}
	masked = append(masked, runes[len(runes)-tail:]...)
	return string(masked)
}

func normalizeRequestID(val string) string {
	val = strings.TrimSpace(val)
	if val == "" {
		return ""
	}
	buf := make([]byte, 0, len(val))
	for i := 0; i < len(val); i++ {
		ch := val[i]
		if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') {
			buf = append(buf, ch)
		}
	}
	if len(buf) == 0 {
		return ""
	}
	if len(buf) > 32 {
		buf = buf[:32]
	}
	return string(buf)
}

func buildOutBizNo(uid int64, reqID string) string {
	reqID = normalizeRequestID(reqID)
	if reqID == "" {
		return ""
	}
	return fmt.Sprintf("HB%d%s", uid, reqID)
}

func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1062
	}
	return strings.Contains(err.Error(), "Duplicate entry")
}
