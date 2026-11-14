package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	tosapp "github.com/LuYifei2011/tos-app-kit/go"
	appconfig "tos-symbolic-link-manager"
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
	devRoot := flag.String("root", ".local/files", "development filesystem root")
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
	var roots []string
	if *dev {
		p, e := filepath.Abs(*devRoot)
		if e != nil {
			return e
		}
		p, e = filepath.EvalSymlinks(p)
		if e != nil {
			return fmt.Errorf("development root: %w (create it first or pass --root)", e)
		}
		d, e := openDirectory(p)
		if e != nil {
			return e
		}
		d.Close()
		roots = []string{p}
	}
	app := &application{roots: roots, client: tosapp.NewPlatformClient(), config: config, web: *web, dev: *dev, verify: verifySession(tosapp.NewPlatformClient())}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("%s %s listening on %s (development=%t)", config.ID, config.Version, listener.Addr(), *dev)
	return tosapp.Serve(ctx, tosapp.NewHTTPServer(app), listener, tosapp.ServeOptions{})
}
