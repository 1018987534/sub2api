package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	LotteryDrawModeAuto            = "auto"
	LotteryDrawModeManual          = "manual"
	LotteryRoundModeAuto           = "auto"
	LotteryRoundModeManual         = "manual"
	LotteryRoundStatusOpen         = "open"
	LotteryRoundStatusDrawn        = "drawn"
	lotteryAdvisoryLock      int64 = 0x4c4f5454455259
	lotteryRecentWinnerLimit       = 10000
)

var (
	ErrLotteryDisabled                     = infraerrors.NotFound("LOTTERY_DISABLED", "lottery is disabled")
	ErrLotteryNoOpenRound                  = infraerrors.Conflict("LOTTERY_NO_OPEN_ROUND", "there is no open lottery round")
	ErrLotteryRoundAlreadyOpen             = infraerrors.Conflict("LOTTERY_ROUND_ALREADY_OPEN", "an open lottery round already exists")
	ErrLotteryAlreadyJoined                = infraerrors.Conflict("LOTTERY_ALREADY_JOINED", "you already joined this lottery round")
	ErrLotteryProgressFull                 = infraerrors.Conflict("LOTTERY_PROGRESS_FULL", "lottery progress is already full")
	ErrLotteryNotEligible                  = infraerrors.Forbidden("LOTTERY_NOT_ELIGIBLE", "you are not eligible for this lottery round")
	ErrLotteryRoundNotFound                = infraerrors.NotFound("LOTTERY_ROUND_NOT_FOUND", "lottery round not found")
	ErrLotteryInsufficientRealParticipants = infraerrors.BadRequest("LOTTERY_NOT_ENOUGH_REAL_PARTICIPANTS", "not enough real participants to draw all prizes")
	ErrLotteryProgressInvalid              = infraerrors.BadRequest("LOTTERY_PROGRESS_INVALID", "participant progress must be between the real participant count and the draw threshold")
)

