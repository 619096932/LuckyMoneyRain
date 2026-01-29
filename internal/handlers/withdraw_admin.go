package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"hongbao/internal/payments"
)

func (s *Server) ListWithdrawsAdmin(c *gin.Context) {
	status := strings.TrimSpace(c.Query("status"))
	limit := parseIntWithDefault(c.Query("limit"), 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := parseIntWithDefault(c.Query("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	query := `SELECT id, user_id, amount, status, out_biz_no, alipay_status, alipay_order_id, alipay_fund_order_id,
		alipay_name, alipay_account, id_card, fail_reason, created_at, updated_at, paid_at
		FROM withdraw_requests`
	args := make([]interface{}, 0)
	if status != "" {
		query += " WHERE status=?"
		args = append(args, status)
	}
	query += " ORDER BY id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	defer rows.Close()

	items := make([]gin.H, 0)
	userIDs := make([]int64, 0)
	for rows.Next() {
		var (
			id, uid, amount                             int64
			status, outBizNo, name, account, idCard     string
			aliStatus, orderId, fundOrderId, failReason sql.NullString
			createdAt, updatedAt                        time.Time
			paidAt                                      sql.NullTime
		)
		if err := rows.Scan(&id, &uid, &amount, &status, &outBizNo, &aliStatus, &orderId, &fundOrderId,
			&name, &account, &idCard, &failReason, &createdAt, &updatedAt, &paidAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
			return
		}
		item := gin.H{
			"id":                   id,
			"user_id":              uid,
			"amount":               amount,
			"status":               status,
			"out_biz_no":           outBizNo,
			"alipay_status":        aliStatus.String,
			"alipay_order_id":      orderId.String,
			"alipay_fund_order_id": fundOrderId.String,
			"alipay_name":          name,
			"alipay_account":       account,
			"id_card":              idCard,
			"fail_reason":          failReason.String,
			"created_at":           createdAt.UnixMilli(),
			"updated_at":           updatedAt.UnixMilli(),
		}
		if paidAt.Valid {
			item["paid_at"] = paidAt.Time.UnixMilli()
		}
		items = append(items, item)
		userIDs = append(userIDs, uid)
	}
	if len(userIDs) > 0 {
		query := `SELECT id, phone, nickname, avatar_url FROM users WHERE id IN (` + strings.TrimRight(strings.Repeat("?,", len(userIDs)), ",") + `)`
		args := make([]interface{}, len(userIDs))
		for i, v := range userIDs {
			args[i] = v
		}
		type userInfo struct {
			Phone     string
			Nickname  string
			AvatarURL string
		}
		infoMap := map[int64]userInfo{}
		if rows, err := s.DB.Query(query, args...); err == nil {
			defer rows.Close()
			for rows.Next() {
				var id int64
				var phone, nickname, avatarURL string
				if err := rows.Scan(&id, &phone, &nickname, &avatarURL); err == nil {
					infoMap[id] = userInfo{Phone: phone, Nickname: nickname, AvatarURL: avatarURL}
				}
			}
		}
		for _, item := range items {
			if uid, ok := item["user_id"].(int64); ok {
				if info, ok := infoMap[uid]; ok {
					item["phone"] = info.Phone
					item["nickname"] = info.Nickname
					item["avatar_url"] = info.AvatarURL
				}
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) TransferWithdrawAdmin(c *gin.Context) {
	withdrawID := parseInt64WithDefault(c.Param("id"), 0)
	if withdrawID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	info, err := s.loadWithdrawForTransfer(withdrawID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if info.Status == WithdrawStatusPaid || info.Status == WithdrawStatusRefunded {
		c.JSON(http.StatusOK, gin.H{"status": info.Status})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	status, err := s.transferWithdraw(ctx, info.ID, info.OutBizNo, info.Amount, &alipayAccount{
		Name:    info.AlipayName,
		Account: info.AlipayAccount,
	})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"status": status, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": status})
}

func (s *Server) SyncWithdrawAdmin(c *gin.Context) {
	withdrawID := parseInt64WithDefault(c.Param("id"), 0)
	if withdrawID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	status, err := s.syncWithdraw(ctx, withdrawID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"status": status, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": status})
}

func (s *Server) GetAlipayAccountInfo(c *gin.Context) {
	if s.Alipay == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alipay not configured"})
		return
	}
	alipayUserID := strings.TrimSpace(c.Query("alipay_user_id"))
	accountType := strings.TrimSpace(c.Query("account_type"))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 6*time.Second)
	defer cancel()
	raw, err := s.Alipay.AccountQuery(ctx, alipayUserID, accountType)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"raw": raw})
}

func (s *Server) GetAlipayQuotaInfo(c *gin.Context) {
	if s.Alipay == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alipay not configured"})
		return
	}
	productCode := strings.TrimSpace(c.Query("product_code"))
	if productCode == "" {
		productCode = s.Cfg.AlipayProductCode
	}
	bizScene := strings.TrimSpace(c.Query("biz_scene"))
	if bizScene == "" {
		bizScene = s.Cfg.AlipayBizScene
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 6*time.Second)
	defer cancel()
	raw, err := s.Alipay.QuotaQuery(ctx, productCode, bizScene)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"raw": raw})
}

type transferTestRequest struct {
	AmountFen                int64                              `json:"amount"`
	AmountYuan               string                             `json:"amount_yuan"`
	PayeeAccount             string                             `json:"payee_account"`
	PayeeName                string                             `json:"payee_name"`
	TransferSceneReportInfos []payments.TransferSceneReportInfo `json:"transfer_scene_report_infos"`
}

func (s *Server) TransferTest(c *gin.Context) {
	if s.Alipay == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alipay not configured"})
		return
	}
	var req transferTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	amount := req.AmountFen
	if amount <= 0 && strings.TrimSpace(req.AmountYuan) != "" {
		amount = parseYuanToFen(req.AmountYuan)
	}
	if amount < 10 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "min transfer is 0.1 yuan"})
		return
	}
	account := strings.TrimSpace(req.PayeeAccount)
	name := strings.TrimSpace(req.PayeeName)
	if account == "" || name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "payee_account and payee_name required"})
		return
	}
	outBizNo := "TEST" + newOutBizNo(0)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	result, err := s.Alipay.Transfer(ctx, payments.TransferRequest{
		OutBizNo:                 outBizNo,
		AmountFen:                amount,
		PayeeAccount:             account,
		PayeeName:                name,
		IdentityType:             s.Cfg.AlipayIdentityType,
		TransferSceneReportInfos: req.TransferSceneReportInfos,
	})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": err.Error(), "result": result})
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": result})
}

type withdrawTransferInfo struct {
	ID            int64
	UserID        int64
	Amount        int64
	Status        string
	OutBizNo      string
	AlipayName    string
	AlipayAccount string
}

func (s *Server) loadWithdrawForTransfer(withdrawID int64) (*withdrawTransferInfo, error) {
	var info withdrawTransferInfo
	row := s.DB.QueryRow(`SELECT id, user_id, amount, status, out_biz_no, alipay_name, alipay_account
		FROM withdraw_requests WHERE id=?`, withdrawID)
	if err := row.Scan(&info.ID, &info.UserID, &info.Amount, &info.Status, &info.OutBizNo, &info.AlipayName, &info.AlipayAccount); err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.New("withdraw not found")
		}
		return nil, err
	}
	if info.OutBizNo == "" {
		info.OutBizNo = newOutBizNo(info.UserID)
		if _, err := s.DB.Exec(`UPDATE withdraw_requests SET out_biz_no=?, updated_at=NOW() WHERE id=?`, info.OutBizNo, info.ID); err != nil {
			return nil, err
		}
	}
	return &info, nil
}

func parseIntWithDefault(val string, def int) int {
	if val == "" {
		return def
	}
	parsed, err := strconv.Atoi(val)
	if err != nil {
		return def
	}
	return parsed
}

func parseInt64WithDefault(val string, def int64) int64 {
	if val == "" {
		return def
	}
	parsed, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return def
	}
	return parsed
}

func parseYuanToFen(val string) int64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
	if err != nil {
		return 0
	}
	return int64(f * 100)
}
