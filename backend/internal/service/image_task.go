package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	ImageTaskStatusProcessing = "processing"
	ImageTaskStatusCompleted  = "completed"
	ImageTaskStatusFailed     = "failed"

	// Task metadata is short-lived; generated image bytes never enter the store.
	defaultImageTaskTTL              = time.Hour
	defaultImageTaskExecutionTimeout = 30 * time.Minute
)

var (
	ErrImageTaskNotFound    = infraerrors.New(http.StatusNotFound, "IMAGE_TASK_NOT_FOUND", "image task not found")
	ErrImageTaskForbidden   = infraerrors.New(http.StatusForbidden, "IMAGE_TASK_FORBIDDEN", "image task does not belong to this API key")
	ErrImageTaskProcessing  = infraerrors.New(http.StatusConflict, "IMAGE_TASK_PROCESSING", "processing image tasks cannot be deleted")
	ErrImageTaskUnavailable = infraerrors.New(http.StatusServiceUnavailable, "IMAGE_TASK_UNAVAILABLE", "image task storage is unavailable")
)

type ImageTaskMetadata struct {
	Mode         string
	Prompt       string
	Size         string
	Quality      string
	OutputFormat string
	N            int
}

// ImageTaskRecord is the private Redis representation of an asynchronous image
// request. Ownership fields are intentionally omitted from the public view.
type ImageTaskRecord struct {
	ID           string          `json:"id"`
	UserID       int64           `json:"user_id"`
	APIKeyID     int64           `json:"api_key_id"`
	Mode         string          `json:"mode,omitempty"`
	Prompt       string          `json:"prompt,omitempty"`
	Size         string          `json:"size,omitempty"`
	Quality      string          `json:"quality,omitempty"`
	OutputFormat string          `json:"output_format,omitempty"`
	N            int             `json:"n,omitempty"`
	Status       string          `json:"status"`
	HTTPStatus   int             `json:"http_status,omitempty"`
	Result       json.RawMessage `json:"result,omitempty"`
	Error        json.RawMessage `json:"error,omitempty"`
	CreatedAt    int64           `json:"created_at"`
	CompletedAt  *int64          `json:"completed_at,omitempty"`
	ExpiresAt    int64           `json:"expires_at"`
}

// ImageTask is the API-safe task representation returned to callers.
type ImageTask struct {
	ID           string          `json:"id"`
	TaskID       string          `json:"task_id"`
	Object       string          `json:"object"`
	Mode         string          `json:"mode,omitempty"`
	Prompt       string          `json:"prompt,omitempty"`
	Size         string          `json:"size,omitempty"`
	Quality      string          `json:"quality,omitempty"`
	OutputFormat string          `json:"output_format,omitempty"`
	N            int             `json:"n,omitempty"`
	Status       string          `json:"status"`
	HTTPStatus   int             `json:"http_status,omitempty"`
	ImageURL     string          `json:"image_url,omitempty"`
	Result       json.RawMessage `json:"result,omitempty"`
	Error        json.RawMessage `json:"error,omitempty"`
	CreatedAt    int64           `json:"created_at"`
	CompletedAt  *int64          `json:"completed_at,omitempty"`
	ExpiresAt    int64           `json:"expires_at"`
}

type ImageTaskOwner struct {
	UserID   int64
	APIKeyID int64
}

type ImageTaskStore interface {
	Save(ctx context.Context, task *ImageTaskRecord, ttl time.Duration) error
	Get(ctx context.Context, id string) (*ImageTaskRecord, error)
	List(ctx context.Context, owner ImageTaskOwner, limit int) ([]*ImageTaskRecord, error)
	Delete(ctx context.Context, task *ImageTaskRecord) error
}

// ImageStorageResolver reports the currently effective object-storage binding.
// It exists so the async image feature can be switched on and off from the admin
// UI without a restart: the wiring below is fixed at startup, but the answer to
// "is object storage configured right now" is re-read (and cached) per call.
type ImageStorageResolver func() (uploader *ImageResultUploader, enabled bool)

type ImageTaskService struct {
	store            ImageTaskStore
	uploader         *ImageResultUploader
	enabled          bool
	resolve          ImageStorageResolver
	ephemeral        bool
	ephemeralMu      sync.Mutex
	ephemeralResults map[string]json.RawMessage
	ttl              time.Duration
	executionTimeout time.Duration
}

