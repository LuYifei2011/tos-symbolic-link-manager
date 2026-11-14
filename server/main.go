package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	tosapp "github.com/LuYifei2011/tos-app-kit/go"
	appconfig "tos-app-template"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	config := appconfig.Load()
	root := filepath.Join("/usr/local", config.ID)
	dev := flag.Bool("dev", false, "use loopback development mode without TOS login")
	listen := flag.String("listen", "127.0.0.1:18947", "development listener")
	web := flag.String("web", filepath.Join(root, "site"), "static web directory")
	prepare := flag.Bool("prepare-data", false, "initialize application data as a privileged pre-start step")
	flag.Parse()
	if *prepare {
		return tosapp.PrepareAppData(config.ID)
	}
	var listener net.Listener
	var err error
	if *dev {
		listener, err = tosapp.ListenLoopback(*listen)
	} else {
		if err := tosapp.RequireNonRoot(); err != nil {
			return err
		}
		if err := tosapp.CheckDataDirectory(filepath.Join(root, "data")); err != nil {
			return err
		}
		listener, err = tosapp.SystemdListener()
	}
	if err != nil {
		return err
	}
	app := &application{config: config, web: *web, dev: *dev, verify: verifySession(tosapp.NewPlatformClient())}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("%s %s listening on %s (development=%t)", config.ID, config.Version, listener.Addr(), *dev)
	return tosapp.Serve(ctx, tosapp.NewHTTPServer(app), listener, tosapp.ServeOptions{})
}
