package handlers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"hongbao/internal/payments"
)

const (
	WithdrawStatusPending    = "PENDING"
	WithdrawStatusProcessing = "PROCESSING"
	WithdrawStatusWaiting    = "WAITING_FUNDS"
	WithdrawStatusPaid       = "PAID"
	WithdrawStatusFailed     = "FAILED"
	WithdrawStatusRefunded   = "REFUNDED"
)

func (s *Server) autoWithdrawEnabled(amount int64) bool {
	if s.Alipay == nil {
		return false
	}
	max := s.Cfg.WithdrawAutoMaxFen
	if max <= 0 {
		return true
	}
	return amount <= max
}

func newOutBizNo(uid int64) string {
	now := time.Now().UnixMilli()
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	suffix := hex.EncodeToString(buf)
	return fmt.Sprintf("HB%d%d%s", uid, now, suffix)
}

func (s *Server) transferWithdraw(ctx context.Context, withdrawID int64, outBizNo string, amount int64, acc *alipayAccount) (string, error) {
	if s.Alipay == nil {
		return WithdrawStatusPending, errors.New("alipay not configured")
	}
	req := payments.TransferRequest{
		OutBizNo:     outBizNo,
		AmountFen:    amount,
		PayeeAccount: acc.Account,
		PayeeName:    acc.Name,
		IdentityType: s.Cfg.AlipayIdentityType,
	}
	result, err := s.Alipay.Transfer(ctx, req)
	return s.updateWithdrawFromResult(withdrawID, result, err)
}

func (s *Server) syncWithdraw(ctx context.Context, withdrawID int64) (string, error) {
	if s.Alipay == nil {
		return "", errors.New("alipay not configured")
	}
	var outBizNo string
	var orderId sql.NullString
	row := s.DB.QueryRow(`SELECT out_biz_no, alipay_order_id FROM withdraw_requests WHERE id=?`, withdrawID)
	if err := row.Scan(&outBizNo, &orderId); err != nil {
		if err == sql.ErrNoRows {
			return "", errors.New("withdraw not found")
		}
		return "", err
	}
	orderID := nullString(orderId)
	if outBizNo == "" && orderID == "" {
		return "", errors.New("missing out_biz_no and order_id")
	}
	result, err := s.Alipay.QueryTransfer(ctx, outBizNo, orderID)
	return s.updateWithdrawFromResult(withdrawID, result, err)
}

func (s *Server) updateWithdrawFromResult(withdrawID int64, res *payments.TransferResult, callErr error) (string, error) {
	status := mapTransferStatus(res, callErr)

	tx, err := s.DB.Begin()
	if err != nil {
		return "", err
	}
	var (
		userID        int64
		amount        int64
		currentStatus string
		orderId       sql.NullString
		payFundOrder  sql.NullString
		aliStatus     sql.NullString
		aliCode       sql.NullString
		aliMsg        sql.NullString
		aliSubCode    sql.NullString
		aliSubMsg     sql.NullString
		failReason    sql.NullString
		rawResp       sql.NullString
	)
	row := tx.QueryRow(`SELECT user_id, amount, status, alipay_order_id, alipay_fund_order_id, alipay_status,
		alipay_code, alipay_msg, alipay_sub_code, alipay_sub_msg, fail_reason, raw_response
		FROM withdraw_requests WHERE id=? FOR UPDATE`, withdrawID)
	if err := row.Scan(&userID, &amount, &currentStatus, &orderId, &payFundOrder, &aliStatus,
		&aliCode, &aliMsg, &aliSubCode, &aliSubMsg, &failReason, &rawResp); err != nil {
		_ = tx.Rollback()
		return "", err
	}
	orderIdVal := nullString(orderId)
	payFundOrderVal := nullString(payFundOrder)
	aliStatusVal := nullString(aliStatus)
	aliCodeVal := nullString(aliCode)
	aliMsgVal := nullString(aliMsg)
	aliSubCodeVal := nullString(aliSubCode)
	aliSubMsgVal := nullString(aliSubMsg)
	failReasonVal := nullString(failReason)
	rawRespVal := nullString(rawResp)

	if currentStatus == WithdrawStatusPaid || currentStatus == WithdrawStatusRefunded {
		_ = tx.Rollback()
		return currentStatus, nil
	}

	if res == nil {
		res = &payments.TransferResult{}
	}
	if res.OrderId == "" {
		res.OrderId = orderIdVal
	}
	if res.PayFundOrderId == "" {
		res.PayFundOrderId = payFundOrderVal
	}
	if res.Status == "" {
		res.Status = aliStatusVal
	}
	if res.Code == "" {
		res.Code = aliCodeVal
	}
	if res.Msg == "" {
		res.Msg = aliMsgVal
	}
	if res.SubCode == "" {
		res.SubCode = aliSubCodeVal
	}
	if res.SubMsg == "" {
		res.SubMsg = aliSubMsgVal
	}
	if res.FailReason == "" {
		res.FailReason = failReasonVal
	}
	if res.Raw == "" {
		res.Raw = rawRespVal
	}

	nextStatus := status
	if nextStatus == WithdrawStatusFailed {
		refunded, refundErr := refundWithdraw(tx, userID, amount, withdrawID)
		if refundErr != nil {
			_ = tx.Rollback()
			return currentStatus, refundErr
		}
		if refunded {
			nextStatus = WithdrawStatusRefunded
		}
	}
	if nextStatus == WithdrawStatusWaiting || nextStatus == WithdrawStatusFailed || nextStatus == WithdrawStatusRefunded {
		if res.FailReason == "" {
			res.FailReason = buildFailReason(res)
		}
	}
	if nextStatus == WithdrawStatusPaid {
		res.FailReason = ""
	}

	if nextStatus == WithdrawStatusPaid {
		_, err = tx.Exec(`UPDATE withdraw_requests SET status=?, alipay_order_id=?, alipay_fund_order_id=?,
			alipay_status=?, alipay_code=?, alipay_msg=?, alipay_sub_code=?, alipay_sub_msg=?, fail_reason=?,
			raw_response=?, updated_at=NOW(), paid_at=NOW() WHERE id=?`,
			nextStatus, res.OrderId, res.PayFundOrderId, res.Status, res.Code, res.Msg, res.SubCode, res.SubMsg,
			res.FailReason, res.Raw, withdrawID)
	} else {
		_, err = tx.Exec(`UPDATE withdraw_requests SET status=?, alipay_order_id=?, alipay_fund_order_id=?,
			alipay_status=?, alipay_code=?, alipay_msg=?, alipay_sub_code=?, alipay_sub_msg=?, fail_reason=?,
			raw_response=?, updated_at=NOW() WHERE id=?`,
			nextStatus, res.OrderId, res.PayFundOrderId, res.Status, res.Code, res.Msg, res.SubCode, res.SubMsg,
			res.FailReason, res.Raw, withdrawID)
	}
	if err != nil {
		_ = tx.Rollback()
		return currentStatus, err
	}
	if err := tx.Commit(); err != nil {
		return currentStatus, err
	}
	return nextStatus, nil
}

