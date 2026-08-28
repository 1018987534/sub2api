package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/wenlng/go-captcha-assets/resources/imagesv2"
	"github.com/wenlng/go-captcha-assets/resources/tiles"
	"github.com/wenlng/go-captcha/v2/slide"
)

const lotteryCaptchaTTL = 2 * time.Minute

var (
	ErrLotteryCaptchaRecordNotFound = errors.New("lottery captcha record not found")
	ErrLotteryCaptchaInvalid        = infraerrors.BadRequest("LOTTERY_CAPTCHA_INVALID", "slider verification failed")
	ErrLotteryCaptchaExpired        = infraerrors.BadRequest("LOTTERY_CAPTCHA_EXPIRED", "slider challenge expired; please try again")
	ErrLotteryCaptchaUnavailable    = infraerrors.ServiceUnavailable("LOTTERY_CAPTCHA_UNAVAILABLE", "slider verification is temporarily unavailable")
)

type LotteryCaptchaRecord struct {
	UserID   int64  `json:"user_id"`
	ClientIP string `json:"client_ip"`
	TargetX  int    `json:"target_x"`
	TargetY  int    `json:"target_y"`
}

type LotteryCaptchaStore interface {
	Save(ctx context.Context, id string, record LotteryCaptchaRecord, ttl time.Duration) error
	Take(ctx context.Context, id string) (LotteryCaptchaRecord, error)
}

type LotteryCaptchaChallenge struct {
	ID          string `json:"id"`
	Image       string `json:"image"`
	Thumb       string `json:"thumb"`
	ThumbX      int    `json:"thumb_x"`
	ThumbY      int    `json:"thumb_y"`
	ThumbWidth  int    `json:"thumb_width"`
	ThumbHeight int    `json:"thumb_height"`
	ExpiresIn   int    `json:"expires_in"`
}

type LotteryCaptchaService struct {
	store     LotteryCaptchaStore
	generator slide.Captcha
	mu        sync.Mutex
	ttl       time.Duration
}

func NewLotteryCaptchaService(store LotteryCaptchaStore) (*LotteryCaptchaService, error) {
	if store == nil {
		return nil, errors.New("lottery captcha store is required")
	}

	backgrounds, err := imagesv2.GetImages()
	if err != nil {
		return nil, fmt.Errorf("load lottery captcha backgrounds: %w", err)
	}
	tileAssets, err := tiles.GetTiles()
	if err != nil {
		return nil, fmt.Errorf("load lottery captcha tiles: %w", err)
	}
	graphs := make([]*slide.GraphImage, 0, len(tileAssets))
	for _, asset := range tileAssets {
		graphs = append(graphs, &slide.GraphImage{
			OverlayImage: asset.OverlayImage,
			MaskImage:    asset.MaskImage,
			ShadowImage:  asset.ShadowImage,
		})
	}

	builder := slide.NewBuilder()
	builder.SetResources(
		slide.WithGraphImages(graphs),
		slide.WithBackgrounds(backgrounds),
	)
	return &LotteryCaptchaService{
		store: store, generator: builder.Make(), ttl: lotteryCaptchaTTL,
	}, nil
}

func (s *LotteryCaptchaService) Generate(ctx context.Context, userID int64, clientIP string) (LotteryCaptchaChallenge, error) {
	if s == nil || s.store == nil || s.generator == nil || userID <= 0 {
		return LotteryCaptchaChallenge{}, ErrLotteryCaptchaUnavailable
	}

	s.mu.Lock()
	data, err := s.generator.Generate()
	s.mu.Unlock()
	if err != nil || data == nil || data.GetData() == nil {
		slog.Error("generate lottery slider challenge failed", "error", err)
		return LotteryCaptchaChallenge{}, ErrLotteryCaptchaUnavailable
	}

	imageBase64, err := data.GetMasterImage().ToBase64()
	if err != nil {
		slog.Error("encode lottery slider background failed", "error", err)
		return LotteryCaptchaChallenge{}, ErrLotteryCaptchaUnavailable
	}
	thumbBase64, err := data.GetTileImage().ToBase64()
	if err != nil {
		slog.Error("encode lottery slider tile failed", "error", err)
		return LotteryCaptchaChallenge{}, ErrLotteryCaptchaUnavailable
	}

	block := data.GetData()
	id := uuid.NewString()
	record := LotteryCaptchaRecord{
		UserID: userID, ClientIP: normalizeLotteryCaptchaIP(clientIP), TargetX: block.X, TargetY: block.Y,
	}
	if err := s.store.Save(ctx, id, record, s.ttl); err != nil {
		slog.Error("save lottery slider challenge failed", "error", err)
		return LotteryCaptchaChallenge{}, ErrLotteryCaptchaUnavailable
	}

	return LotteryCaptchaChallenge{
		ID: id, Image: imageBase64, Thumb: thumbBase64,
		ThumbX: block.DX, ThumbY: block.DY, ThumbWidth: block.Width, ThumbHeight: block.Height,
		ExpiresIn: int(s.ttl.Seconds()),
	}, nil
}

func (s *LotteryCaptchaService) VerifyAndConsume(ctx context.Context, userID int64, clientIP, challengeID string, x, y int) error {
	challengeID = strings.TrimSpace(challengeID)
	if s == nil || s.store == nil || userID <= 0 || challengeID == "" {
		return ErrLotteryCaptchaInvalid
	}
	record, err := s.store.Take(ctx, challengeID)
	if errors.Is(err, ErrLotteryCaptchaRecordNotFound) {
		return ErrLotteryCaptchaExpired
	}
	if err != nil {
		slog.Error("consume lottery slider challenge failed", "error", err)
		return ErrLotteryCaptchaUnavailable
	}
	if record.UserID != userID || record.ClientIP != normalizeLotteryCaptchaIP(clientIP) {
		return ErrLotteryCaptchaInvalid
	}
	if !slide.Validate(x, y, record.TargetX, record.TargetY, 5) {
		return ErrLotteryCaptchaInvalid
	}
	return nil
}

func normalizeLotteryCaptchaIP(clientIP string) string {
	clientIP = strings.TrimSpace(clientIP)
	if len(clientIP) > 64 {
		return clientIP[:64]
	}
	return clientIP
}