func NewImageTaskService(store ImageTaskStore) *ImageTaskService {
	return NewImageTaskServiceWithOptions(store, defaultImageTaskTTL, defaultImageTaskExecutionTimeout)
}

func NewImageTaskServiceWithOptions(store ImageTaskStore, ttl, executionTimeout time.Duration) *ImageTaskService {
	if ttl <= 0 {
		ttl = defaultImageTaskTTL
	}
	if executionTimeout <= 0 {
		executionTimeout = defaultImageTaskExecutionTimeout
	}
	return &ImageTaskService{store: store, ttl: ttl, executionTimeout: executionTimeout}
}

// NewImageTaskServiceBrowserMemory stores generated image results only in the
// process memory and consumes each result on its first successful poll. Redis
// keeps task status and metadata so the browser can continue polling, but never
// receives image bytes.
func NewImageTaskServiceBrowserMemory(store ImageTaskStore, ttl, executionTimeout time.Duration) *ImageTaskService {
	s := NewImageTaskServiceWithOptions(store, ttl, executionTimeout)
	s.ephemeral = true
	s.ephemeralResults = make(map[string]json.RawMessage)
	return s
}

// NewImageTaskServiceWithUploader 构造一个已启用的图片任务服务：结果会先经 uploader
// 转存到对象存储再落 Redis。uploader 为 nil 时不做转存（仅用于测试）。
func NewImageTaskServiceWithUploader(store ImageTaskStore, uploader *ImageResultUploader, ttl, executionTimeout time.Duration) *ImageTaskService {
	s := NewImageTaskServiceWithOptions(store, ttl, executionTimeout)
	s.uploader = uploader
	s.enabled = true
	return s
}

// NewImageTaskServiceWithResolver 构造一个由 resolver 决定启用状态的服务：
// 开关与凭证来自后台设置，保存后立即生效，无需重启。
func NewImageTaskServiceWithResolver(store ImageTaskStore, resolve ImageStorageResolver, ttl, executionTimeout time.Duration) *ImageTaskService {
	s := NewImageTaskServiceWithOptions(store, ttl, executionTimeout)
	s.resolve = resolve
	return s
}

// current 返回当前生效的 uploader 与启用状态。
// 注入了 resolver 时以 resolver 为准（后台设置可热切换），否则回落到构造时固定的值。
func (s *ImageTaskService) current() (*ImageResultUploader, bool) {
	if s == nil {
		return nil, false
	}
	if s.resolve != nil {
		return s.resolve()
	}
	return s.uploader, s.enabled
}

// Enabled 表示异步图片任务功能是否可用（总开关 + 凭证齐全）。
// 关闭时 handler 直接返回 404，不创建任务、不写 Redis。
func (s *ImageTaskService) Enabled() bool {
	if s == nil || s.store == nil {
		return false
	}
	if s.ephemeral {
		return true
	}
	_, enabled := s.current()
	return enabled
}

// Pollable 表示已创建的任务能否被查询。
// 比 Enabled 弱：只要 store 可用即可，从而在功能被关掉后仍能取回进行中的任务结果。
func (s *ImageTaskService) Pollable() bool {
	return s != nil && s.store != nil
}

func (s *ImageTaskService) ExecutionTimeout() time.Duration {
	if s == nil || s.executionTimeout <= 0 {
		return defaultImageTaskExecutionTimeout
	}
	return s.executionTimeout
}

func (s *ImageTaskService) Create(ctx context.Context, owner ImageTaskOwner, metadata ...ImageTaskMetadata) (*ImageTask, error) {
	if s == nil || s.store == nil {
		return nil, ErrImageTaskUnavailable
	}
	meta := normalizeImageTaskMetadata(metadata)
	now := time.Now().UTC()
	task := &ImageTaskRecord{
		ID:           "imgtask_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		UserID:       owner.UserID,
		APIKeyID:     owner.APIKeyID,
		Mode:         meta.Mode,
		Prompt:       meta.Prompt,
		Size:         meta.Size,
		Quality:      meta.Quality,
		OutputFormat: meta.OutputFormat,
		N:            meta.N,
		Status:       ImageTaskStatusProcessing,
		CreatedAt:    now.Unix(),
		ExpiresAt:    now.Add(s.ttl).Unix(),
	}
	if err := s.store.Save(ctx, task, s.ttl); err != nil {
		return nil, ErrImageTaskUnavailable.WithCause(err)
	}
	return imageTaskToPublic(task), nil
}

