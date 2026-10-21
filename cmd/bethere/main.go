// Command bethere runs the BeThere server and its operator subcommands.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BensonPigo/bethere/internal/server"
	"github.com/BensonPigo/bethere/web"
)

// version is set at build time: -ldflags "-X main.version=..."
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: bethere <serve|version>")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		if err := serve(os.Args[2:]); err != nil {
			slog.Error("serve failed", "err", err)
			os.Exit(1)
		}
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:8080", "listen address (Caddy proxies to it)")
	fs.Parse(args)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	srv := &http.Server{
		Addr: *addr,
		Handler: server.New(server.Config{
			Static:  web.Dist(),
			Now:     time.Now,
			Version: version,
			Logger:  logger,
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	logger.Info("listening", "addr", *addr, "version", version)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
