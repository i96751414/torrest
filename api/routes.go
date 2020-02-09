package api

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/i96751414/torrest/bittorrent"
	_ "github.com/i96751414/torrest/docs"
	"github.com/i96751414/torrest/settings"
	"github.com/op/go-logging"
	swaggerFiles "github.com/swaggo/files"
	"github.com/swaggo/gin-swagger"
)

var log = logging.MustGetLogger("api")

type ErrorResponse struct {
	Error string `json:"error" example:"Houston, we have a problem!"`
}

type MessageResponse struct {
	Message string `json:"message" example:"done"`
}

func NewErrorResponse(err interface{}) *ErrorResponse {
	r := ErrorResponse{}
	switch err.(type) {
	case string:
		r.Error = err.(string)
	case error:
		r.Error = err.(error).Error()
	default:
		panic("expecting either string or error")
	}
	return &r
}

func NewMessageResponse(format string, a ...interface{}) *MessageResponse {
	return &MessageResponse{Message: fmt.Sprintf(format, a...)}
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
func Routes(config *settings.Settings, service *bittorrent.Service) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.LoggerWithWriter(gin.DefaultWriter))
	r.Use(CORSMiddleware())

	r.GET("/status", status(service))
	r.GET("/pause", pause(service))
	r.GET("/resume", resume(service))

	addRoute := r.Group("/add")
	addRoute.GET("/magnet", addMagnet(service))
	addRoute.POST("/torrent", addTorrent(service))

	settingsRoutes := r.Group("/settings")
	settingsRoutes.GET("/get", getSettings(config))
	settingsRoutes.POST("/set", setSettings(config, service))

	torrentsRoutes := r.Group("/torrents")
	torrentsRoutes.GET("/", listTorrents(service))
	torrentsRoutes.GET("/:infoHash/pause", pauseTorrent(service))
	torrentsRoutes.GET("/:infoHash/resume", resumeTorrent(service))
	torrentsRoutes.GET("/:infoHash/remove", removeTorrent(service))
	torrentsRoutes.GET("/:infoHash/status", torrentStatus(service))
	torrentsRoutes.GET("/:infoHash/files", torrentFiles(service))
	torrentsRoutes.GET("/:infoHash/download", downloadTorrent(service))
	torrentsRoutes.GET("/:infoHash/files/:file/download", downloadFile(config, service))
	torrentsRoutes.GET("/:infoHash/files/:file/stop", stopFile(service))
	torrentsRoutes.GET("/:infoHash/files/:file/info", fileInfo(service))
	torrentsRoutes.GET("/:infoHash/files/:file/status", fileStatus(service))
	torrentsRoutes.GET("/:infoHash/files/:file/hash", fileHash(service))
	torrentsRoutes.Any("/:infoHash/files/:file/serve", serveFile(service))

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler,
		ginSwagger.URL("/swagger/doc.json")))

	return r
}

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Next()
	}
}
