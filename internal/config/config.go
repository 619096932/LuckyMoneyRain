package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddr                       string
	MySQLDSN                       string
	RedisAddr                      string
	RedisPassword                  string
	RedisDB                        int
	JWTSecret                      string
	AdminToken                     string
	AdminPassword                  string
	AdminPhones                    map[string]bool
	GameSignSecret                 string
	InitSecret                     string
	SubmailAppID                   string
	SubmailAppKey                  string
	SubmailProjectID               string
	ClickWindowMS                  int
	ClickGraceMS                   int
	MinSpeedMult                   float64
	TimeSkewMS                     int
	IntroBGMURL                    string
	GameBGMURL                     string
	AlipayAppID                    string
	AlipayPrivateKey               string
	AlipayAppCertPath              string
	AlipayAlipayCertPath           string
	AlipayRootCertPath             string
	AlipayEnv                      string
	AlipayIdentityType             string
	AlipayBizScene                 string
	AlipayProductCode              string
	AlipayOrderTitle               string
	AlipayRemark                   string
	AlipayTransferSceneName        string
	AlipayTransferSceneReportInfos string
	AlipayBalanceUserID            string
	AlipayBalanceAccountType       string
	WithdrawAutoMaxFen             int64
	WithdrawWorkerEnabled          bool
	WithdrawEnabled                bool
	RemoteAPIKey                   string
}

func Load() Config {
	loadDotEnv(".env")
	cfg := Config{
		HTTPAddr:                       getEnv("HTTP_ADDR", ":8080"),
		MySQLDSN:                       getEnv("MYSQL_DSN", "root:password@tcp(127.0.0.1:3306)/hongbao?parseTime=true&charset=utf8mb4"),
		RedisAddr:                      getEnv("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPassword:                  getEnv("REDIS_PASSWORD", ""),
		RedisDB:                        getEnvInt("REDIS_DB", 0),
		JWTSecret:                      getEnv("JWT_SECRET", "change-me"),
		AdminToken:                     getEnv("ADMIN_TOKEN", ""),
		AdminPassword:                  getEnv("ADMIN_PASSWORD", ""),
		GameSignSecret:                 getEnv("GAME_SIGN_SECRET", "change-me"),
		InitSecret:                     getEnv("INIT_SECRET", ""),
		SubmailAppID:                   getEnv("SUBMAIL_APPID", ""),
		SubmailAppKey:                  getEnv("SUBMAIL_APPKEY", ""),
		SubmailProjectID:               getEnv("SUBMAIL_PROJECT", ""),
		ClickWindowMS:                  getEnvInt("CLICK_WINDOW_MS", 2400),
		ClickGraceMS:                   getEnvInt("CLICK_GRACE_MS", 1200),
		MinSpeedMult:                   getEnvFloat("MIN_SPEED_MULT", 0.2),
		TimeSkewMS:                     getEnvInt("TIME_SKEW_MS", 400),
		IntroBGMURL:                    getEnv("INTRO_BGM_URL", ""),
		GameBGMURL:                     getEnv("GAME_BGM_URL", ""),
		AlipayAppID:                    getEnv("ALIPAY_APP_ID", ""),
		AlipayPrivateKey:               getEnv("ALIPAY_PRIVATE_KEY", ""),
		AlipayAppCertPath:              getEnv("ALIPAY_APP_CERT_PATH", ""),
		AlipayAlipayCertPath:           getEnv("ALIPAY_ALIPAY_CERT_PATH", ""),
		AlipayRootCertPath:             getEnv("ALIPAY_ROOT_CERT_PATH", ""),
		AlipayEnv:                      getEnv("ALIPAY_ENV", "prod"),
		AlipayIdentityType:             getEnv("ALIPAY_IDENTITY_TYPE", "ALIPAY_LOGON_ID"),
		AlipayBizScene:                 getEnv("ALIPAY_BIZ_SCENE", "DIRECT_TRANSFER"),
		AlipayProductCode:              getEnv("ALIPAY_PRODUCT_CODE", "TRANS_ACCOUNT_NO_PWD"),
		AlipayOrderTitle:               getEnv("ALIPAY_ORDER_TITLE", "红包雨提现"),
		AlipayRemark:                   getEnv("ALIPAY_REMARK", "红包雨提现"),
		AlipayTransferSceneName:        getEnv("ALIPAY_TRANSFER_SCENE_NAME", ""),
		AlipayTransferSceneReportInfos: getEnv("ALIPAY_TRANSFER_SCENE_REPORT_INFOS", ""),
		AlipayBalanceUserID:            getEnv("ALIPAY_BALANCE_USER_ID", ""),
		AlipayBalanceAccountType:       getEnv("ALIPAY_BALANCE_ACCOUNT_TYPE", ""),
		WithdrawAutoMaxFen:             getEnvInt64("WITHDRAW_AUTO_MAX_FEN", 0),
		WithdrawWorkerEnabled:          getEnvBool("WITHDRAW_WORKER_ENABLED", false),
		WithdrawEnabled:                getEnvBool("WITHDRAW_ENABLED", true),
		RemoteAPIKey:                   getEnv("REMOTE_API_KEY", ""),
	}
	if cfg.ClickWindowMS < 2000 {
		cfg.ClickWindowMS = 2000
	}
	if cfg.ClickGraceMS < 0 {
		cfg.ClickGraceMS = 0
	}
	if cfg.ClickGraceMS > 5000 {
		cfg.ClickGraceMS = 5000
	}
	cfg.AdminPhones = parseCSVSet(getEnv("ADMIN_PHONES", ""))
	return cfg
}

func parseCSVSet(val string) map[string]bool {
	set := make(map[string]bool)
	for _, item := range strings.Split(val, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		set[item] = true
	}
	return set
}

func getEnv(key, def string) string {
	val := os.Getenv(key)
	if val == "" {
		return def
	}
	return val
}

func loadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, "\"")
		if key == "" {
			continue
		}
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
}

func getEnvInt(key string, def int) int {
	val := os.Getenv(key)
	if val == "" {
		return def
	}
	parsed, err := strconv.Atoi(val)
	if err != nil {
		return def
	}
	return parsed
}

func getEnvFloat(key string, def float64) float64 {
	val := os.Getenv(key)
	if val == "" {
		return def
	}
	parsed, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return def
	}
	return parsed
}

func getEnvInt64(key string, def int64) int64 {
	val := os.Getenv(key)
	if val == "" {
		return def
	}
	parsed, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return def
	}
	return parsed
}

func getEnvBool(key string, def bool) bool {
	val := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if val == "" {
		return def
	}
	switch val {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return def
	}
}
