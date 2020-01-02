package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/i96751414/torrest/bittorrent"
)

type NewTorrentResponse struct {
	InfoHash string `json:"info_hash" example:"000102030405060708090a0b0c0d0e0f10111213"`
}

// @Summary Add Magnet
// @Description add magnet to service
// @ID add-magnet
// @Produce  json
// @Param uri query string true "magnet URI"
// @Success 200 {object} NewTorrentResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /add/magnet [get]
func addMagnet(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		magnet := ctx.Query("uri")
		if !strings.HasPrefix(magnet, "magnet:") {
			ctx.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid magnet provided"})
			return
		}
		if infoHash, err := service.AddMagnet(magnet); err != nil {
			ctx.JSON(http.StatusInternalServerError, NewErrorResponse(err))
		} else {
			ctx.JSON(http.StatusOK, NewTorrentResponse{InfoHash: infoHash})
		}
	}
}

// @Summary Remove Torrent
// @Description remove torrent from service
// @ID remove-torrent
// @Produce  json
// @Param infoHash path string true "torrent info hash"
// @Param delete query boolean false "delete files"
// @Success 200 {object} MessageResponse
// @Failure 404 {object} ErrorResponse
// @Router /remove/{infoHash} [get]
func removeTorrent(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		infoHash := ctx.Param("infoHash")
		removeFiles := ctx.DefaultQuery("delete", "true") == "true"

		if service.RemoveTorrent(infoHash, removeFiles) {
			ctx.JSON(http.StatusOK, MessageResponse{Message: fmt.Sprintf("Torrent '%s' deleted", infoHash)})
		} else {
			ctx.JSON(http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("No such info hash '%s'", infoHash)})
		}
	}
}
