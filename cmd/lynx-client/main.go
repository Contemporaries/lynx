package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Contemporaries/lynx/internal/appclient"
	"github.com/Contemporaries/lynx/internal/config"
	"github.com/Contemporaries/lynx/internal/subscribe"
	"github.com/Contemporaries/lynx/internal/version"
)

func main() {
	configPath := flag.String("config", "/etc/lynx/client.json", "single client JSON config")
	subscribeURL := flag.String("subscribe", "", "subscription URL; fetches and writes inline PEMs into -config")
	subscribeRefresh := flag.Int("subscribe-refresh", 0, "re-fetch subscription every N seconds (0=disabled)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.Version)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var cfg *config.Client
	var err error
	sub := *subscribeURL

	if sub == "" {
		cfg, err = config.LoadClient(*configPath)
		if err != nil {
			log.Fatal(err)
		}
		sub = cfg.SubscribeURL
	} else {
		// Minimal shell so listen defaults apply when only -subscribe is given.
		cfg = &config.Client{SubscribeURL: sub}
		cfg, err = config.NormalizeClient(cfg)
		if err != nil {
			log.Fatal(err)
		}
	}

	refresh := *subscribeRefresh
	if refresh <= 0 && cfg != nil {
		refresh = cfg.SubscribeRefreshSec
	}

	if sub != "" {
		socks, httpListen := cfg.SOCKSListen, cfg.HTTPListen
		channels := cfg.ProxyChannels
		fetched, profile, ferr := subscribe.FetchAndApply(ctx, sub, socks, httpListen)
		if ferr != nil {
			log.Fatal(ferr)
		}
		fetched.ProxyChannels = channels
		fetched.SubscribeURL = sub
		fetched.SubscribeRefreshSec = refresh
		// Preserve local listen / auth from existing file when present.
		if cfg.SOCKSListen != "" {
			fetched.SOCKSListen = cfg.SOCKSListen
		}
		if cfg.HTTPListen != "" {
			fetched.HTTPListen = cfg.HTTPListen
		}
		if cfg.ProxyUsername != "" {
			fetched.ProxyUsername = cfg.ProxyUsername
			fetched.ProxyPassword = cfg.ProxyPassword
		}
		if cfg.DirectAddr != "" {
			fetched.DirectAddr = cfg.DirectAddr
			fetched.DirectServerName = cfg.DirectServerName
			if cfg.Mode != "" {
				fetched.Mode = cfg.Mode
			}
		}
		cfg = fetched
		if err := config.WriteClient(*configPath, cfg); err != nil {
			log.Printf("warn: could not persist %s: %v", *configPath, err)
		} else {
			log.Printf("wrote single-file config %s", *configPath)
		}
		log.Printf("subscribe ok device=%s ws=%s", profile.Device, profile.WSURL)
		if refresh > 0 {
			go refreshLoop(ctx, *configPath, sub, cfg.SOCKSListen, cfg.HTTPListen, cfg.ProxyChannels, time.Duration(refresh)*time.Second)
		}
	}

	if !cfg.HasInlineCredentials() {
		log.Fatal("config has no inline credentials; set subscribe_url or certificate/key/certificate_authority")
	}

	if err := appclient.RunConfig(ctx, cfg, log.Default()); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}

func refreshLoop(ctx context.Context, configPath, sub, socks, httpListen string, channels int, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cfg, _, err := subscribe.FetchAndApply(ctx, sub, socks, httpListen)
			if err != nil {
				log.Printf("subscribe refresh: %v", err)
				continue
			}
			cfg.ProxyChannels = channels
			cfg.SubscribeURL = sub
			if err := config.WriteClient(configPath, cfg); err != nil {
				log.Printf("subscribe refresh write: %v", err)
				continue
			}
			log.Printf("subscribe refreshed into %s (restart client to apply cert rotation)", configPath)
		}
	}
}
