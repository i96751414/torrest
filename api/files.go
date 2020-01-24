package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/i96751414/torrest/bittorrent"
	"github.com/i96751414/torrest/settings"
)

// @Summary Download File
// @Description download file from torrent given its id
// @ID download-file
// @Produce json
// @Param infoHash path string true "torrent info hash"
// @Param file path integer true "file id"
// @Param buffer query boolean false "buffer file"
// @Success 200 {object} MessageResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /torrents/{infoHash}/files/{file}/download [get]
func downloadFile(config *settings.Settings, service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		onGetFile(ctx, service, func(file *bittorrent.File) {
			file.SetPriority(bittorrent.DefaultPriority)
			if ctx.DefaultQuery("buffer", "false") == "true" {
				bufferSize := int64(float64(file.Length()) * startBufferPercent)
				if bufferSize < config.BufferSize {
					bufferSize = config.BufferSize
				}
				file.Buffer(bufferSize, endBufferSize)
			}
			ctx.JSON(http.StatusOK, NewMessageResponse("file '%d' is downloading", file.Id()))
		})
	}
}

// @Summary Stop File Download
// @Description stop file download from torrent given its id
// @ID stop-file
// @Produce json
// @Param infoHash path string true "torrent info hash"
// @Param file path integer true "file id"
// @Success 200 {object} MessageResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /torrents/{infoHash}/files/{file}/stop [get]
func stopFile(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		onGetFile(ctx, service, func(file *bittorrent.File) {
			file.SetPriority(bittorrent.DontDownloadPriority)
			ctx.JSON(http.StatusOK, NewMessageResponse("stopped file '%d' download", file.Id()))
		})
	}
}

// @Summary Get File Info
// @Description get file info from torrent given its id
// @ID file-info
// @Produce json
// @Param infoHash path string true "torrent info hash"
// @Param file path integer true "file id"
// @Success 200 {object} bittorrent.FileInfo
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /torrents/{infoHash}/files/{file}/info [get]
func fileInfo(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		onGetFile(ctx, service, func(file *bittorrent.File) {
			ctx.JSON(http.StatusOK, file.Info())
		})
	}
}

// @Summary Get File Status
// @Description get file status from torrent given its id
// @ID file-status
// @Produce json
// @Param infoHash path string true "torrent info hash"
// @Param file path integer true "file id"
// @Success 200 {object} bittorrent.FileStatus
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /torrents/{infoHash}/files/{file}/status [get]
func fileStatus(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		onGetFile(ctx, service, func(file *bittorrent.File) {
			ctx.JSON(http.StatusOK, file.Status())
		})
	}
}

// @Summary Serve File
// @Description serve file from torrent given its id
// @ID serve-file
// @Produce json
// @Param infoHash path string true "torrent info hash"
// @Param file path integer true "file id"
// @Success 200
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /torrents/{infoHash}/files/{file}/serve [get]
func serveFile(service *bittorrent.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		onGetFile(ctx, service, func(file *bittorrent.File) {
			if reader, err := file.NewReader(); err == nil {
				reader.RegisterCloseNotifier(ctx.Writer.CloseNotify())
				http.ServeContent(ctx.Writer, ctx.Request, file.Name(), time.Time{}, reader)
				if err := reader.Close(); err != nil {
					log.Errorf("Error closing file reader: %s\n", err)
				}
			} else {
				ctx.JSON(http.StatusInternalServerError, NewErrorResponse(err))
			}
		})
	}
}

func onGetFile(ctx *gin.Context, service *bittorrent.Service, f func(*bittorrent.File)) {
	fileString := ctx.Param("file")
	if fileId, err := strconv.Atoi(fileString); err == nil {
		onGetTorrent(ctx, service, func(torrent *bittorrent.Torrent) {
			if torrent.HasMetadata() {
				if file, err := torrent.GetFile(fileId); err == nil {
					f(file)
				} else {
					ctx.JSON(http.StatusBadRequest, NewErrorResponse(err))
				}
			} else {
				ctx.JSON(http.StatusInternalServerError, NewErrorResponse("no metadata"))
			}
		})
	} else {
		ctx.JSON(http.StatusBadRequest, NewErrorResponse("'file' must be integer"))
	}
}