type LotteryConfig struct {
	Enabled              bool      `json:"enabled"`
	ParticipantThreshold int       `json:"participant_threshold"`
	PrizeCount           int       `json:"prize_count"`
	PrizeAmount          float64   `json:"prize_amount"`
	DrawMode             string    `json:"draw_mode"`
	NextRoundMode        string    `json:"next_round_mode"`
	RequireRecharge      bool      `json:"require_recharge"`
	MinRechargeAmount    float64   `json:"min_recharge_amount"`
	MinAccountAgeDays    int       `json:"min_account_age_days"`
	RecentRechargeDays   int       `json:"recent_recharge_days"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type LotteryRound struct {
	ID                   int64      `json:"id"`
	RoundNo              int64      `json:"round_no"`
	Status               string     `json:"status"`
	ParticipantThreshold int        `json:"participant_threshold"`
	PrizeCount           int        `json:"prize_count"`
	PrizeAmount          float64    `json:"prize_amount"`
	DrawMode             string     `json:"draw_mode"`
	NextRoundMode        string     `json:"next_round_mode"`
	RequireRecharge      bool       `json:"require_recharge"`
	MinRechargeAmount    float64    `json:"min_recharge_amount"`
	MinAccountAgeDays    int        `json:"min_account_age_days"`
	RecentRechargeDays   int        `json:"recent_recharge_days"`
	ParticipantCount     int        `json:"participant_count"`
	ManualProgressCount  int        `json:"manual_progress_count"`
	RealParticipantCount int        `json:"real_participant_count"`
	WinnerCount          int        `json:"winner_count"`
	UniqueIPCount        int        `json:"unique_ip_count"`
	StartedAt            time.Time  `json:"started_at"`
	DrawnAt              *time.Time `json:"drawn_at,omitempty"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type LotteryPublicRound struct {
	ID                   int64      `json:"id"`
	RoundNo              int64      `json:"round_no"`
	Status               string     `json:"status"`
	DrawMode             string     `json:"draw_mode"`
	PrizeAmount          float64    `json:"prize_amount"`
	ParticipantThreshold int        `json:"participant_threshold"`
	PrizeCount           int        `json:"prize_count"`
	RequireRecharge      bool       `json:"require_recharge"`
	MinRechargeAmount    float64    `json:"min_recharge_amount"`
	MinAccountAgeDays    int        `json:"min_account_age_days"`
	ParticipantCount     int        `json:"participant_count"`
	WinnerCount          int        `json:"winner_count"`
	StartedAt            time.Time  `json:"started_at"`
	DrawnAt              *time.Time `json:"drawn_at,omitempty"`
}

type LotteryWinner struct {
	ID             int64     `json:"id"`
	RoundID        int64     `json:"round_id"`
	RoundNo        int64     `json:"round_no"`
	Email          string    `json:"email"`
	PrizeAmount    float64   `json:"prize_amount"`
	AwardedAt      time.Time `json:"awarded_at"`
	ParticipatedAt time.Time `json:"participated_at"`
}

type LotteryEligibility struct {
	Eligible               bool    `json:"eligible"`
	Reason                 string  `json:"reason,omitempty"`
	TotalRecharge          float64 `json:"total_recharge"`
	HiddenRecentRuleFailed bool    `json:"-"`
}

type LotteryCurrent struct {
	Enabled         bool                `json:"enabled"`
	CurrentRound    *LotteryPublicRound `json:"current_round,omitempty"`
	Joined          bool                `json:"joined"`
	Eligibility     LotteryEligibility  `json:"eligibility"`
	RecentWinners   []LotteryWinner     `json:"recent_winners"`
	MyRecentWinners []LotteryWinner     `json:"my_recent_winners"`
}

// LotteryAnnouncement is the intentionally small public payload consumed by
// external notification integrations. It omits eligibility internals,
// participant details, and account balances.
type LotteryAnnouncement struct {
	Enabled       bool                `json:"enabled"`
	CurrentRound  *LotteryPublicRound `json:"current_round,omitempty"`
	RecentWinners []LotteryWinner     `json:"recent_winners"`
}

type LotteryRoundPage struct {
	Items    []LotteryRound `json:"items"`
	Total    int64          `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	Pages    int            `json:"pages"`
}

type LotteryParticipant struct {
	ID       int64     `json:"id"`
	RoundID  int64     `json:"round_id"`
	UserID   int64     `json:"user_id"`
	Username string    `json:"username"`
	Email    string    `json:"email"`
	ClientIP string    `json:"client_ip"`
	JoinedAt time.Time `json:"joined_at"`
}

type LotteryParticipantPage struct {
	Items    []LotteryParticipant `json:"items"`
	Total    int64                `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
	Pages    int                  `json:"pages"`
}

type LotteryPublicRoundPage struct {
	Items    []LotteryPublicRound `json:"items"`
	Total    int64                `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
	Pages    int                  `json:"pages"`
}

type LotteryJoinResult struct {
	RoundID          int64     `json:"round_id"`
	RoundNo          int64     `json:"round_no"`
	ParticipantCount int       `json:"participant_count"`
	JoinedAt         time.Time `json:"joined_at"`
}

type LotteryDrawResult struct {
	Round   LotteryRound    `json:"round"`
	Winners []LotteryWinner `json:"winners"`
	Next    *LotteryRound   `json:"next_round,omitempty"`
}

type LotteryService struct {
	db           *sql.DB
	authCache    APIKeyAuthCacheInvalidator
	billingCache *BillingCacheService
	now          func() time.Time
}

func NewLotteryService(db *sql.DB, authCache APIKeyAuthCacheInvalidator, billingCache *BillingCacheService) *LotteryService {
	return &LotteryService{
		db:           db,
		authCache:    authCache,
		billingCache: billingCache,
		now:          time.Now,
	}
}

func ValidateLotteryConfig(cfg LotteryConfig) error {
	if cfg.ParticipantThreshold < 2 || cfg.ParticipantThreshold > 100000 {
		return infraerrors.BadRequest("LOTTERY_CONFIG_INVALID", "participant threshold must be between 2 and 100000")
	}
	if cfg.PrizeCount < 1 || cfg.PrizeCount > 10000 {
		return infraerrors.BadRequest("LOTTERY_CONFIG_INVALID", "prize count must be between 1 and 10000")
	}
	if cfg.PrizeAmount <= 0 || math.IsNaN(cfg.PrizeAmount) || math.IsInf(cfg.PrizeAmount, 0) {
		return infraerrors.BadRequest("LOTTERY_CONFIG_INVALID", "prize amount must be positive")
	}
	if cfg.DrawMode != LotteryDrawModeAuto && cfg.DrawMode != LotteryDrawModeManual {
		return infraerrors.BadRequest("LOTTERY_CONFIG_INVALID", "draw mode must be auto or manual")
	}
	if cfg.NextRoundMode != LotteryRoundModeAuto && cfg.NextRoundMode != LotteryRoundModeManual {
		return infraerrors.BadRequest("LOTTERY_CONFIG_INVALID", "next round mode must be auto or manual")
	}
	if cfg.MinRechargeAmount < 0 || math.IsNaN(cfg.MinRechargeAmount) || math.IsInf(cfg.MinRechargeAmount, 0) {
		return infraerrors.BadRequest("LOTTERY_CONFIG_INVALID", "minimum recharge amount cannot be negative")
	}
	if cfg.MinAccountAgeDays < 0 || cfg.MinAccountAgeDays > 36500 || cfg.RecentRechargeDays < 0 || cfg.RecentRechargeDays > 36500 {
		return infraerrors.BadRequest("LOTTERY_CONFIG_INVALID", "eligibility day values must be between 0 and 36500")
	}
	if cfg.PrizeCount > cfg.ParticipantThreshold {
		return infraerrors.BadRequest("LOTTERY_CONFIG_INVALID", "prize count cannot exceed participant threshold")
	}
	return nil
}

const lotteryConfigSelect = `
	SELECT COALESCE((SELECT LOWER(TRIM(value)) = 'true' FROM settings WHERE key = 'lottery_enabled'), FALSE),
	       participant_threshold, prize_count, prize_amount, draw_mode, next_round_mode,
	       require_recharge, min_recharge_amount, min_account_age_days,
	       recent_recharge_days, updated_at
	FROM lottery_config WHERE id = 1`

type lotteryRowScanner interface {
	Scan(dest ...any) error
}

func scanLotteryConfig(row lotteryRowScanner) (LotteryConfig, error) {
	var cfg LotteryConfig
	err := row.Scan(
		&cfg.Enabled, &cfg.ParticipantThreshold, &cfg.PrizeCount, &cfg.PrizeAmount,
		&cfg.DrawMode, &cfg.NextRoundMode, &cfg.RequireRecharge,
		&cfg.MinRechargeAmount, &cfg.MinAccountAgeDays, &cfg.RecentRechargeDays, &cfg.UpdatedAt,
	)
	return cfg, err
}

func (s *LotteryService) GetConfig(ctx context.Context) (LotteryConfig, error) {
	if s == nil || s.db == nil {
		return LotteryConfig{}, errors.New("lottery database is unavailable")
	}
	return scanLotteryConfig(s.db.QueryRowContext(ctx, lotteryConfigSelect))
}

func (s *LotteryService) UpdateConfig(ctx context.Context, cfg LotteryConfig) (LotteryConfig, error) {
	if err := ValidateLotteryConfig(cfg); err != nil {
		return LotteryConfig{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LotteryConfig{}, err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
		UPDATE lottery_config SET participant_threshold=$1, prize_count=$2, prize_amount=$3,
			draw_mode=$4, next_round_mode=$5, require_recharge=$6,
			min_recharge_amount=$7, min_account_age_days=$8, recent_recharge_days=$9,
			updated_at=NOW() WHERE id=1`,
		cfg.ParticipantThreshold, cfg.PrizeCount, cfg.PrizeAmount, cfg.DrawMode,
		cfg.NextRoundMode, cfg.RequireRecharge, cfg.MinRechargeAmount,
		cfg.MinAccountAgeDays, cfg.RecentRechargeDays,
	)
	if err != nil {
		return LotteryConfig{}, fmt.Errorf("update lottery config: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO settings (key,value,updated_at) VALUES ('lottery_enabled',$1,NOW()) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, fmt.Sprintf("%t", cfg.Enabled))
	if err != nil {
		return LotteryConfig{}, fmt.Errorf("update lottery switch: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LotteryConfig{}, err
	}
	return s.GetConfig(ctx)
}

const lotteryRoundColumns = `
	r.id,r.round_no,r.status,r.participant_threshold,r.prize_count,r.prize_amount,
	r.draw_mode,r.next_round_mode,r.require_recharge,
	r.min_recharge_amount,r.min_account_age_days,r.recent_recharge_days,
	r.participant_count,r.manual_progress_count,
	COALESCE((SELECT COUNT(*) FROM lottery_participants p WHERE p.round_id=r.id AND p.is_actor=FALSE),0),
	r.winner_count,
	COALESCE((SELECT COUNT(DISTINCT p.client_ip) FROM lottery_participants p WHERE p.round_id=r.id AND p.client_ip IS NOT NULL AND p.client_ip<>''),0),
	r.started_at,r.drawn_at,r.updated_at`

func scanLotteryRound(row lotteryRowScanner) (LotteryRound, error) {
	var round LotteryRound
	err := row.Scan(
		&round.ID, &round.RoundNo, &round.Status, &round.ParticipantThreshold,
		&round.PrizeCount, &round.PrizeAmount, &round.DrawMode, &round.NextRoundMode,
		&round.RequireRecharge, &round.MinRechargeAmount,
		&round.MinAccountAgeDays, &round.RecentRechargeDays, &round.ParticipantCount,
		&round.ManualProgressCount, &round.RealParticipantCount, &round.WinnerCount, &round.UniqueIPCount,
		&round.StartedAt, &round.DrawnAt, &round.UpdatedAt,
	)
	return round, err
}

func toLotteryPublicRound(round LotteryRound) LotteryPublicRound {
	return LotteryPublicRound{
		ID: round.ID, RoundNo: round.RoundNo, Status: round.Status,
		DrawMode:    round.DrawMode,
		PrizeAmount: round.PrizeAmount, ParticipantThreshold: round.ParticipantThreshold,
		PrizeCount: round.PrizeCount, RequireRecharge: round.RequireRecharge,
		MinRechargeAmount: round.MinRechargeAmount, MinAccountAgeDays: round.MinAccountAgeDays,
		ParticipantCount: round.ParticipantCount, WinnerCount: round.WinnerCount,
		StartedAt: round.StartedAt, DrawnAt: round.DrawnAt,
	}
}

func (s *LotteryService) StartRound(ctx context.Context, createdBy int64) (LotteryRound, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LotteryRound{}, err
	}
	defer func() { _ = tx.Rollback() }()
	cfg, err := scanLotteryConfig(tx.QueryRowContext(ctx, lotteryConfigSelect+` FOR UPDATE`))
	if err != nil {
		return LotteryRound{}, err
	}
	if !cfg.Enabled {
		return LotteryRound{}, ErrLotteryDisabled
	}
	round, err := s.insertRoundTx(ctx, tx, cfg, createdBy)
	if err != nil {
		return LotteryRound{}, err
	}
	if err := tx.Commit(); err != nil {
		return LotteryRound{}, err
	}
	return round, nil
}

func (s *LotteryService) insertRoundTx(ctx context.Context, tx *sql.Tx, cfg LotteryConfig, createdBy int64) (LotteryRound, error) {
	var openID int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM lottery_rounds WHERE status='open' FOR UPDATE`).Scan(&openID)
	if err == nil {
		return LotteryRound{}, ErrLotteryRoundAlreadyOpen
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return LotteryRound{}, err
	}
	var roundNo int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(round_no),0)+1 FROM lottery_rounds`).Scan(&roundNo); err != nil {
		return LotteryRound{}, err
	}
	var creator any
	if createdBy > 0 {
		creator = createdBy
	}
	query := `INSERT INTO lottery_rounds (
		round_no,status,participant_threshold,prize_count,prize_amount,draw_mode,next_round_mode,
		actor_percentage,actor_target_count,actor_join_min_seconds,actor_join_max_seconds,
		require_recharge,min_recharge_amount,min_account_age_days,recent_recharge_days,
		next_actor_at,created_by
	) VALUES ($1,'open',$2,$3,$4,$5,$6,0,0,5,5,$7,$8,$9,$10,NULL,$11)
	RETURNING id`
	var roundID int64
	err = tx.QueryRowContext(ctx, query,
		roundNo, cfg.ParticipantThreshold, cfg.PrizeCount, cfg.PrizeAmount,
		cfg.DrawMode, cfg.NextRoundMode, cfg.RequireRecharge,
		cfg.MinRechargeAmount, cfg.MinAccountAgeDays, cfg.RecentRechargeDays,
		creator,
	).Scan(&roundID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "idx_lottery_rounds_one_open") {
			return LotteryRound{}, ErrLotteryRoundAlreadyOpen
		}
		return LotteryRound{}, err
	}
	return scanLotteryRound(tx.QueryRowContext(ctx, `SELECT `+lotteryRoundColumns+` FROM lottery_rounds r WHERE r.id=$1`, roundID))
}

