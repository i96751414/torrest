package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/i96751414/torrest/bittorrent"
)

const (
	startBufferPercent = 0.005
	endBufferSize      = 10 * 1024 * 1024 // 10MB
)

type FileInfoResponse struct {
	*bittorrent.FileInfo
	Status *bittorrent.FileStatus `json:"status,omitempty"`
}

type TorrentInfoResponse struct {
	*bittorrent.TorrentInfo
	Status *bittorrent.TorrentStatus `json:"status,omitempty"`
}

// @Summary List Torrents
// @Description list all torrents from service
// @ID list-torrents
// @Produce json
// @Param status query boolean false "get torrents status"
// @Success 200 {array} TorrentInfoResponse
// @Router /torrents [get]
func listTorrents(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		torrents := service.Torrents()
		response := make([]TorrentInfoResponse, len(torrents))
		for i, torrent := range torrents {
			response[i].TorrentInfo = torrent.GetInfo()
		}
		if ctx.DefaultQuery("status", "false") == "true" {
			for i, torrent := range torrents {
				response[i].Status = torrent.GetStatus()
			}
		}
		ctx.JSON(http.StatusOK, response)
	}
}

// @Summary Remove Torrent
// @Description remove torrent from service
// @ID remove-torrent
// @Produce json
// @Param infoHash path string true "torrent info hash"
// @Param delete query boolean false "delete files"
// @Success 200 {object} MessageResponse
// @Failure 404 {object} ErrorResponse
// @Router /torrents/{infoHash}/remove [get]
func removeTorrent(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		infoHash := ctx.Param("infoHash")
		removeFiles := ctx.DefaultQuery("delete", "true") != "false"

		if err := service.RemoveTorrent(infoHash, removeFiles); err == nil {
			ctx.JSON(http.StatusOK, NewMessageResponse("Torrent '%s' deleted", infoHash))
		} else {
			ctx.JSON(http.StatusNotFound, NewErrorResponse(err))
		}
	}
}

// @Summary Resume Torrent
// @Description resume a paused torrent
// @ID resume-torrent
// @Produce json
// @Param infoHash path string true "torrent info hash"
// @Success 200 {object} MessageResponse
// @Failure 404 {object} ErrorResponse
// @Router /torrents/{infoHash}/resume [get]
func resumeTorrent(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		onGetTorrent(ctx, service, func(torrent *bittorrent.Torrent) {
			torrent.Resume()
			ctx.JSON(http.StatusOK, NewMessageResponse("Torrent '%s' resumed", torrent.InfoHash()))
		})
	}
}

// @Summary Pause Torrent
// @Description pause torrent from service
// @ID pause-torrent
// @Produce json
// @Param infoHash path string true "torrent info hash"
// @Success 200 {object} MessageResponse
// @Failure 404 {object} ErrorResponse
// @Router /torrents/{infoHash}/pause [get]
func pauseTorrent(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		onGetTorrent(ctx, service, func(torrent *bittorrent.Torrent) {
			torrent.Pause()
			ctx.JSON(http.StatusOK, NewMessageResponse("Torrent '%s' paused", torrent.InfoHash()))
		})
	}
}

// @Summary Get Torrent Info
// @Description get torrent info
// @ID torrent-info
// @Produce json
// @Param infoHash path string true "torrent info hash"
// @Success 200 {object} bittorrent.TorrentInfo
// @Failure 404 {object} ErrorResponse
// @Router /torrents/{infoHash}/info [get]
func torrentInfo(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		onGetTorrent(ctx, service, func(torrent *bittorrent.Torrent) {
			ctx.JSON(http.StatusOK, torrent.GetInfo())
		})
	}
}

// @Summary Get Torrent Status
// @Description get torrent status
// @ID torrent-status
// @Produce json
// @Param infoHash path string true "torrent info hash"
// @Success 200 {object} bittorrent.TorrentStatus
// @Failure 404 {object} ErrorResponse
// @Router /torrents/{infoHash}/status [get]
func torrentStatus(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		onGetTorrent(ctx, service, func(torrent *bittorrent.Torrent) {
			ctx.JSON(http.StatusOK, torrent.GetStatus())
		})
	}
}

// @Summary Get Torrent Files
// @Description get a list of the torrent files and its details
// @ID torrent-files
// @Produce json
// @Param infoHash path string true "torrent info hash"
// @Param status query boolean false "get files status"
// @Success 200 {array} FileInfoResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /torrents/{infoHash}/files [get]
func torrentFiles(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		onGetTorrent(ctx, service, func(torrent *bittorrent.Torrent) {
			if files, err := torrent.Files(); err == nil {
				response := make([]FileInfoResponse, len(files))
				for i, file := range files {
					response[i].FileInfo = file.Info()
				}
				if ctx.DefaultQuery("status", "false") == "true" {
					for i, file := range files {
						response[i].Status = file.Status()
					}
				}
				ctx.JSON(http.StatusOK, response)
			} else {
				ctx.JSON(http.StatusInternalServerError, NewErrorResponse(err))
			}
		})
	}
}

// @Summary Download
// @Description download all files from torrent
// @ID download-torrent
// @Produce json
// @Param infoHash path string true "torrent info hash"
// @Success 200 {object} MessageResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /torrents/{infoHash}/download [get]
func downloadTorrent(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		onGetTorrent(ctx, service, func(torrent *bittorrent.Torrent) {
			if err := torrent.SetPriority(bittorrent.DefaultPriority); err == nil {
				ctx.JSON(http.StatusOK, NewMessageResponse("torrent '%s' is downloading", torrent.InfoHash()))
			} else {
				ctx.JSON(http.StatusInternalServerError, NewErrorResponse(err))
			}
		})
	}
}

// @Summary Stop Download
// @Description stop downloading torrent
// @ID stop-torrent
// @Produce json
// @Param infoHash path string true "torrent info hash"
// @Success 200 {object} MessageResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /torrents/{infoHash}/stop [get]
func stopTorrent(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		onGetTorrent(ctx, service, func(torrent *bittorrent.Torrent) {
			if err := torrent.SetPriority(bittorrent.DontDownloadPriority); err == nil {
				ctx.JSON(http.StatusOK, NewMessageResponse("stopped torrent '%s' download", torrent.InfoHash()))
			} else {
				ctx.JSON(http.StatusInternalServerError, NewErrorResponse(err))
			}
		})
	}
}

// Can produce 404 (StatusNotFound) http error
func onGetTorrent(ctx *gin.Context, service *bittorrent.Service, f func(*bittorrent.Torrent)) {
	infoHash := ctx.Param("infoHash")
	if torrent, err := service.GetTorrent(infoHash); err == nil {
		f(torrent)
	} else {
		ctx.JSON(http.StatusNotFound, NewErrorResponse(err))
	}
}
