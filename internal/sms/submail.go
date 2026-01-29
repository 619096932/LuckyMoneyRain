package sms

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type SubmailClient struct {
	AppID     string
	AppKey    string
	ProjectID string
	Endpoint  string
	HTTP      *http.Client
}

type SubmailResponse struct {
	Status string `json:"status"`
	SendID string `json:"send_id"`
	Fee    int    `json:"fee"`
	Code   string `json:"code"`
	Msg    string `json:"msg"`
}

func NewSubmailClient(appID, appKey, projectID string) *SubmailClient {
	return &SubmailClient{
		AppID:     appID,
		AppKey:    appKey,
		ProjectID: projectID,
		Endpoint:  "https://api-v4.mysubmail.com/sms/xsend.json",
		HTTP: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (c *SubmailClient) SendCode(phone, code string) (*SubmailResponse, error) {
	if c.AppID == "" || c.AppKey == "" || c.ProjectID == "" {
		return nil, errors.New("submail config missing")
	}
	form := url.Values{}
	form.Set("appid", c.AppID)
	form.Set("to", phone)
	form.Set("project", c.ProjectID)
	form.Set("signature", c.AppKey)
	// 默认使用模板变量 code
	form.Set("vars", fmt.Sprintf("{\"code\":\"%s\"}", code))

	resp, err := c.HTTP.PostForm(c.Endpoint, form)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var parsed SubmailResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if parsed.Status != "success" {
		return &parsed, errors.New(parsed.Msg)
	}
	return &parsed, nil
}
