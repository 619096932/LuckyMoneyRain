package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	alipay "github.com/smartwalle/alipay/v3"
)

type AlipayConfig struct {
	AppID                    string
	PrivateKey               string
	AppCertPath              string
	AlipayCertPath           string
	AlipayRootCertPath       string
	Env                      string
	IdentityType             string
	BizScene                 string
	ProductCode              string
	OrderTitle               string
	Remark                   string
	TransferSceneName        string
	TransferSceneReportInfos string
}

type AlipayClient struct {
	client                   *alipay.Client
	cfg                      AlipayConfig
	transferSceneReportInfos []TransferSceneReportInfo
}

type TransferRequest struct {
	OutBizNo                 string
	AmountFen                int64
	PayeeAccount             string
	PayeeName                string
	IdentityType             string
	OrderTitle               string
	Remark                   string
	TransferSceneReportInfos []TransferSceneReportInfo
}

type TransferResult struct {
	OutBizNo       string
	OrderId        string
	PayFundOrderId string
	Status         string
	Code           string
	Msg            string
	SubCode        string
	SubMsg         string
	FailReason     string
	Raw            string
	ReceivedAt     time.Time
}

type TransferSceneReportInfo struct {
	InfoType    string `json:"info_type"`
	InfoContent string `json:"info_content"`
}

type fundTransUniTransfer struct {
	alipay.AuxParam
	AppAuthToken             string                    `json:"-"`
	OutBizNo                 string                    `json:"out_biz_no"`
	TransAmount              string                    `json:"trans_amount"`
	ProductCode              string                    `json:"product_code"`
	BizScene                 string                    `json:"biz_scene"`
	OrderTitle               string                    `json:"order_title,omitempty"`
	PayeeInfo                *alipay.PayeeInfo         `json:"payee_info"`
	Remark                   string                    `json:"remark,omitempty"`
	TransferSceneName        string                    `json:"transfer_scene_name,omitempty"`
	TransferSceneReportInfos []TransferSceneReportInfo `json:"transfer_scene_report_infos,omitempty"`
}

func (f fundTransUniTransfer) APIName() string {
	return "alipay.fund.trans.uni.transfer"
}

func (f fundTransUniTransfer) Params() map[string]string {
	var m = make(map[string]string)
	m["app_auth_token"] = f.AppAuthToken
	return m
}

func NewAlipayClient(cfg AlipayConfig) (*AlipayClient, error) {
	if strings.TrimSpace(cfg.AppID) == "" || strings.TrimSpace(cfg.PrivateKey) == "" {
		return nil, nil
	}
	privateKey, err := loadKey(cfg.PrivateKey)
	if err != nil {
		return nil, err
	}
	cfg = normalizeAlipayConfig(cfg)

	isProd := !strings.Contains(strings.ToLower(cfg.Env), "sandbox")
	client, err := alipay.New(cfg.AppID, privateKey, isProd)
	if err != nil {
		return nil, err
	}
	if cfg.AppCertPath == "" || cfg.AlipayCertPath == "" || cfg.AlipayRootCertPath == "" {
		return nil, errors.New("alipay cert mode requires ALIPAY_APP_CERT_PATH, ALIPAY_ALIPAY_CERT_PATH, ALIPAY_ROOT_CERT_PATH")
	}
	if err := client.LoadAppCertPublicKeyFromFile(cfg.AppCertPath); err != nil {
		return nil, err
	}
	if err := client.LoadAlipayCertPublicKeyFromFile(cfg.AlipayCertPath); err != nil {
		return nil, err
	}
	if err := client.LoadAliPayRootCertFromFile(cfg.AlipayRootCertPath); err != nil {
		return nil, err
	}
	var reportInfos []TransferSceneReportInfo
	if strings.TrimSpace(cfg.TransferSceneReportInfos) != "" {
		if err := json.Unmarshal([]byte(cfg.TransferSceneReportInfos), &reportInfos); err != nil {
			return nil, err
		}
	}
	return &AlipayClient{
		client:                   client,
		cfg:                      cfg,
		transferSceneReportInfos: reportInfos,
	}, nil
}

