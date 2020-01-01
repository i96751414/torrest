package main

import (
	"context"
	"flag"
	"github.com/i96751414/torrest/settings"
	"net/http"
	"os"
	"runtime"
	"strconv"

	"github.com/i96751414/torrest/api"
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

	config, err := settings.Load(settingsPath)
	if err != nil {
		log.Errorf("Failed loading settings: %s", err)
	}

	log.Infof("Starting torrent daemon on port %d", listenPort)

	m := http.NewServeMux()
	s := http.Server{
		Addr:    ":" + strconv.Itoa(listenPort),
		Handler: m,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.Handle("/", api.Routes(config))
	m.HandleFunc("/shutdown", shutdown(cancel))

	go func() {
		if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()

	// Shutdown the server when the context is canceled
	log.Info("Shutting down daemon")
	if err := s.Shutdown(ctx); err != nil && err != context.Canceled {
		log.Errorf("Failed shutting down http server gracefully: %s", err.Error())
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
