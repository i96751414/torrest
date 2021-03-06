package api

import (
	"github.com/i96751414/torrest/bittorrent"
	"io/ioutil"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/i96751414/torrest/settings"
)

// @Summary Get current settings
// @Description get settings in JSON object
// @ID get-settings
// @Produce json
// @Success 200 {object} settings.Settings
// @Router /settings/get [get]
func getSettings(config *settings.Settings) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, config)
	}
}

// @Summary Set settings
// @Description set settings given the provided JSON object
// @ID set-settings
// @Accept json
// @Produce json
// @Param default body settings.Settings false "Settings to be set"
// @Param reset query boolean false "reset torrents"
// @Success 200 {object} settings.Settings
// @Failure 500 {object} ErrorResponse
// @Router /settings/set [post]
func setSettings(config *settings.Settings, service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		body, err := ioutil.ReadAll(ctx.Request.Body)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, NewErrorResponse(err))
			return
		}

		newConfig := config.Clone()
		if err := newConfig.Update(body); err != nil {
			ctx.JSON(http.StatusInternalServerError, NewErrorResponse(err))
			return
		}

		setLogLevel(newConfig)
		reset := ctx.DefaultQuery("reset", "false") == "true"
		service.Reconfigure(newConfig, reset)

		if err := newConfig.Save(); err != nil {
			log.Errorf("Failed saving settings: %s", err)
		}
		if err := config.UpdateFrom(newConfig); err != nil {
			log.Errorf("Failed updating global settings: %s", err)
		}

		ctx.JSON(http.StatusOK, newConfig)
	}
}
