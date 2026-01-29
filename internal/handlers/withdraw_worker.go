package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

type WithdrawWorker struct {
	srv                *Server
	pollInterval       time.Duration
	waitFundsInterval  time.Duration
	processingInterval time.Duration
}

type withdrawJob struct {
	ID            int64
	UserID        int64
	Amount        int64
	Status        string
	OutBizNo      string
	AlipayName    string
	AlipayAccount string
	AlipayOrderID string
	Attempts      int
}

type alipayAccountResp struct {
	Code            string `json:"code"`
	Msg             string `json:"msg"`
	SubCode         string `json:"sub_code"`
	SubMsg          string `json:"sub_msg"`
	AvailableAmount string `json:"available_amount"`
	FreezeAmount    string `json:"freeze_amount"`
}

func NewWithdrawWorker(srv *Server) *WithdrawWorker {
	return &WithdrawWorker{
		srv:                srv,
		pollInterval:       2 * time.Second,
		waitFundsInterval:  30 * time.Second,
		processingInterval: 10 * time.Second,
	}
}

func (w *WithdrawWorker) Run(ctx context.Context) {
	if w == nil || w.srv == nil || w.srv.DB == nil {
		log.Printf("withdraw worker: db not configured")
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		processed, err := w.processOnce(ctx)
		if err != nil {
			log.Printf("withdraw worker error: %v", err)
		}
		if !processed {
			time.Sleep(w.pollInterval)
		}
	}
}

func (w *WithdrawWorker) processOnce(ctx context.Context) (bool, error) {
	job, err := w.pickNextAutoWithdraw(ctx)
	if err != nil {
		return false, err
	}
	if job == nil {
		return false, nil
	}
	if w.srv.Alipay == nil {
		_ = w.scheduleNextAttempt(job.ID, time.Now().Add(w.processingInterval))
		return true, errors.New("alipay not configured")
	}

	// If already processing and we have an order id or prior attempts, query instead of transfer.
	if job.Status == WithdrawStatusProcessing && (job.Attempts > 1 || job.AlipayOrderID != "") {
		status, err := w.srv.syncWithdraw(ctx, job.ID)
		_ = w.updateNextAttemptByStatus(job.ID, status)
		return true, err
	}

	available, err := w.queryAvailableBalance(ctx)
	if err != nil {
		_ = w.scheduleNextAttempt(job.ID, time.Now().Add(w.processingInterval))
		return true, err
	}
	if available < job.Amount {
		_ = w.markWaitingFunds(job.ID, available)
		return true, nil
	}

	status, err := w.srv.transferWithdraw(ctx, job.ID, job.OutBizNo, job.Amount, &alipayAccount{
		Name:    job.AlipayName,
		Account: job.AlipayAccount,
	})
	_ = w.updateNextAttemptByStatus(job.ID, status)
	return true, err
}