func (c *AlipayClient) Transfer(ctx context.Context, req TransferRequest) (*TransferResult, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("alipay not configured")
	}
	if req.OutBizNo == "" || req.AmountFen <= 0 || req.PayeeAccount == "" || req.PayeeName == "" {
		return nil, errors.New("invalid transfer request")
	}
	identityType := req.IdentityType
	if identityType == "" {
		identityType = c.cfg.IdentityType
	}
	orderTitle := req.OrderTitle
	if orderTitle == "" {
		orderTitle = c.cfg.OrderTitle
	}
	remark := req.Remark
	if remark == "" {
		remark = c.cfg.Remark
	}
	param := fundTransUniTransfer{
		OutBizNo:    req.OutBizNo,
		TransAmount: fenToYuan(req.AmountFen),
		ProductCode: c.cfg.ProductCode,
		BizScene:    c.cfg.BizScene,
		OrderTitle:  orderTitle,
		Remark:      remark,
		PayeeInfo: &alipay.PayeeInfo{
			Identity:     req.PayeeAccount,
			IdentityType: normalizeIdentityType(identityType),
			Name:         req.PayeeName,
		},
	}
	if c.cfg.TransferSceneName != "" {
		param.TransferSceneName = c.cfg.TransferSceneName
	}
	infos := req.TransferSceneReportInfos
	if len(infos) == 0 {
		infos = c.transferSceneReportInfos
	}
	if len(infos) > 0 {
		param.TransferSceneReportInfos = infos
	}
	var resp *alipay.FundTransUniTransferRsp
	err := c.client.Request(ctx, param, &resp)
	result := parseTransferResult(req.OutBizNo, resp)
	if err != nil {
		return result, err
	}
	return result, nil
}

func (c *AlipayClient) QueryTransfer(ctx context.Context, outBizNo, orderId string) (*TransferResult, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("alipay not configured")
	}
	if outBizNo == "" && orderId == "" {
		return nil, errors.New("out_biz_no or order_id required")
	}
	param := alipay.FundTransCommonQuery{
		ProductCode: c.cfg.ProductCode,
		BizScene:    c.cfg.BizScene,
		OutBizNo:    outBizNo,
		OrderId:     orderId,
	}
	resp, err := c.client.FundTransCommonQuery(ctx, param)
	result := parseCommonQueryResult(outBizNo, resp)
	if err != nil {
		return result, err
	}
	return result, nil
}

func (c *AlipayClient) AccountQuery(ctx context.Context, alipayUserId, accountType string) (string, error) {
	if c == nil || c.client == nil {
		return "", errors.New("alipay not configured")
	}
	param := alipay.FundAccountQuery{
		AliPayUserId: strings.TrimSpace(alipayUserId),
		AccountType:  strings.TrimSpace(accountType),
	}
	resp, err := c.client.FundAccountQuery(ctx, param)
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(resp)
	return string(raw), nil
}

func (c *AlipayClient) QuotaQuery(ctx context.Context, productCode, bizScene string) (string, error) {
	if c == nil || c.client == nil {
		return "", errors.New("alipay not configured")
	}
	param := fundQuotaQuery{
		ProductCode: strings.TrimSpace(productCode),
		BizScene:    strings.TrimSpace(bizScene),
	}
	resp, err := c.doRequest(ctx, param)
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(resp)
	return string(raw), nil
}

type fundQuotaQuery struct {
	alipay.AuxParam
	AppAuthToken string `json:"-"`
	ProductCode  string `json:"product_code"`
	BizScene     string `json:"biz_scene"`
}

func (f fundQuotaQuery) APIName() string {
	return "alipay.fund.quota.query"
}

func (f fundQuotaQuery) Params() map[string]string {
	var m = make(map[string]string)
	m["app_auth_token"] = f.AppAuthToken
	return m
}