func normalizeImageTaskMetadata(values []ImageTaskMetadata) ImageTaskMetadata {
	meta := ImageTaskMetadata{}
	if len(values) > 0 {
		meta = values[0]
	}
	meta.Mode = strings.ToLower(strings.TrimSpace(meta.Mode))
	if meta.Mode != "edit" {
		meta.Mode = "generate"
	}
	meta.Prompt = strings.TrimSpace(meta.Prompt)
	meta.Size = strings.TrimSpace(meta.Size)
	if meta.Size == "" {
		meta.Size = "1024x1024"
	}
	meta.Quality = strings.ToLower(strings.TrimSpace(meta.Quality))
	if meta.Quality == "" {
		meta.Quality = "auto"
	}
	meta.OutputFormat = strings.ToLower(strings.TrimSpace(meta.OutputFormat))
	if meta.OutputFormat == "" {
		meta.OutputFormat = "png"
	}
	if meta.N <= 0 {
		meta.N = 1
	}
	return meta
}

func (s *ImageTaskService) Get(ctx context.Context, owner ImageTaskOwner, id string) (*ImageTask, error) {
	if s == nil || s.store == nil {
		return nil, ErrImageTaskUnavailable
	}
	task, err := s.store.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, ErrImageTaskNotFound) {
			return nil, ErrImageTaskNotFound
		}
		return nil, ErrImageTaskUnavailable.WithCause(err)
	}
	if task.UserID != owner.UserID || task.APIKeyID != owner.APIKeyID {
		// Do not reveal whether a random task ID exists for another caller.
		return nil, ErrImageTaskNotFound
	}
	public := imageTaskToPublic(task)
	if s.ephemeral && task.Status == ImageTaskStatusCompleted {
		s.ephemeralMu.Lock()
		result := append(json.RawMessage(nil), s.ephemeralResults[task.ID]...)
		delete(s.ephemeralResults, task.ID)
		s.ephemeralMu.Unlock()
		public.Result = result
		public.ImageURL = firstImageTaskURL(result)
	}
	return public, nil
}

func (s *ImageTaskService) List(ctx context.Context, owner ImageTaskOwner, limit int) ([]*ImageTask, error) {
	if s == nil || s.store == nil {
		return nil, ErrImageTaskUnavailable
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	records, err := s.store.List(ctx, owner, limit)
	if err != nil {
		return nil, ErrImageTaskUnavailable.WithCause(err)
	}
	tasks := make([]*ImageTask, 0, len(records))
	for _, record := range records {
		if record == nil || record.UserID != owner.UserID || record.APIKeyID != owner.APIKeyID {
			continue
		}
		public := imageTaskToPublic(record)
		if s.ephemeral {
			public.Result = nil
			public.ImageURL = ""
		}
		tasks = append(tasks, public)
	}
	return tasks, nil
}

func (s *ImageTaskService) Delete(ctx context.Context, owner ImageTaskOwner, id string) error {
	if s == nil || s.store == nil {
		return ErrImageTaskUnavailable
	}
	record, err := s.store.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, ErrImageTaskNotFound) {
			return ErrImageTaskNotFound
		}
		return ErrImageTaskUnavailable.WithCause(err)
	}
	if record.UserID != owner.UserID || record.APIKeyID != owner.APIKeyID {
		return ErrImageTaskNotFound
	}
	if record.Status == ImageTaskStatusProcessing {
		return ErrImageTaskProcessing
	}
	if err := s.store.Delete(ctx, record); err != nil {
		return ErrImageTaskUnavailable.WithCause(err)
	}
	if s.ephemeral {
		s.ephemeralMu.Lock()
		delete(s.ephemeralResults, record.ID)
		s.ephemeralMu.Unlock()
	}
	return nil
}