func (s *LotteryService) GetCurrent(ctx context.Context, userID int64) (LotteryCurrent, error) {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return LotteryCurrent{}, err
	}
	out := LotteryCurrent{Enabled: cfg.Enabled, RecentWinners: []LotteryWinner{}, MyRecentWinners: []LotteryWinner{}}
	if !cfg.Enabled {
		return out, nil
	}
	round, err := scanLotteryRound(s.db.QueryRowContext(ctx, `SELECT `+lotteryRoundColumns+` FROM lottery_rounds r WHERE r.status='open'`))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return LotteryCurrent{}, err
	}
	if err == nil {
		publicRound := toLotteryPublicRound(round)
		out.CurrentRound = &publicRound
		if userID > 0 {
			out.Joined, err = s.hasJoined(ctx, round.ID, userID)
			if err != nil {
				return LotteryCurrent{}, err
			}
			out.Eligibility, err = s.checkEligibility(ctx, userID, round)
			if err != nil {
				return LotteryCurrent{}, err
			}
			if out.Joined {
				out.Eligibility.Eligible = true
				out.Eligibility.Reason = ""
			}
		}
	}
	out.RecentWinners, err = s.listWinners(ctx, 0, lotteryRecentWinnerLimit)
	if err != nil {
		return LotteryCurrent{}, err
	}
	if userID > 0 {
		out.MyRecentWinners, err = s.listWinners(ctx, userID, 20)
		if err != nil {
			return LotteryCurrent{}, err
		}
	}
	return out, nil
}

