package models

import "time"

type User struct {
	ID        int64     `json:"id"`
	Phone     string    `json:"phone"`
	Nickname  string    `json:"nickname"`
	AvatarURL string    `json:"avatar_url"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type RoundStatus string

const (
	RoundWaiting        RoundStatus = "WAITING"
	RoundLocked         RoundStatus = "LOCKED"
	RoundCountdown      RoundStatus = "COUNTDOWN"
	RoundRunning        RoundStatus = "RUNNING"
	RoundReadyDraw      RoundStatus = "READY_DRAW"
	RoundDrawing        RoundStatus = "DRAWING"
	RoundPendingConfirm RoundStatus = "PENDING_CONFIRM"
	RoundFinished       RoundStatus = "FINISHED"
)

type Round struct {
	ID            int64       `json:"id"`
	Title         string      `json:"title"`
	TotalPool     int64       `json:"total_pool"` // 分
	DurationSec   int         `json:"duration_sec"`
	SliceMS       int         `json:"slice_ms"`
	DropsPerSlice int         `json:"drops_per_slice"`
	BombsPerSlice int         `json:"bombs_per_slice"`
	BigsPerSlice  int         `json:"bigs_per_slice"`
	EmptyPerSlice int         `json:"empty_per_slice"`
	BigMultiplier float64     `json:"big_multiplier"`
	MaxSpeed      float64     `json:"max_speed"`
	DropVisibleMS int         `json:"drop_visible_ms"`
	ScoreTotal    int         `json:"score_total"` // 每用户总幸运分
	BombPenalty   int         `json:"bomb_penalty"`
	MinAward      int64       `json:"min_award"`
	MaxAward      int64       `json:"max_award"`
	LuckyRatio    int         `json:"lucky_ratio"`
	BaseRatio     int         `json:"base_ratio"`
	TailTopN      int         `json:"tail_top_n"`
	RankSegments  int         `json:"rank_segments"`
	Status        RoundStatus `json:"status"`
	StartAtMS     int64       `json:"start_at"`
	EndAtMS       int64       `json:"end_at"`
	Seed          uint32      `json:"seed"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

type AwardBatch struct {
	ID          int64     `json:"id"`
	RoundID     int64     `json:"round_id"`
	TotalPool   int64     `json:"total_pool"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	ConfirmedAt time.Time `json:"confirmed_at"`
}

type AwardDetail struct {
	BatchID int64 `json:"batch_id"`
	UserID  int64 `json:"user_id"`
	Score   int   `json:"score"`
	Amount  int64 `json:"amount"`
	Base    int64 `json:"base_amount"`
	Lucky   int64 `json:"lucky_amount"`
}