func (s *ImageTaskService) Complete(ctx context.Context, id string, statusCode int, result json.RawMessage) error {
	if !json.Valid(result) {
		return s.Fail(ctx, id, http.StatusBadGateway, imageTaskErrorJSON("api_error", "upstream returned a non-JSON image response"))
	}
	if !s.ephemeral {
		if uploader, _ := s.current(); uploader != nil {
			rewritten, err := uploader.Rewrite(ctx, id, result)
			if err != nil {
				// 转存失败不回退存 base64，避免大 blob 撑爆 Redis：直接把任务标记为失败。
				logger.L().Error("image_task.offload_failed", zap.String("task_id", id), zap.Error(err))
				return s.Fail(ctx, id, http.StatusBadGateway, imageTaskErrorJSON("api_error", "failed to store generated image to object storage"))
			}
			result = rewritten
		}
	}
	return s.finish(ctx, id, ImageTaskStatusCompleted, statusCode, result, nil)
}

func (s *ImageTaskService) Fail(ctx context.Context, id string, statusCode int, taskErr json.RawMessage) error {
	if !json.Valid(taskErr) {
		taskErr = imageTaskErrorJSON("api_error", "image generation failed")
	}
	return s.finish(ctx, id, ImageTaskStatusFailed, statusCode, nil, taskErr)
}

func (s *ImageTaskService) finish(ctx context.Context, id, status string, statusCode int, result, taskErr json.RawMessage) error {
	if s == nil || s.store == nil {
		return ErrImageTaskUnavailable
	}
	task, err := s.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, ErrImageTaskNotFound) {
			return ErrImageTaskNotFound
		}
		return ErrImageTaskUnavailable.WithCause(err)
	}
	now := time.Now().UTC()
	completedAt := now.Unix()
	task.Status = status
	task.HTTPStatus = statusCode
	if s.ephemeral && status == ImageTaskStatusCompleted {
		task.Result = nil
	} else {
		task.Result = result
	}
	task.Error = taskErr
	task.CompletedAt = &completedAt
	task.ExpiresAt = now.Add(s.ttl).Unix()
	if s.ephemeral && status == ImageTaskStatusCompleted {
		s.ephemeralMu.Lock()
		s.ephemeralResults[task.ID] = append(json.RawMessage(nil), result...)
		s.ephemeralMu.Unlock()
	}
	if err := s.store.Save(ctx, task, s.ttl); err != nil {
		if s.ephemeral && status == ImageTaskStatusCompleted {
			s.ephemeralMu.Lock()
			delete(s.ephemeralResults, task.ID)
			s.ephemeralMu.Unlock()
		}
		return ErrImageTaskUnavailable.WithCause(err)
	}
	if s.ephemeral && status == ImageTaskStatusCompleted {
		time.AfterFunc(s.ttl, func() {
			s.ephemeralMu.Lock()
			delete(s.ephemeralResults, task.ID)
			s.ephemeralMu.Unlock()
		})
	}
	return nil
}

func imageTaskToPublic(task *ImageTaskRecord) *ImageTask {
	if task == nil {
		return nil
	}
	return &ImageTask{
		ID:           task.ID,
		TaskID:       task.ID,
		Object:       "image.generation.task",
		Mode:         task.Mode,
		Prompt:       task.Prompt,
		Size:         task.Size,
		Quality:      task.Quality,
		OutputFormat: task.OutputFormat,
		N:            task.N,
		Status:       task.Status,
		HTTPStatus:   task.HTTPStatus,
		ImageURL:     firstImageTaskURL(task.Result),
		Result:       task.Result,
		Error:        task.Error,
		CreatedAt:    task.CreatedAt,
		CompletedAt:  task.CompletedAt,
		ExpiresAt:    task.ExpiresAt,
	}
}

func firstImageTaskURL(result json.RawMessage) string {
	if len(result) == 0 || !json.Valid(result) {
		return ""
	}
	var response struct {
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if json.Unmarshal(result, &response) != nil || len(response.Data) == 0 {
		return ""
	}
	return strings.TrimSpace(response.Data[0].URL)
}

func imageTaskErrorJSON(errorType, message string) json.RawMessage {
	data, _ := json.Marshal(map[string]string{"type": errorType, "message": message})
	return data
}
