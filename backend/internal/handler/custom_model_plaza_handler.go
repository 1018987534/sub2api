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
	c.Header("Cache-Control", "no-store")
	if id, ok := c.GetQuery("model"); ok {
		model, err := h.plaza.GetAdminModel(c.Request.Context(), id)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		response.Success(c, model)
		return
	}
	models, err := h.plaza.ListAdmin(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, models)
}

func decodePlazaRequest(c *gin.Context, dst any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil || decoder.Decode(new(any)) != io.EOF {
		response.BadRequest(c, "expected one valid model configuration object")
		return false
	}
	return true
}

func (h *CustomModelPlazaHandler) PreviewDefaults(c *gin.Context) {
	model, err := h.plaza.PreviewDefaults(c.Request.Context(), c.Query("model"), c.Query("family"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, model)
}

func (h *CustomModelPlazaHandler) Create(c *gin.Context) { h.configure(c, true) }
func (h *CustomModelPlazaHandler) Edit(c *gin.Context)   { h.configure(c, false) }

func (h *CustomModelPlazaHandler) configure(c *gin.Context, create bool) {
	var req service.CustomPlazaConfig
	if !decodePlazaRequest(c, &req) {
		return
	}
	if req.Tiers == nil {
		response.BadRequest(c, "tiers must be a JSON array")
		return
	}
	model, err := h.plaza.Configure(c.Request.Context(), req, create)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if create {
		response.Created(c, model)
		return
	}
	response.Success(c, model)
}

func (h *CustomModelPlazaHandler) Delete(c *gin.Context) {
	if err := h.plaza.DeleteModel(c.Request.Context(), c.Query("model")); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, nil)
}

func (h *CustomModelPlazaHandler) PatchPrices(c *gin.Context) {
	var req service.CustomPlazaPricePatch
	if !decodePlazaRequest(c, &req) {
		return
	}
	model, err := h.plaza.PatchPrices(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, model)
}

// Update retains the price-only endpoint for older frontend bundles.
func (h *CustomModelPlazaHandler) Update(c *gin.Context) {
	var req struct {
		Tiers []service.CustomPlazaTier `json:"tiers"`
	}
	if !decodePlazaRequest(c, &req) {
		return
	}
	if req.Tiers == nil {
		response.BadRequest(c, "tiers must be a JSON array")
		return
	}
	model, err := h.plaza.SetOverrides(c.Request.Context(), c.Param("model"), req.Tiers)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, model)
}
