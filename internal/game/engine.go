package game

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"hongbao/internal/models"
)

var clickLua = redis.NewScript(`
local bitKey = KEYS[1]
local scoreKey = KEYS[2]
local sumKey = KEYS[3]
local bitOffset = tonumber(ARGV[1])
local delta = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])
local member = ARGV[4]

local old = redis.call('SETBIT', bitKey, bitOffset, 1)
if old == 1 then
  return {1, 0, 0}
end

local total = redis.call('ZINCRBY', scoreKey, delta, member)
total = tonumber(total)
if total < 0 then
  redis.call('ZADD', scoreKey, 0, member)
  delta = delta - total
  total = 0
end

if delta ~= 0 then
  redis.call('INCRBY', sumKey, delta)
end

if ttl and ttl > 0 then
  redis.call('EXPIRE', bitKey, ttl)
  redis.call('EXPIRE', scoreKey, ttl)
  if delta ~= 0 then
    redis.call('EXPIRE', sumKey, ttl)
  end
end

return {0, total, delta}
`)

type SliceManifest struct {
	SliceID       int     `json:"slice_id"`
	StartAtMS     int64   `json:"start_at"`
	DurationMS    int     `json:"duration_ms"`
	DropCount     int     `json:"drop_count"`
	BombCount     int     `json:"bomb_count"`
	BigCount      int     `json:"big_count"`
	EmptyCount    int     `json:"empty_count"`
	BigMultiplier float64 `json:"big_multiplier"`
	WindowMS      int     `json:"window_ms"`
	Seed          uint32  `json:"seed"`
	ScoreTotal    int     `json:"score_total"`
}

type SliceRuntime struct {
	Manifest   SliceManifest
	OffsetsMS  []int
	IsBomb     []bool
	IsBig      []bool
	IsEmpty    []bool
	BaseScores []int
}

type RoundRuntime struct {
	Round      models.Round
	Slices     []SliceRuntime
	RevealSalt string
}

type Manager struct {
	mu             sync.RWMutex
	current        *RoundRuntime
	redis          *redis.Client
	windowMS       int
	minSpeedMult   float64
	timeSkewMS     int64
	lateGraceMS    int64
	cacheMu        sync.Mutex
	cacheRoundID   int64
	cacheSalt      string
	cacheUsers     map[int64]map[int]SliceRuntime
	cacheMaxUsers  int
	cacheMaxSlices int
}

func NewManager(redis *redis.Client, windowMS int, minSpeedMult float64, timeSkewMS int, lateGraceMS int, cacheUsers int, cacheSlices int) *Manager {
	if cacheUsers < 0 {
		cacheUsers = 0
	}
	if cacheSlices < 0 {
		cacheSlices = 0
	}
	return &Manager{
		redis:          redis,
		windowMS:       windowMS,
		minSpeedMult:   minSpeedMult,
		timeSkewMS:     int64(timeSkewMS),
		lateGraceMS:    int64(lateGraceMS),
		cacheMaxUsers:  cacheUsers,
		cacheMaxSlices: cacheSlices,
	}
}

func (m *Manager) SetCurrent(rt *RoundRuntime) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = rt
	m.resetRuntimeCacheLocked()
}

func (m *Manager) GetCurrent() *RoundRuntime {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current == nil {
		return nil
	}
	copyRt := *m.current
	return &copyRt
}

func (m *Manager) CurrentRoundID() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current == nil {
		return 0
	}
	return m.current.Round.ID
}

func (m *Manager) cacheEnabled() bool {
	return m.cacheMaxUsers > 0 && m.cacheMaxSlices > 0
}

func (m *Manager) resetRuntimeCacheLocked() {
	if !m.cacheEnabled() {
		return
	}
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	m.cacheRoundID = 0
	m.cacheSalt = ""
	m.cacheUsers = make(map[int64]map[int]SliceRuntime)
}

