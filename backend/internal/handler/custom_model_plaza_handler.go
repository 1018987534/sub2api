package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type CustomModelPlazaHandler struct {
	plaza *service.CustomModelPlazaService
}

func NewCustomModelPlazaHandler(plaza *service.CustomModelPlazaService) *CustomModelPlazaHandler {
	return &CustomModelPlazaHandler{plaza: plaza}
}

func (h *CustomModelPlazaHandler) Get(c *gin.Context) {
	models, err := h.plaza.List(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, models)
}

func (h *CustomModelPlazaHandler) GetAdmin(c *gin.Context) {
	models, err := h.plaza.ListAdmin(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, models)
}

func (h *CustomModelPlazaHandler) Update(c *gin.Context) {
	var req struct {
		Tiers []service.CustomPlazaTier `json:"tiers"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || req.Tiers == nil {
		response.BadRequest(c, "tiers must be a JSON array")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		response.BadRequest(c, "expected one JSON object")
		return
	}
	model, err := h.plaza.SetOverrides(c.Request.Context(), c.Param("model"), req.Tiers)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, model)
}
