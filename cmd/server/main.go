package main

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	webassets "hongbao"
	"hongbao/internal/config"
	"hongbao/internal/db"
	"hongbao/internal/handlers"
)

func serveEmbedded(path string) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, err := webassets.EmbeddedPages.ReadFile(path)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	}
}

func main() {
	cfg := config.Load()
	secret := strings.TrimSpace(cfg.GameSignSecret)
	if secret == "" || secret == "change-me" {
		log.Fatal("GAME_SIGN_SECRET must be set to a non-default value")
	}
	mysql, err := db.NewMySQL(cfg.MySQLDSN)
	if err != nil {
		log.Fatalf("mysql error: %v", err)
	}
	redis, err := db.NewRedis(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		log.Fatalf("redis error: %v", err)
	}

	srv := handlers.NewServer(cfg, mysql, redis)
	if cfg.WithdrawWorkerEnabled {
		worker := handlers.NewWithdrawWorker(srv)
		go worker.Run(context.Background())
		log.Printf("withdraw worker enabled in server")
	}

	r := gin.Default()
	// 静态页面（内嵌）
	r.GET("/", serveEmbedded("web/index.html"))
	r.GET("/admin", serveEmbedded("web/admin.html"))
	r.GET("/wallet", serveEmbedded("web/wallet.html"))
	r.GET("/withdraw", serveEmbedded("web/withdraw.html"))

	r.GET("/ws", func(c *gin.Context) {
		srv.HandleWS(c.Writer, c.Request)
	})

	api := r.Group("/api")
	{
		api.GET("/assets", srv.GetAssets)

		auth := api.Group("/auth")
		auth.POST("/sms/send", srv.SendSMSCode)
		auth.POST("/sms/verify", srv.VerifySMSCode)

		api.GET("/user/me", srv.AuthRequired(), srv.GetMe)
		api.GET("/user/wallet", srv.AuthRequired(), srv.GetWallet)
		api.POST("/user/alipay/bind", srv.AuthRequired(), srv.BindAlipay)
		api.POST("/user/withdraw", srv.AuthRequired(), srv.CreateWithdraw)
		api.GET("/user/withdraws", srv.AuthRequired(), srv.ListWithdraws)
		api.GET("/rounds/current", srv.GetCurrentRound)
		api.GET("/game/state", srv.AuthRequired(), srv.GetGameState)
		api.POST("/game/click", srv.AuthRequired(), srv.Click)
		api.GET("/game/result", srv.AuthRequired(), srv.GetResult)
		api.GET("/game/reveal", srv.AuthRequired(), srv.GetGameReveal)

		remote := api.Group("/remote")
		remote.POST("/register", srv.RemoteRegister)

		api.POST("/admin/login", srv.AdminLogin)
		api.POST("/admin/init/reset", srv.InitReset)

		admin := api.Group("/admin", srv.AdminRequired())
		admin.POST("/rounds", srv.CreateRound)
		admin.GET("/rounds", srv.ListRounds)
		admin.POST("/rounds/:id/whitelist", srv.AddWhitelist)
		admin.POST("/rounds/:id/lock", srv.LockRound)
		admin.POST("/rounds/:id/clear", srv.ClearRound)
		admin.POST("/rounds/:id/start", srv.StartRound)
		admin.POST("/rounds/:id/draw", srv.DrawRound)
		admin.DELETE("/rounds/:id", srv.DeleteRound)
		admin.GET("/rounds/:id/results", srv.GetRoundResults)
		admin.GET("/rounds/:id/leaderboard", srv.GetLeaderboard)
		admin.GET("/rounds/:id/export", srv.ExportRound)
		admin.GET("/online_users", srv.GetOnlineUsers)
		admin.GET("/metrics", srv.GetMetrics)
		admin.GET("/award_batches", srv.ListAwardBatches)
		admin.POST("/award_batches/:id/confirm", srv.ConfirmAward)
		admin.POST("/award_batches/:id/void", srv.VoidAwardBatch)
		admin.GET("/withdraw_switch", srv.GetWithdrawSwitch)
		admin.POST("/withdraw_switch", srv.SetWithdrawSwitch)
		admin.GET("/withdraws", srv.ListWithdrawsAdmin)
		admin.POST("/withdraws/:id/transfer", srv.TransferWithdrawAdmin)
		admin.POST("/withdraws/:id/sync", srv.SyncWithdrawAdmin)
		admin.GET("/alipay/account", srv.GetAlipayAccountInfo)
		admin.GET("/alipay/quota", srv.GetAlipayQuotaInfo)
		admin.POST("/alipay/transfer_test", srv.TransferTest)
	}

	r.GET("/healthz", func(c *gin.Context) { c.String(200, "ok") })

	log.Printf("server listening on %s", cfg.HTTPAddr)
	if err := r.Run(cfg.HTTPAddr); err != nil {
		log.Fatal(err)
	}
}