func (m *Manager) getSliceRuntime(rt *RoundRuntime, userID int64, sliceID int) SliceRuntime {
	if !m.cacheEnabled() {
		manifest := rt.Slices[sliceID].Manifest
		outcomeSeed := UserSeed(manifest.Seed, userID)
		visualSeed := UserVisualSeed(manifest.Seed, userID, rt.RevealSalt)
		return BuildSliceRuntimeWithSeeds(manifest, outcomeSeed, visualSeed)
	}

	m.cacheMu.Lock()
	if m.cacheUsers == nil || m.cacheRoundID != rt.Round.ID || m.cacheSalt != rt.RevealSalt {
		m.cacheRoundID = rt.Round.ID
		m.cacheSalt = rt.RevealSalt
		m.cacheUsers = make(map[int64]map[int]SliceRuntime)
	}
	userSlices := m.cacheUsers[userID]
	if userSlices != nil {
		if cached, ok := userSlices[sliceID]; ok {
			m.cacheMu.Unlock()
			return cached
		}
	}
	m.cacheMu.Unlock()

	manifest := rt.Slices[sliceID].Manifest
	outcomeSeed := UserSeed(manifest.Seed, userID)
	visualSeed := UserVisualSeed(manifest.Seed, userID, rt.RevealSalt)
	runtime := BuildSliceRuntimeWithSeeds(manifest, outcomeSeed, visualSeed)

	m.cacheMu.Lock()
	if m.cacheRoundID != rt.Round.ID || m.cacheSalt != rt.RevealSalt {
		m.cacheMu.Unlock()
		return runtime
	}
	userSlices = m.cacheUsers[userID]
	if userSlices == nil {
		if m.cacheMaxUsers > 0 && len(m.cacheUsers) >= m.cacheMaxUsers {
			for k := range m.cacheUsers {
				delete(m.cacheUsers, k)
				break
			}
		}
		userSlices = make(map[int]SliceRuntime)
		m.cacheUsers[userID] = userSlices
	}
	if m.cacheMaxSlices > 0 && len(userSlices) >= m.cacheMaxSlices {
		for k := range userSlices {
			delete(userSlices, k)
			break
		}
	}
	userSlices[sliceID] = runtime
	m.cacheMu.Unlock()
	return runtime
}

func BuildRoundRuntime(round models.Round, windowMS int) (*RoundRuntime, error) {
	if round.DropsPerSlice <= 0 || round.BombsPerSlice < 0 || round.BombsPerSlice >= round.DropsPerSlice {
		return nil, errors.New("invalid drop/bomb config")
	}
	if round.BigsPerSlice < 0 {
		return nil, errors.New("invalid big config")
	}
	if round.BigsPerSlice > round.DropsPerSlice-round.BombsPerSlice {
		return nil, errors.New("invalid big config")
	}
	if round.EmptyPerSlice < 0 {
		return nil, errors.New("invalid empty config")
	}
	if round.EmptyPerSlice > round.DropsPerSlice-round.BombsPerSlice-round.BigsPerSlice {
		return nil, errors.New("invalid empty config")
	}
	if round.BigMultiplier <= 1 {
		round.BigMultiplier = 2
	}
	durationMS := round.DurationSec * 1000
	sliceCount := durationMS / round.SliceMS
	if sliceCount <= 0 {
		return nil, errors.New("invalid slice config")
	}
	if durationMS%round.SliceMS != 0 {
		sliceCount++
	}

	perSlice := make([]int, sliceCount)
	base := round.ScoreTotal / sliceCount
	rem := round.ScoreTotal % sliceCount
	for i := 0; i < sliceCount; i++ {
		perSlice[i] = base
		if i < rem {
			perSlice[i]++
		}
	}

	effectiveWindow := windowMS
	if round.DropVisibleMS > 0 {
		effectiveWindow = round.DropVisibleMS
	} else if round.MaxSpeed > 0 {
		scale := round.MaxSpeed
		if scale < 0.6 {
			scale = 0.6
		}
		if scale > 1.6 {
			scale = 1.6
		}
		effectiveWindow = int(math.Round(float64(windowMS) / scale))
	}
	if effectiveWindow < 800 {
		effectiveWindow = 800
	}
	if effectiveWindow > 6000 {
		effectiveWindow = 6000
	}

	rt := &RoundRuntime{Round: round}
	rt.Slices = make([]SliceRuntime, sliceCount)

	for i := 0; i < sliceCount; i++ {
		seed := round.Seed ^ uint32(i*2654435761)
		if seed == 0 {
			seed = 0x12345678
		}
		start := round.StartAtMS + int64(i*round.SliceMS)
		manifest := SliceManifest{
			SliceID:       i,
			StartAtMS:     start,
			DurationMS:    round.SliceMS,
			DropCount:     round.DropsPerSlice,
			BombCount:     round.BombsPerSlice,
			BigCount:      round.BigsPerSlice,
			EmptyCount:    round.EmptyPerSlice,
			BigMultiplier: round.BigMultiplier,
			WindowMS:      effectiveWindow,
			Seed:          seed,
			ScoreTotal:    perSlice[i],
		}
		rt.Slices[i] = buildSliceRuntime(manifest)
	}
	rt.RevealSalt = newRevealSalt()
	return rt, nil
}

