package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	_ "github.com/i96751414/torrest/docs"
	"github.com/i96751414/torrest/settings"
	"github.com/op/go-logging"
	swaggerFiles "github.com/swaggo/files"
	"github.com/swaggo/gin-swagger"
)

var log = logging.MustGetLogger("api")

type StatusResponse struct {
	Status string `json:"status" example:"ok"`
}

type ErrorResponse struct {
	Error string `json:"error" example:"Houston, we have a problem!"`
}

func NewErrorResponse(err error) *ErrorResponse {
	return &ErrorResponse{
		Error: err.Error(),
	}
}

// @title Torrest API
// @version 1.0
// @description Torrent server with a REST API.

// @contact.name i96751414
// @contact.url https://github.com/i96751414/torrest
// @contact.email i96751414@gmail.com

// @license.name GPL3.0
// @license.url https://www.gnu.org/licenses/gpl-3.0.html

// @BasePath /

// Routes defines all the routes of the server
func Routes(settingsPath string) *gin.Engine {
	config, err := settings.Load(settingsPath)
	if err != nil {
		log.Errorf("Failed loading settings: %s", err)
	}

	gin.SetMode(gin.ReleaseMode)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.LoggerWithWriter(gin.DefaultWriter))

	r.GET("/status", status)
	r.GET("/settings/get", getSettings(config))
	r.POST("/settings/set", setSettings(settingsPath, config))

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler,
		ginSwagger.URL("/swagger/doc.json")))

	return r
}

// @Summary Status
// @Description check server status
// @ID status
// @Produce  json
// @Success 200 {object} StatusResponse
// @Router /status [get]
func status(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, &StatusResponse{
		Status: "ok",
	})
}
