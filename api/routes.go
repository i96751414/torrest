package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/i96751414/torrest/settings"
	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("api")

type errorResponse struct {
	Error string `json:"error"`
}

func newErrorResponse(err error) *errorResponse {
	return &errorResponse{
		Error: err.Error(),
	}
}

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

	return r
}

func status(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{
		"status": "ok",
	})
}