func buildSliceRuntime(manifest SliceManifest) SliceRuntime {
	rng := NewXorShift32(manifest.Seed)
	indices := make([]int, manifest.DropCount)
	for i := 0; i < manifest.DropCount; i++ {
		indices[i] = i
	}
	shuffle(indices, rng)
	isBomb := make([]bool, manifest.DropCount)
	for i := 0; i < manifest.BombCount; i++ {
		isBomb[indices[i]] = true
	}

	nonBomb := indices[manifest.BombCount:]
	isBig := make([]bool, manifest.DropCount)
	if manifest.BigCount > 0 && len(nonBomb) > 0 {
		shuffle(nonBomb, rng)
		maxBig := manifest.BigCount
		if maxBig > len(nonBomb) {
			maxBig = len(nonBomb)
		}
		for i := 0; i < maxBig; i++ {
			isBig[nonBomb[i]] = true
		}
	}

	remaining := make([]int, 0, len(nonBomb))
	for _, idx := range nonBomb {
		if !isBig[idx] {
			remaining = append(remaining, idx)
		}
	}
	isEmpty := make([]bool, manifest.DropCount)
	if manifest.EmptyCount > 0 && len(remaining) > 0 {
		shuffle(remaining, rng)
		maxEmpty := manifest.EmptyCount
		if maxEmpty > len(remaining) {
			maxEmpty = len(remaining)
		}
		for i := 0; i < maxEmpty; i++ {
			isEmpty[remaining[i]] = true
		}
	}

	baseScores := make([]int, manifest.DropCount)
	scoring := make([]int, 0, len(nonBomb))
	for _, idx := range nonBomb {
		if !isEmpty[idx] {
			scoring = append(scoring, idx)
		}
	}
	if len(scoring) > 0 && manifest.ScoreTotal > 0 {
		totalWeight := 0.0
		for _, idx := range scoring {
			if isBig[idx] {
				totalWeight += manifest.BigMultiplier
			} else {
				totalWeight += 1.0
			}
		}
		allocated := 0
		for _, idx := range scoring {
			weight := 1.0
			if isBig[idx] {
				weight = manifest.BigMultiplier
			}
			val := int(math.Floor(float64(manifest.ScoreTotal) * weight / totalWeight))
			baseScores[idx] = val
			allocated += val
		}
		rem := manifest.ScoreTotal - allocated
		if rem > 0 {
			shuffle(scoring, rng)
			for i := 0; i < rem; i++ {
				baseScores[scoring[i%len(scoring)]]++
			}
		}
	}

	offsets := make([]int, manifest.DropCount)
	maxOffset := manifest.DurationMS - manifest.WindowMS
	if maxOffset < 0 {
		maxOffset = 0
	}
	for i := 0; i < manifest.DropCount; i++ {
		offsets[i] = int(math.Floor(rng.Float64() * float64(maxOffset+1)))
	}

	return SliceRuntime{
		Manifest:   manifest,
		OffsetsMS:  offsets,
		IsBomb:     isBomb,
		IsBig:      isBig,
		IsEmpty:    isEmpty,
		BaseScores: baseScores,
	}
}

func UserSeed(baseSeed uint32, userID int64) uint32 {
	u := uint32(userID) ^ uint32(uint64(userID)>>32)
	return baseSeed ^ (u * 2654435761)
}

func UserVisualSeed(baseSeed uint32, userID int64, salt string) uint32 {
	if salt == "" {
		return UserSeed(baseSeed, userID) ^ 0x9e3779b9
	}
	h := sha256.New()
	_, _ = h.Write([]byte(salt))
	var buf [12]byte
	binary.BigEndian.PutUint32(buf[0:4], baseSeed)
	binary.BigEndian.PutUint64(buf[4:12], uint64(userID))
	_, _ = h.Write(buf[:])
	sum := h.Sum(nil)
	return binary.BigEndian.Uint32(sum[:4])
}

func BuildSliceRuntimeWithSeed(manifest SliceManifest, seed uint32) SliceRuntime {
	m := manifest
	m.Seed = seed
	return buildSliceRuntime(m)
}

func BuildSliceRuntimeWithSeeds(manifest SliceManifest, outcomeSeed uint32, visualSeed uint32) SliceRuntime {
	runtime := BuildSliceRuntimeWithSeed(manifest, outcomeSeed)
	if visualSeed != outcomeSeed {
		runtime.OffsetsMS = buildOffsets(manifest, visualSeed)
	}
	return runtime
}

