package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"

	"github.com/i96751414/torrest/api"
	"github.com/i96751414/torrest/bittorrent"
	"github.com/i96751414/torrest/settings"
	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("main")

func main() {
	// Make sure we are properly multi threaded.
	runtime.GOMAXPROCS(runtime.NumCPU())

	logging.SetFormatter(logging.MustStringFormatter(
		`%{color}%{level:.4s}  %{module:-12s} - %{shortfunc:-15s}  %{color:reset}%{message}`,
	))
	logging.SetBackend(logging.NewLogBackend(os.Stdout, "", 0))

	// Parse necessary arguments
	var listenPort int
	var settingsPath string
	flag.IntVar(&listenPort, "port", 8080, "Server listen port")
	flag.StringVar(&settingsPath, "settings", "settings.json", "Settings path")
	flag.Parse()

	m := http.NewServeMux()
	s := http.Server{
		Addr:    ":" + strconv.Itoa(listenPort),
		Handler: m,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Info("Loading configs")
	config, err := settings.Load(settingsPath)
	if err != nil {
		log.Errorf("Failed loading settings: %s", err)
	}

	log.Info("Starting bittorrent service")
	service := bittorrent.NewService(config.Clone())
	defer service.Close()

	m.Handle("/", api.Routes(config, service))
	m.HandleFunc("/shutdown", shutdown(cancel))

	log.Infof("Starting torrent daemon on port %d", listenPort)
	go func() {
		if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	quit := make(chan os.Signal)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-ctx.Done():
	case <-quit:
	}

	log.Info("Shutting down daemon")
	if err := s.Shutdown(ctx); err != nil && err != context.Canceled {
		log.Errorf("Failed shutting down http server gracefully: %s", err)
	}
}

// @Summary Shutdown
// @Description shutdown server
// @ID shutdown
// @Success 200 "OK"
// @Router /shutdown [get]
func shutdown(cancel context.CancelFunc) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			cancel()
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}