// GetAnnouncement returns the public lottery state used by the QQ notifier.
// GetCurrent masks winner emails and excludes the hidden recharge rule from its
// JSON representation, so the integration cannot expose private data.
func (s *LotteryService) GetAnnouncement(ctx context.Context) (LotteryAnnouncement, error) {
	current, err := s.GetCurrent(ctx, 0)
	if err != nil {
		return LotteryAnnouncement{}, err
	}
	return LotteryAnnouncement{
		Enabled:       current.Enabled,
		CurrentRound:  current.CurrentRound,
		RecentWinners: current.RecentWinners,
	}, nil
}

func (s *LotteryService) hasJoined(ctx context.Context, roundID, userID int64) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM lottery_participants WHERE round_id=$1 AND user_id=$2)`, roundID, userID).Scan(&exists)
	return exists, err
}

func (s *LotteryService) checkEligibility(ctx context.Context, userID int64, round LotteryRound) (LotteryEligibility, error) {
	var createdAt time.Time
	var total float64
	var lastRecharge sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT u.created_at,
		       COALESCE(SUM(CASE WHEN rc.type IN ('balance','admin_balance') AND rc.value>0 AND rc.used_at IS NOT NULL THEN rc.value ELSE 0 END),0),
		       MAX(CASE WHEN rc.type IN ('balance','admin_balance') AND rc.value>0 THEN rc.used_at END)
		FROM users u
		LEFT JOIN redeem_codes rc ON rc.used_by=u.id
		WHERE u.id=$1 AND u.deleted_at IS NULL AND u.status='active'
		GROUP BY u.created_at`, userID).Scan(&createdAt, &total, &lastRecharge)
	if errors.Is(err, sql.ErrNoRows) {
		return LotteryEligibility{Reason: "not_eligible"}, nil
	}
	if err != nil {
		return LotteryEligibility{}, err
	}
	eligibility := LotteryEligibility{Eligible: true, TotalRecharge: total}
	if round.RequireRecharge && total <= 0 {
		eligibility.Eligible = false
		eligibility.Reason = "recharge_required"
		return eligibility, nil
	}
	if round.MinRechargeAmount > 0 && total+1e-9 < round.MinRechargeAmount {
		eligibility.Eligible = false
		eligibility.Reason = "recharge_amount"
		return eligibility, nil
	}
	if round.MinAccountAgeDays > 0 && createdAt.After(s.now().AddDate(0, 0, -round.MinAccountAgeDays)) {
		eligibility.Eligible = false
		eligibility.Reason = "account_age"
		return eligibility, nil
	}
	if round.RecentRechargeDays > 0 && (!lastRecharge.Valid || lastRecharge.Time.Before(s.now().AddDate(0, 0, -round.RecentRechargeDays))) {
		eligibility.Eligible = false
		eligibility.Reason = "not_eligible"
		eligibility.HiddenRecentRuleFailed = true
	}
	return eligibility, nil
}