func (m *Manager) ValidateClick(ctx context.Context, userID int64, roundID int64, dropID int, nowMS int64) (int, int, bool, error) {
	m.mu.RLock()
	rt := m.current
	m.mu.RUnlock()
	if rt == nil || rt.Round.ID != roundID {
		return 0, 0, false, errors.New("round not running")
	}
	if rt.Round.Status != models.RoundRunning {
		return 0, 0, false, errors.New("round not in running state")
	}
	if dropID < 0 {
		return 0, 0, false, errors.New("invalid drop")
	}
	dropCount := rt.Round.DropsPerSlice
	sliceID := dropID / dropCount
	idx := dropID % dropCount
	if sliceID < 0 || sliceID >= len(rt.Slices) {
		return 0, 0, false, errors.New("invalid slice")
	}
	manifest := rt.Slices[sliceID].Manifest
	if idx < 0 || idx >= manifest.DropCount {
		return 0, 0, false, errors.New("invalid drop index")
	}
	slice := m.getSliceRuntime(rt, userID, sliceID)

	// 时间窗口校验（使用服务端时间）
	dropStart := slice.Manifest.StartAtMS + int64(slice.OffsetsMS[idx])
	if nowMS+m.timeSkewMS < dropStart || nowMS > dropStart+int64(slice.Manifest.WindowMS)+m.timeSkewMS+m.lateGraceMS {
		return 0, 0, false, errors.New("out of window")
	}

	// 去重（每用户一个bitmap）
	bitKey := clickBitmapKey(roundID, userID, rt.Round.StartAtMS)
	bitOffset := int64(dropID)

	isBomb := slice.IsBomb[idx]
	isEmpty := false
	if idx >= 0 && idx < len(slice.IsEmpty) {
		isEmpty = slice.IsEmpty[idx]
	}
	baseScore := slice.BaseScores[idx]

	// 速度衰减
	delta := float64(nowMS-dropStart) / float64(slice.Manifest.WindowMS)
	speedMult := 1.0 - delta
	if speedMult < m.minSpeedMult {
		speedMult = m.minSpeedMult
	}
	if speedMult > 1 {
		speedMult = 1
	}
	deltaScore := int(math.Round(float64(baseScore) * speedMult))
	if isBomb {
		deltaScore = -rt.Round.BombPenalty
	} else if isEmpty {
		deltaScore = 0
	} else {
		if deltaScore < 1 {
			deltaScore = 1
		}
	}

	// 更新分数（Lua 合并 Redis 操作）
	scoreKey := scoreZSetKey(roundID)
	sumKey := scoreSumKey(roundID)
	ttl := roundTTL(rt.Round.EndAtMS)
	ttlSeconds := int64(0)
	if ttl > 0 {
		ttlSeconds = int64(ttl.Seconds())
		if ttlSeconds <= 0 {
			ttlSeconds = 1
		}
	}
	res, err := clickLua.Run(ctx, m.redis, []string{bitKey, scoreKey, sumKey}, bitOffset, deltaScore, ttlSeconds, scoreMember(userID)).Result()
	if err != nil {
		return 0, 0, false, err
	}
	arr, ok := res.([]interface{})
	if !ok || len(arr) < 3 {
		return 0, 0, false, errors.New("invalid redis response")
	}
	code, _ := arr[0].(int64)
	if code == 1 {
		return 0, 0, false, errors.New("already clicked")
	}
	totalScore := int64(0)
	switch v := arr[1].(type) {
	case int64:
		totalScore = v
	case float64:
		totalScore = int64(v)
	case string:
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			totalScore = parsed
		}
	}
	switch v := arr[2].(type) {
	case int64:
		deltaScore = int(v)
	case float64:
		deltaScore = int(v)
	case string:
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			deltaScore = int(parsed)
		}
	}

	return deltaScore, int(totalScore), isBomb, nil
}

func clickBitmapKey(roundID, userID, startAtMS int64) string {
	return "round:" + itoa(roundID) + ":start:" + itoa(startAtMS) + ":user:" + itoa(userID) + ":clicks"
}

func scoreZSetKey(roundID int64) string {
	return "round:" + itoa(roundID) + ":scores"
}

func scoreSumKey(roundID int64) string {
	return "round:" + itoa(roundID) + ":score_sum"
}

func scoreMember(userID int64) string {
	return "u:" + itoa(userID)
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}

func (m *Manager) WindowMS() int {
	return m.windowMS
}

func roundTTL(endAtMS int64) time.Duration {
	if endAtMS <= 0 {
		return 2 * time.Hour
	}
	ttl := time.Until(time.UnixMilli(endAtMS).Add(2 * time.Hour))
	if ttl < time.Minute {
		return 2 * time.Hour
	}
	return ttl
}

func newRevealSalt() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(buf)
}

func buildOffsets(manifest SliceManifest, seed uint32) []int {
	rng := NewXorShift32(seed)
	offsets := make([]int, manifest.DropCount)
	maxOffset := manifest.DurationMS - manifest.WindowMS
	if maxOffset < 0 {
		maxOffset = 0
	}
	for i := 0; i < manifest.DropCount; i++ {
		offsets[i] = int(math.Floor(rng.Float64() * float64(maxOffset+1)))
	}
	return offsets
}
