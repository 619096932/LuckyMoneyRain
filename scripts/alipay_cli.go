package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"hongbao/internal/config"
	"hongbao/internal/payments"
)

func main() {
	action := flag.String("action", "transfer", "account|quota|transfer|query")
	amountFen := flag.Int64("amount_fen", 0, "transfer amount in fen")
	amountYuan := flag.String("amount_yuan", "", "transfer amount in yuan, e.g. 10.50")
	payee := flag.String("payee", "", "payee account (alipay logon id or user id)")
	payeeName := flag.String("name", "", "payee real name")
	outBizNo := flag.String("out_biz_no", "", "out biz no")
	orderId := flag.String("order_id", "", "alipay order id")
	alipayUserID := flag.String("alipay_user_id", "", "account query alipay user id")
	accountType := flag.String("account_type", "", "account query account type")
	productCode := flag.String("product_code", "", "quota query product code")
	bizScene := flag.String("biz_scene", "", "quota query biz scene")
	flag.Parse()

	cfg := config.Load()
	client, err := payments.NewAlipayClient(payments.AlipayConfig{
		AppID:              cfg.AlipayAppID,
		PrivateKey:         cfg.AlipayPrivateKey,
		AppCertPath:        cfg.AlipayAppCertPath,
		AlipayCertPath:     cfg.AlipayAlipayCertPath,
		AlipayRootCertPath: cfg.AlipayRootCertPath,
		Env:                cfg.AlipayEnv,
		IdentityType:       cfg.AlipayIdentityType,
		BizScene:           cfg.AlipayBizScene,
		ProductCode:        cfg.AlipayProductCode,
		OrderTitle:         cfg.AlipayOrderTitle,
		Remark:             cfg.AlipayRemark,
	})
	if err != nil || client == nil {
		exitErr(fmt.Errorf("alipay not configured: %v", err))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	switch strings.ToLower(*action) {
	case "account":
		raw, err := client.AccountQuery(ctx, strings.TrimSpace(*alipayUserID), strings.TrimSpace(*accountType))
		if err != nil {
			exitErr(err)
			return
		}
		printJSON(map[string]string{"raw": raw})
	case "quota":
		pc := strings.TrimSpace(*productCode)
		if pc == "" {
			pc = cfg.AlipayProductCode
		}
		bs := strings.TrimSpace(*bizScene)
		if bs == "" {
			bs = cfg.AlipayBizScene
		}
		raw, err := client.QuotaQuery(ctx, pc, bs)
		if err != nil {
			exitErr(err)
			return
		}
		printJSON(map[string]string{"raw": raw})
	case "query":
		if strings.TrimSpace(*outBizNo) == "" && strings.TrimSpace(*orderId) == "" {
			exitErr(fmt.Errorf("out_biz_no or order_id required"))
			return
		}
		res, err := client.QueryTransfer(ctx, strings.TrimSpace(*outBizNo), strings.TrimSpace(*orderId))
		if err != nil {
			printJSON(res)
			exitErr(err)
			return
		}
		printJSON(res)
	default:
		amount := *amountFen
		if amount <= 0 && strings.TrimSpace(*amountYuan) != "" {
			amount = yuanToFen(*amountYuan)
		}
		if amount <= 0 {
			exitErr(fmt.Errorf("amount required"))
			return
		}
		if strings.TrimSpace(*payee) == "" || strings.TrimSpace(*payeeName) == "" {
			exitErr(fmt.Errorf("payee and name required"))
			return
		}
		obn := strings.TrimSpace(*outBizNo)
		if obn == "" {
			obn = fmt.Sprintf("CLI%d", time.Now().UnixMilli())
		}
		res, err := client.Transfer(ctx, payments.TransferRequest{
			OutBizNo:     obn,
			AmountFen:    amount,
			PayeeAccount: strings.TrimSpace(*payee),
			PayeeName:    strings.TrimSpace(*payeeName),
			IdentityType: cfg.AlipayIdentityType,
		})
		if err != nil {
			printJSON(res)
			exitErr(err)
			return
		}
		printJSON(res)
	}
}

func yuanToFen(val string) int64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
	if err != nil {
		return 0
	}
	return int64(f * 100)
}

func printJSON(v interface{}) {
	data, _ := json.MarshalIndent(v, "", "  ")
	_, _ = fmt.Fprintln(os.Stdout, string(data))
}

func exitErr(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err.Error())
}