func (s *LotteryService) Join(ctx context.Context, userID int64, clientIP string) (LotteryJoinResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LotteryJoinResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	cfg, err := scanLotteryConfig(tx.QueryRowContext(ctx, lotteryConfigSelect))
	if err != nil {
		return LotteryJoinResult{}, err
	}
	if !cfg.Enabled {
		return LotteryJoinResult{}, ErrLotteryDisabled
	}
	round, err := scanLotteryRound(tx.QueryRowContext(ctx, `SELECT `+lotteryRoundColumns+` FROM lottery_rounds r WHERE r.status='open' FOR UPDATE OF r`))
	if errors.Is(err, sql.ErrNoRows) {
		return LotteryJoinResult{}, ErrLotteryNoOpenRound
	}
	if err != nil {
		return LotteryJoinResult{}, err
	}
	var existingJoinedAt time.Time
	err = tx.QueryRowContext(ctx, `SELECT joined_at FROM lottery_participants WHERE round_id=$1 AND user_id=$2`, round.ID, userID).Scan(&existingJoinedAt)
	if err == nil {
		return LotteryJoinResult{}, ErrLotteryAlreadyJoined
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return LotteryJoinResult{}, err
	}
	if round.ParticipantCount >= round.ParticipantThreshold {
		return LotteryJoinResult{}, ErrLotteryProgressFull
	}
	eligibility, err := s.checkEligibilityTx(ctx, tx, userID, round)
	if err != nil {
		return LotteryJoinResult{}, err
	}
	if !eligibility.Eligible {
		return LotteryJoinResult{}, ErrLotteryNotEligible.WithMetadata(map[string]string{"reason": eligibility.Reason})
	}
	clientIP = strings.TrimSpace(clientIP)
	if len(clientIP) > 64 {
		clientIP = clientIP[:64]
	}
	var joinedAt time.Time
	err = tx.QueryRowContext(ctx, `INSERT INTO lottery_participants (round_id,user_id,is_actor,client_ip) VALUES ($1,$2,FALSE,NULLIF($3,'')) RETURNING joined_at`, round.ID, userID, clientIP).Scan(&joinedAt)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "idx_lottery_participants_round_user") {
			return LotteryJoinResult{}, ErrLotteryAlreadyJoined
		}
		return LotteryJoinResult{}, err
	}
	if err := tx.QueryRowContext(ctx, `UPDATE lottery_rounds SET participant_count=participant_count+1,updated_at=NOW() WHERE id=$1 RETURNING participant_count`, round.ID).Scan(&round.ParticipantCount); err != nil {
		return LotteryJoinResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return LotteryJoinResult{}, err
	}
	if round.DrawMode == LotteryDrawModeAuto && round.ParticipantCount >= round.ParticipantThreshold {
		go s.advanceWithTimeout()
	}
	return LotteryJoinResult{RoundID: round.ID, RoundNo: round.RoundNo, ParticipantCount: round.ParticipantCount, JoinedAt: joinedAt}, nil
}

func (s *LotteryService) checkEligibilityTx(ctx context.Context, tx *sql.Tx, userID int64, round LotteryRound) (LotteryEligibility, error) {
	var createdAt time.Time
	var total float64
	var lastRecharge sql.NullTime
	err := tx.QueryRowContext(ctx, `
		SELECT u.created_at,
		       COALESCE(SUM(CASE WHEN rc.type IN ('balance','admin_balance') AND rc.value>0 AND rc.used_at IS NOT NULL THEN rc.value ELSE 0 END),0),
		       MAX(CASE WHEN rc.type IN ('balance','admin_balance') AND rc.value>0 THEN rc.used_at END)
		FROM users u LEFT JOIN redeem_codes rc ON rc.used_by=u.id
		WHERE u.id=$1 AND u.deleted_at IS NULL AND u.status='active'
		GROUP BY u.created_at`, userID).Scan(&createdAt, &total, &lastRecharge)
	if errors.Is(err, sql.ErrNoRows) {
		return LotteryEligibility{Reason: "not_eligible"}, nil
	}
	if err != nil {
		return LotteryEligibility{}, err
	}
	eligibility := LotteryEligibility{Eligible: true, TotalRecharge: total}
	if round.RequireRecharge && total <= 0 {
		eligibility.Eligible, eligibility.Reason = false, "recharge_required"
	} else if round.MinRechargeAmount > 0 && total+1e-9 < round.MinRechargeAmount {
		eligibility.Eligible, eligibility.Reason = false, "recharge_amount"
	} else if round.MinAccountAgeDays > 0 && createdAt.After(s.now().AddDate(0, 0, -round.MinAccountAgeDays)) {
		eligibility.Eligible, eligibility.Reason = false, "account_age"
	} else if round.RecentRechargeDays > 0 && (!lastRecharge.Valid || lastRecharge.Time.Before(s.now().AddDate(0, 0, -round.RecentRechargeDays))) {
		eligibility.Eligible, eligibility.Reason, eligibility.HiddenRecentRuleFailed = false, "not_eligible", true
	}
	return eligibility, nil
}

