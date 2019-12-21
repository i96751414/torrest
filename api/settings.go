package api

import (
	"io/ioutil"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/i96751414/torrest/settings"
)

func getSettings(config *settings.Settings) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, config)
	}
}

func setSettings(settingsPath string, config *settings.Settings) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		body, err := ioutil.ReadAll(ctx.Request.Body)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, newErrorResponse(err))
			return
		}
		if err := config.Update(body); err != nil {
			ctx.JSON(http.StatusInternalServerError, newErrorResponse(err))
			return
		}
		if err := config.Save(settingsPath); err != nil {
			log.Errorf("Failed saving settings: %s", err)
		}
		ctx.Status(http.StatusOK)
	}
}
