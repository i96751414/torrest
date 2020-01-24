package api

import (
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/i96751414/torrest/bittorrent"
)

// @Summary Status
// @Description get service status
// @ID status
// @Produce json
// @Success 200 {object} bittorrent.ServiceStatus
// @Router /status [get]
func status(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, service.GetStatus())
	}
}

// @Summary Add Magnet
// @Description add magnet to service
// @ID add-magnet
// @Produce json
// @Param uri query string true "magnet URI"
// @Success 200 {object} NewTorrentResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /add/magnet [get]
func addMagnet(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		magnet := ctx.Query("uri")
		if !strings.HasPrefix(magnet, "magnet:") {
			ctx.JSON(http.StatusBadRequest, NewErrorResponse("Invalid magnet provided"))
			return
		}
		if infoHash, err := service.AddMagnet(magnet); err == nil {
			ctx.JSON(http.StatusOK, NewTorrentResponse{InfoHash: infoHash})
		} else {
			ctx.JSON(http.StatusInternalServerError, NewErrorResponse(err))
		}
	}
}

// @Summary Add Torrent File
// @Description add torrent file to service
// @ID add-torrent
// @Accept multipart/form-data
// @Produce json
// @Param torrent formData file true "torrent file"
// @Success 200 {object} NewTorrentResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /add/torrent [post]
func addTorrent(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if f, err := ctx.FormFile("torrent"); err == nil {
			var err error
			var infoHash string
			var file multipart.File

			if file, err = f.Open(); err == nil {
				data := make([]byte, f.Size)
				if _, err = file.Read(data); err == nil {
					if infoHash, err = service.AddTorrentData(data); err == nil {
						ctx.JSON(http.StatusOK, NewTorrentResponse{InfoHash: infoHash})
						return
					}
				}
			}

			ctx.JSON(http.StatusInternalServerError, NewErrorResponse(err))
		} else {
			ctx.JSON(http.StatusBadRequest, NewErrorResponse(err))
		}
	}
}