func mapTransferStatus(res *payments.TransferResult, callErr error) string {
	if callErr != nil || res == nil {
		return WithdrawStatusProcessing
	}
	code := strings.ToUpper(strings.TrimSpace(res.Code))
	sub := strings.ToUpper(strings.TrimSpace(res.SubCode))
	if (code != "" && code != "10000") || (code == "" && sub != "") {
		if isWaitingFundsCode(sub) {
			return WithdrawStatusWaiting
		}
		if isRetryableCode(sub) {
			return WithdrawStatusProcessing
		}
		return WithdrawStatusFailed
	}
	switch strings.ToUpper(res.Status) {
	case "SUCCESS":
		return WithdrawStatusPaid
	case "FAIL", "FAILED":
		return WithdrawStatusFailed
	case "REFUND":
		return WithdrawStatusRefunded
	default:
		return WithdrawStatusProcessing
	}
}

func isRetryableCode(code string) bool {
	switch code {
	case "SYSTEM_ERROR",
		"REQUEST_PROCESSING",
		"RESOURCE_LIMIT_EXCEED",
		"MRCHPROD_QUERY_ERROR",
		"PROCESS_FAIL":
		return true
	default:
		return false
	}
}

func isWaitingFundsCode(code string) bool {
	switch code {
	case "BALANCE_IS_NOT_ENOUGH",
		"PAYER_BALANCE_NOT_ENOUGH":
		return true
	default:
		return false
	}
}

func buildFailReason(res *payments.TransferResult) string {
	if res == nil {
		return ""
	}
	reason := strings.TrimSpace(res.FailReason)
	if reason == "" {
		reason = strings.TrimSpace(res.SubMsg)
	}
	if reason == "" {
		reason = strings.TrimSpace(res.Msg)
	}
	if reason == "" {
		reason = strings.TrimSpace(res.SubCode)
	}
	if reason == "" {
		reason = strings.TrimSpace(res.Code)
	}
	if reason == "" {
		return ""
	}
	sub := strings.TrimSpace(res.SubCode)
	if sub != "" && !strings.Contains(reason, sub) {
		reason = fmt.Sprintf("%s(%s)", reason, sub)
	}
	code := strings.TrimSpace(res.Code)
	if code != "" && !strings.Contains(reason, code) {
		reason = fmt.Sprintf("%s[%s]", reason, code)
	}
	return reason
}

func refundWithdraw(tx *sql.Tx, userID int64, amount int64, withdrawID int64) (bool, error) {
	var count int
	row := tx.QueryRow(`SELECT COUNT(1) FROM wallet_ledger WHERE reason='WITHDRAW_REFUND' AND ref_id=?`, withdrawID)
	if err := row.Scan(&count); err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}
	var balance int64
	row = tx.QueryRow(`SELECT balance FROM wallets WHERE user_id=? FOR UPDATE`, userID)
	if err := row.Scan(&balance); err != nil {
		if err != sql.ErrNoRows {
			return false, err
		}
		if _, err := tx.Exec(`INSERT INTO wallets (user_id, balance, updated_at) VALUES (?, ?, NOW())`, userID, amount); err != nil {
			return false, err
		}
	} else {
		newBalance := balance + amount
		if _, err := tx.Exec(`UPDATE wallets SET balance=?, updated_at=NOW() WHERE user_id=?`, newBalance, userID); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(`INSERT INTO wallet_ledger (user_id, amount, reason, ref_id, created_at) VALUES (?, ?, ?, ?, NOW())`,
		userID, amount, "WITHDRAW_REFUND", withdrawID); err != nil {
		return false, err
	}
	return true, nil
}

func nullString(val sql.NullString) string {
	if val.Valid {
		return val.String
	}
	return ""
}