func (w *WithdrawWorker) pickNextAutoWithdraw(ctx context.Context) (*withdrawJob, error) {
	tx, err := w.srv.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	row := tx.QueryRowContext(ctx, `SELECT id, user_id, amount, status, out_biz_no, alipay_name, alipay_account, alipay_order_id, attempts
		FROM withdraw_requests
		WHERE auto_pay=1 AND status IN (?, ?, ?) AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())
		ORDER BY id ASC
		LIMIT 1 FOR UPDATE`,
		WithdrawStatusPending, WithdrawStatusWaiting, WithdrawStatusProcessing)
	var job withdrawJob
	var orderID sql.NullString
	if err := row.Scan(&job.ID, &job.UserID, &job.Amount, &job.Status, &job.OutBizNo, &job.AlipayName,
		&job.AlipayAccount, &orderID, &job.Attempts); err != nil {
		_ = tx.Rollback()
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if orderID.Valid {
		job.AlipayOrderID = orderID.String
	}
	if strings.TrimSpace(job.OutBizNo) == "" {
		job.OutBizNo = newOutBizNo(job.UserID)
		if _, err := tx.ExecContext(ctx, `UPDATE withdraw_requests SET out_biz_no=?, updated_at=NOW() WHERE id=?`, job.OutBizNo, job.ID); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	job.Attempts += 1
	nextAttempt := time.Now().Add(w.processingInterval)
	if _, err := tx.ExecContext(ctx, `UPDATE withdraw_requests SET status=?, attempts=?, last_attempt_at=NOW(), next_attempt_at=?, updated_at=NOW() WHERE id=?`,
		WithdrawStatusProcessing, job.Attempts, nextAttempt, job.ID); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &job, nil
}

func (w *WithdrawWorker) queryAvailableBalance(ctx context.Context) (int64, error) {
	raw, err := w.srv.Alipay.AccountQuery(ctx, w.srv.Cfg.AlipayBalanceUserID, w.srv.Cfg.AlipayBalanceAccountType)
	if err != nil {
		return 0, err
	}
	var resp alipayAccountResp
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return 0, err
	}
	if resp.Code != "" && resp.Code != "10000" {
		msg := strings.TrimSpace(resp.SubMsg)
		if msg == "" {
			msg = strings.TrimSpace(resp.Msg)
		}
		if resp.SubCode != "" {
			msg = fmt.Sprintf("%s(%s)", msg, resp.SubCode)
		}
		if msg == "" {
			msg = "alipay account query failed"
		}
		return 0, errors.New(msg)
	}
	return parseAmountToFen(resp.AvailableAmount)
}

func (w *WithdrawWorker) markWaitingFunds(withdrawID int64, available int64) error {
	reason := fmt.Sprintf("余额不足，当前可用 %.2f 元", float64(available)/100)
	_, err := w.srv.DB.Exec(`UPDATE withdraw_requests SET status=?, fail_reason=?, next_attempt_at=?, updated_at=NOW() WHERE id=?`,
		WithdrawStatusWaiting, reason, time.Now().Add(w.waitFundsInterval), withdrawID)
	return err
}

func (w *WithdrawWorker) scheduleNextAttempt(withdrawID int64, nextAt time.Time) error {
	_, err := w.srv.DB.Exec(`UPDATE withdraw_requests SET next_attempt_at=?, updated_at=NOW() WHERE id=?`, nextAt, withdrawID)
	return err
}

func (w *WithdrawWorker) updateNextAttemptByStatus(withdrawID int64, status string) error {
	switch status {
	case WithdrawStatusProcessing:
		return w.scheduleNextAttempt(withdrawID, time.Now().Add(w.processingInterval))
	case WithdrawStatusWaiting:
		return w.scheduleNextAttempt(withdrawID, time.Now().Add(w.waitFundsInterval))
	case WithdrawStatusPaid, WithdrawStatusFailed, WithdrawStatusRefunded:
		_, err := w.srv.DB.Exec(`UPDATE withdraw_requests SET next_attempt_at=NULL, updated_at=NOW() WHERE id=?`, withdrawID)
		return err
	default:
		return w.scheduleNextAttempt(withdrawID, time.Now().Add(w.pollInterval))
	}
}

func parseAmountToFen(val string) (int64, error) {
	raw := strings.TrimSpace(val)
	if raw == "" {
		return 0, errors.New("empty amount")
	}
	neg := false
	if strings.HasPrefix(raw, "-") {
		neg = true
		raw = strings.TrimPrefix(raw, "-")
	}
	parts := strings.SplitN(raw, ".", 2)
	intPart := int64(0)
	if parts[0] != "" {
		parsed, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, err
		}
		intPart = parsed
	}
	fracPart := int64(0)
	if len(parts) == 2 {
		frac := parts[1]
		if len(frac) > 2 {
			frac = frac[:2]
		}
		if len(frac) == 1 {
			frac += "0"
		}
		if frac != "" {
			parsed, err := strconv.ParseInt(frac, 10, 64)
			if err != nil {
				return 0, err
			}
			fracPart = parsed
		}
	}
	total := intPart*100 + fracPart
	if neg {
		total = -total
	}
	return total, nil
}