func (s *LotteryService) ListRounds(ctx context.Context, page, pageSize int) (LotteryRoundPage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 8
	}
	if pageSize > 100 {
		pageSize = 100
	}
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM lottery_rounds`).Scan(&total); err != nil {
		return LotteryRoundPage{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+lotteryRoundColumns+` FROM lottery_rounds r ORDER BY r.round_no DESC LIMIT $1 OFFSET $2`, pageSize, (page-1)*pageSize)
	if err != nil {
		return LotteryRoundPage{}, err
	}
	defer rows.Close()
	items := make([]LotteryRound, 0, pageSize)
	for rows.Next() {
		round, scanErr := scanLotteryRound(rows)
		if scanErr != nil {
			return LotteryRoundPage{}, scanErr
		}
		items = append(items, round)
	}
	if err := rows.Err(); err != nil {
		return LotteryRoundPage{}, err
	}
	pages := int((total + int64(pageSize) - 1) / int64(pageSize))
	return LotteryRoundPage{Items: items, Total: total, Page: page, PageSize: pageSize, Pages: pages}, nil
}

func (s *LotteryService) ListParticipants(ctx context.Context, roundID int64, page, pageSize int) (LotteryParticipantPage, error) {
	if roundID <= 0 {
		return LotteryParticipantPage{}, ErrLotteryRoundNotFound
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	var total int64
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(p.id)
		FROM lottery_rounds r
		LEFT JOIN lottery_participants p ON p.round_id=r.id AND p.is_actor=FALSE
		WHERE r.id=$1
		GROUP BY r.id`, roundID).Scan(&total)
	if errors.Is(err, sql.ErrNoRows) {
		return LotteryParticipantPage{}, ErrLotteryRoundNotFound
	}
	if err != nil {
		return LotteryParticipantPage{}, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id,p.round_id,p.user_id,COALESCE(u.username,''),u.email,
		       COALESCE(p.client_ip,''),p.joined_at
		FROM lottery_participants p
		JOIN users u ON u.id=p.user_id
		WHERE p.round_id=$1 AND p.is_actor=FALSE
		ORDER BY p.joined_at ASC,p.id ASC
		LIMIT $2 OFFSET $3`, roundID, pageSize, (page-1)*pageSize)
	if err != nil {
		return LotteryParticipantPage{}, err
	}
	defer rows.Close()

	items := make([]LotteryParticipant, 0, pageSize)
	for rows.Next() {
		var participant LotteryParticipant
		if err := rows.Scan(
			&participant.ID,
			&participant.RoundID,
			&participant.UserID,
			&participant.Username,
			&participant.Email,
			&participant.ClientIP,
			&participant.JoinedAt,
		); err != nil {
			return LotteryParticipantPage{}, err
		}
		items = append(items, participant)
	}
	if err := rows.Err(); err != nil {
		return LotteryParticipantPage{}, err
	}

	pages := int((total + int64(pageSize) - 1) / int64(pageSize))
	return LotteryParticipantPage{
		Items: items, Total: total, Page: page, PageSize: pageSize, Pages: pages,
	}, nil
}

func (s *LotteryService) UpdateProgress(ctx context.Context, roundID int64, participantCount int) (LotteryRound, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LotteryRound{}, err
	}
	defer func() { _ = tx.Rollback() }()

	round, err := scanLotteryRound(tx.QueryRowContext(ctx, `SELECT `+lotteryRoundColumns+` FROM lottery_rounds r WHERE r.id=$1 FOR UPDATE OF r`, roundID))
	if errors.Is(err, sql.ErrNoRows) {
		return LotteryRound{}, ErrLotteryNoOpenRound
	}
	if err != nil {
		return LotteryRound{}, err
	}
	if round.Status != LotteryRoundStatusOpen {
		return LotteryRound{}, infraerrors.Conflict("LOTTERY_ROUND_CLOSED", "lottery round is already closed")
	}
	if err := validateLotteryProgress(round, participantCount); err != nil {
		return LotteryRound{}, err
	}
	manualProgressCount := participantCount - round.RealParticipantCount
	if _, err := tx.ExecContext(ctx, `
		UPDATE lottery_rounds
		SET participant_count=$2,manual_progress_count=$3,updated_at=NOW()
		WHERE id=$1`, round.ID, participantCount, manualProgressCount); err != nil {
		return LotteryRound{}, err
	}
	updated, err := scanLotteryRound(tx.QueryRowContext(ctx, `SELECT `+lotteryRoundColumns+` FROM lottery_rounds r WHERE r.id=$1`, round.ID))
	if err != nil {
		return LotteryRound{}, err
	}
	if err := tx.Commit(); err != nil {
		return LotteryRound{}, err
	}
	if updated.DrawMode == LotteryDrawModeAuto && updated.ParticipantCount >= updated.ParticipantThreshold {
		go s.advanceWithTimeout()
	}
	return updated, nil
}

func validateLotteryProgress(round LotteryRound, participantCount int) error {
	if participantCount < round.RealParticipantCount || participantCount > round.ParticipantThreshold {
		return ErrLotteryProgressInvalid.WithMetadata(map[string]string{
			"real_participant_count": fmt.Sprintf("%d", round.RealParticipantCount),
			"participant_threshold":  fmt.Sprintf("%d", round.ParticipantThreshold),
		})
	}
	if participantCount == round.ParticipantThreshold && round.RealParticipantCount < round.PrizeCount {
		return ErrLotteryInsufficientRealParticipants
	}
	return nil
}

func (s *LotteryService) ListPublicRounds(ctx context.Context, page, pageSize int) (LotteryPublicRoundPage, error) {
	adminPage, err := s.ListRounds(ctx, page, pageSize)
	if err != nil {
		return LotteryPublicRoundPage{}, err
	}
	items := make([]LotteryPublicRound, 0, len(adminPage.Items))
	for _, round := range adminPage.Items {
		items = append(items, toLotteryPublicRound(round))
	}
	return LotteryPublicRoundPage{Items: items, Total: adminPage.Total, Page: adminPage.Page, PageSize: adminPage.PageSize, Pages: adminPage.Pages}, nil
}

func (s *LotteryService) listWinners(ctx context.Context, userID int64, limit int) ([]LotteryWinner, error) {
	query := `SELECT w.id,w.round_id,r.round_no,w.email_snapshot,w.prize_amount,w.awarded_at,p.joined_at FROM lottery_winners w JOIN lottery_rounds r ON r.id=w.round_id JOIN lottery_participants p ON p.id=w.participant_id`
	args := []any{}
	if userID > 0 {
		query += ` WHERE w.user_id=$1`
		args = append(args, userID)
	}
	query += fmt.Sprintf(` ORDER BY w.awarded_at DESC,w.id DESC LIMIT $%d`, len(args)+1)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]LotteryWinner, 0, limit)
	for rows.Next() {
		var winner LotteryWinner
		if err := rows.Scan(&winner.ID, &winner.RoundID, &winner.RoundNo, &winner.Email, &winner.PrizeAmount, &winner.AwardedAt, &winner.ParticipatedAt); err != nil {
			return nil, err
		}
		winner.Email = MaskLotteryEmail(winner.Email)
		items = append(items, winner)
	}
	return items, rows.Err()
}

func (s *LotteryService) listWinnersForRound(ctx context.Context, roundID int64, limit int) ([]LotteryWinner, error) {
	if limit < 1 {
		limit = 1
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT w.id,w.round_id,r.round_no,w.email_snapshot,w.prize_amount,w.awarded_at,p.joined_at
		FROM lottery_winners w
		JOIN lottery_rounds r ON r.id=w.round_id
		JOIN lottery_participants p ON p.id=w.participant_id
		WHERE w.round_id=$1
		ORDER BY w.id ASC LIMIT $2`, roundID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]LotteryWinner, 0, limit)
	for rows.Next() {
		var winner LotteryWinner
		if err := rows.Scan(&winner.ID, &winner.RoundID, &winner.RoundNo, &winner.Email, &winner.PrizeAmount, &winner.AwardedAt, &winner.ParticipatedAt); err != nil {
			return nil, err
		}
		winner.Email = MaskLotteryEmail(winner.Email)
		items = append(items, winner)
	}
	return items, rows.Err()
}