func (c *AlipayClient) doRequest(ctx context.Context, param alipay.Param) (map[string]interface{}, error) {
	var resp map[string]interface{}
	if err := c.client.Request(ctx, param, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func parseTransferResult(outBizNo string, resp *alipay.FundTransUniTransferRsp) *TransferResult {
	if resp == nil {
		return &TransferResult{OutBizNo: outBizNo, ReceivedAt: time.Now()}
	}
	raw, _ := json.Marshal(resp)
	result := &TransferResult{
		OutBizNo:       outBizNo,
		OrderId:        resp.OrderId,
		PayFundOrderId: resp.PayFundOrderId,
		Status:         resp.Status,
		Code:           string(resp.Code),
		Msg:            resp.Msg,
		SubCode:        resp.SubCode,
		SubMsg:         resp.SubMsg,
		Raw:            string(raw),
		ReceivedAt:     time.Now(),
	}
	if resp.OutBizNo != "" {
		result.OutBizNo = resp.OutBizNo
	}
	return result
}

func parseCommonQueryResult(outBizNo string, resp *alipay.FundTransCommonQueryRsp) *TransferResult {
	if resp == nil {
		return &TransferResult{OutBizNo: outBizNo, ReceivedAt: time.Now()}
	}
	raw, _ := json.Marshal(resp)
	result := &TransferResult{
		OutBizNo:       outBizNo,
		OrderId:        resp.OrderId,
		PayFundOrderId: resp.PayFundOrderId,
		Status:         resp.Status,
		Code:           string(resp.Code),
		Msg:            resp.Msg,
		SubCode:        resp.SubCode,
		SubMsg:         resp.SubMsg,
		FailReason:     resp.FailReason,
		Raw:            string(raw),
		ReceivedAt:     time.Now(),
	}
	if resp.OutBizNo != "" {
		result.OutBizNo = resp.OutBizNo
	}
	return result
}

func normalizeAlipayConfig(cfg AlipayConfig) AlipayConfig {
	cfg.IdentityType = strings.TrimSpace(cfg.IdentityType)
	if cfg.IdentityType == "" {
		cfg.IdentityType = "ALIPAY_LOGON_ID"
	}
	cfg.BizScene = strings.TrimSpace(cfg.BizScene)
	if cfg.BizScene == "" {
		cfg.BizScene = "DIRECT_TRANSFER"
	}
	cfg.ProductCode = strings.TrimSpace(cfg.ProductCode)
	if cfg.ProductCode == "" {
		cfg.ProductCode = "TRANS_ACCOUNT_NO_PWD"
	}
	cfg.OrderTitle = strings.TrimSpace(cfg.OrderTitle)
	if cfg.OrderTitle == "" {
		cfg.OrderTitle = "红包雨提现"
	}
	cfg.Remark = strings.TrimSpace(cfg.Remark)
	if cfg.Remark == "" {
		cfg.Remark = "红包雨提现"
	}
	cfg.TransferSceneName = strings.TrimSpace(cfg.TransferSceneName)
	return cfg
}

func normalizeIdentityType(val string) string {
	v := strings.ToUpper(strings.TrimSpace(val))
	switch v {
	case "ALIPAY_USER_ID", "ALIPAY_USERID":
		return "ALIPAY_USER_ID"
	default:
		return "ALIPAY_LOGON_ID"
	}
}

func loadKey(val string) (string, error) {
	trimmed := strings.TrimSpace(val)
	if trimmed == "" {
		return "", errors.New("empty key")
	}
	if strings.Contains(trimmed, "PRIVATE KEY") {
		return normalizeKey(trimmed), nil
	}
	if fileExists(trimmed) {
		content, err := os.ReadFile(trimmed)
		if err != nil {
			return "", err
		}
		return normalizeKey(string(content)), nil
	}
	return normalizeKey(trimmed), nil
}

func normalizeKey(key string) string {
	key = strings.ReplaceAll(key, "\\n", "\n")
	key = strings.TrimSpace(key)
	if strings.Contains(key, "BEGIN") {
		return key
	}
	// If the key is raw base64 (no PEM header), wrap it as PKCS8 PEM.
	return wrapPemBlock("PRIVATE KEY", key)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func wrapPemBlock(title, raw string) string {
	clean := make([]rune, 0, len(raw))
	for _, r := range raw {
		if r == '\n' || r == '\r' || r == '\t' || r == ' ' {
			continue
		}
		clean = append(clean, r)
	}
	const lineLen = 64
	var b strings.Builder
	b.WriteString("-----BEGIN ")
	b.WriteString(title)
	b.WriteString("-----\n")
	for i := 0; i < len(clean); i += lineLen {
		end := i + lineLen
		if end > len(clean) {
			end = len(clean)
		}
		b.WriteString(string(clean[i:end]))
		b.WriteString("\n")
	}
	b.WriteString("-----END ")
	b.WriteString(title)
	b.WriteString("-----")
	return b.String()
}

func fenToYuan(fen int64) string {
	sign := ""
	if fen < 0 {
		sign = "-"
		fen = -fen
	}
	return fmt.Sprintf("%s%d.%02d", sign, fen/100, fen%100)
}
