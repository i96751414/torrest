package api

import (
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/i96751414/torrest/bittorrent"
)

type NewTorrentResponse struct {
	InfoHash string `json:"info_hash" example:"000102030405060708090a0b0c0d0e0f10111213"`
}

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

// @Summary Pause
// @Description pause service
// @ID pause
// @Produce json
// @Success 200 {object} MessageResponse
// @Router /pause [get]
func pause(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		service.Pause()
		ctx.JSON(http.StatusOK, NewMessageResponse("service paused"))
	}
}

// @Summary Resume
// @Description resume service
// @ID resume
// @Produce json
// @Success 200 {object} MessageResponse
// @Router /resume [get]
func resume(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		service.Resume()
		ctx.JSON(http.StatusOK, NewMessageResponse("service resumed"))
	}
}

// @Summary Add Magnet
// @Description add magnet to service
// @ID add-magnet
// @Produce json
// @Param uri query string true "magnet URI"
// @Param ignore_duplicate query boolean false "ignore if duplicate"
// @Param download query boolean false "start downloading"
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
		download := ctx.DefaultQuery("download", "false") == "true"
		if infoHash, err := service.AddMagnet(magnet, download); err == nil ||
			(err == bittorrent.DuplicateTorrentError &&
				ctx.DefaultQuery("ignore_duplicate", "false") == "true") {
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
// @Param ignore_duplicate query boolean false "ignore if duplicate"
// @Param download query boolean false "start downloading"
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
					download := ctx.DefaultQuery("download", "false") == "true"
					if infoHash, err = service.AddTorrentData(data, download); err == nil ||
						(err == bittorrent.DuplicateTorrentError &&
							ctx.DefaultQuery("ignore_duplicate", "false") == "true") {
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