func MaskLotteryEmail(email string) string {
	parts := strings.SplitN(strings.TrimSpace(email), "@", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "***"
	}
	local := []rune(parts[0])
	visible := string(local[:1])
	if len(local) >= 4 {
		visible = string(local[:2]) + "***" + string(local[len(local)-1:])
	} else {
		visible += "***"
	}
	return visible + "@" + parts[1]
}

func (s *LotteryService) DrawRound(ctx context.Context, roundID int64) (LotteryDrawResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LotteryDrawResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	cfg, err := scanLotteryConfig(tx.QueryRowContext(ctx, lotteryConfigSelect+` FOR UPDATE`))
	if err != nil {
		return LotteryDrawResult{}, err
	}
	if !cfg.Enabled {
		return LotteryDrawResult{}, ErrLotteryDisabled
	}
	result, winnerIDs, err := s.drawRoundTx(ctx, tx, roundID, true, cfg)
	if err != nil {
		return LotteryDrawResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return LotteryDrawResult{}, err
	}
	s.invalidateWinnerBalances(winnerIDs)
	return result, nil
}

type lotteryWinnerCandidate struct {
	participantID int64
	userID        int64
	email         string
	joinedAt      time.Time
}

func (s *LotteryService) drawRoundTx(ctx context.Context, tx *sql.Tx, roundID int64, manual bool, cfg LotteryConfig) (LotteryDrawResult, []int64, error) {
	round, err := scanLotteryRound(tx.QueryRowContext(ctx, `SELECT `+lotteryRoundColumns+` FROM lottery_rounds r WHERE r.id=$1 FOR UPDATE OF r`, roundID))
	if errors.Is(err, sql.ErrNoRows) {
		return LotteryDrawResult{}, nil, ErrLotteryNoOpenRound
	}
	if err != nil {
		return LotteryDrawResult{}, nil, err
	}
	if round.Status != LotteryRoundStatusOpen {
		return LotteryDrawResult{}, nil, infraerrors.Conflict("LOTTERY_ROUND_CLOSED", "lottery round is already closed")
	}
	if !manual && round.ParticipantCount < round.ParticipantThreshold {
		return LotteryDrawResult{}, nil, nil
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT p.id,p.user_id,u.email,p.joined_at
		FROM lottery_participants p
		JOIN users u ON u.id=p.user_id AND u.deleted_at IS NULL AND u.status='active'
		WHERE p.round_id=$1 AND p.is_actor=FALSE
		ORDER BY random() LIMIT $2`, round.ID, round.PrizeCount)
	if err != nil {
		return LotteryDrawResult{}, nil, err
	}
	candidates := make([]lotteryWinnerCandidate, 0, round.PrizeCount)
	for rows.Next() {
		var candidate lotteryWinnerCandidate
		if err := rows.Scan(&candidate.participantID, &candidate.userID, &candidate.email, &candidate.joinedAt); err != nil {
			_ = rows.Close()
			return LotteryDrawResult{}, nil, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Close(); err != nil {
		return LotteryDrawResult{}, nil, err
	}
	if len(candidates) < round.PrizeCount {
		return LotteryDrawResult{}, nil, ErrLotteryInsufficientRealParticipants
	}
	now := s.now()
	winners := make([]LotteryWinner, 0, len(candidates))
	winnerIDs := make([]int64, 0, len(candidates))
	for _, candidate := range candidates {
		var before, after float64
		if err := tx.QueryRowContext(ctx, `UPDATE users SET balance=balance+$1,updated_at=NOW() WHERE id=$2 AND deleted_at IS NULL RETURNING balance-$1,balance`, round.PrizeAmount, candidate.userID).Scan(&before, &after); err != nil {
			return LotteryDrawResult{}, nil, err
		}
		var winnerID int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO lottery_winners (round_id,participant_id,user_id,email_snapshot,prize_amount,balance_before,balance_after,awarded_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, round.ID, candidate.participantID, candidate.userID, candidate.email, round.PrizeAmount, before, after, now).Scan(&winnerID); err != nil {
			return LotteryDrawResult{}, nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO lottery_balance_ledger (winner_id,round_id,user_id,amount,balance_before,balance_after,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`, winnerID, round.ID, candidate.userID, round.PrizeAmount, before, after, now); err != nil {
			return LotteryDrawResult{}, nil, err
		}
		winners = append(winners, LotteryWinner{ID: winnerID, RoundID: round.ID, RoundNo: round.RoundNo, Email: MaskLotteryEmail(candidate.email), PrizeAmount: round.PrizeAmount, AwardedAt: now, ParticipatedAt: candidate.joinedAt})
		winnerIDs = append(winnerIDs, candidate.userID)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE lottery_rounds SET status='drawn',winner_count=$2,drawn_at=$3,updated_at=$3 WHERE id=$1`, round.ID, len(winners), now); err != nil {
		return LotteryDrawResult{}, nil, err
	}
	round.Status, round.WinnerCount, round.DrawnAt, round.UpdatedAt = LotteryRoundStatusDrawn, len(winners), &now, now
	result := LotteryDrawResult{Round: round, Winners: winners}
	if round.NextRoundMode == LotteryRoundModeAuto && cfg.Enabled {
		next, err := s.insertRoundTx(ctx, tx, cfg, 0)
		if err != nil {
			return LotteryDrawResult{}, nil, err
		}
		result.Next = &next
	}
	return result, winnerIDs, nil
}

func (s *LotteryService) invalidateWinnerBalances(userIDs []int64) {
	if len(userIDs) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, userID := range userIDs {
		if s.authCache != nil {
			s.authCache.InvalidateAuthCacheByUserID(ctx, userID)
		}
		if s.billingCache != nil {
			_ = s.billingCache.InvalidateUserBalance(ctx, userID)
		}
	}
}

func (s *LotteryService) Advance(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, lotteryAdvisoryLock).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return tx.Commit()
	}
	cfg, err := scanLotteryConfig(tx.QueryRowContext(ctx, lotteryConfigSelect+` FOR UPDATE`))
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return tx.Commit()
	}
	round, err := scanLotteryRound(tx.QueryRowContext(ctx, `SELECT `+lotteryRoundColumns+` FROM lottery_rounds r WHERE r.status='open' FOR UPDATE OF r`))
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	var winnerIDs []int64
	if round.DrawMode == LotteryDrawModeAuto && round.ParticipantCount >= round.ParticipantThreshold {
		_, winnerIDs, err = s.drawRoundTx(ctx, tx, round.ID, false, cfg)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.invalidateWinnerBalances(winnerIDs)
	return nil
}

func (s *LotteryService) advanceWithTimeout() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.Advance(ctx); err != nil {
		slog.Error("lottery advance failed", "error", err)
	}
}
