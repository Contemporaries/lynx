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
	"github.com/Contemporaries/lynx/internal/logx"
	"github.com/Contemporaries/lynx/internal/mgmt"
	"github.com/Contemporaries/lynx/internal/subscribe"
	"github.com/Contemporaries/lynx/internal/upgrade"
	"github.com/Contemporaries/lynx/internal/version"
)

func main() {
	configPath := flag.String("config", "/etc/lynx/client.json", "single client JSON config")
	subscribeURL := flag.String("subscribe", "", "subscription URL; fetches and writes inline PEMs into -config")
	subscribeRefresh := flag.Int("subscribe-refresh", 0, "re-fetch subscription every N seconds (0=disabled)")
	logLevel := flag.String("log-level", "", "log level override: debug|info|warn|error")
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
		fetched.Log = cfg.Log
		fetched.Mgmt = cfg.Mgmt
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

	level := logx.ParseLevel(cfg.Log.Level)
	if *logLevel != "" {
		level = logx.ParseLevel(*logLevel)
	}
	lx := logx.New(level)
	started := time.Now()

	var rt *appclient.Runtime
	runErrCh := make(chan error, 1)
	go func() {
		_, err := appclient.RunConfig(ctx, cfg, nil, *configPath, &appclient.RunOptions{Logx: lx, Runtime: &rt})
		runErrCh <- err
	}()

	// Wait briefly for runtime to come up for status hooks.
	deadline := time.Now().Add(3 * time.Second)
	for rt == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	store := &mgmt.FileConfigStore{PathName: *configPath, Role: mgmt.RoleClient, Client: cfg, Logger: lx}
	svc := &clientService{rt: &rt, stop: stop, cfgPath: *configPath, lx: lx}
	if cfg.Mgmt.Listen != "" {
		go func() {
			err := mgmt.ListenAndServe(ctx, mgmt.Options{
				Listen:       cfg.Mgmt.Listen,
				Secret:       cfg.Mgmt.Secret,
				CORSOrigin:   cfg.Mgmt.CORSOrigin,
				AllowUpgrade: cfg.Mgmt.AllowUpgrade,
				ApplyRestart: cfg.Mgmt.ApplyRestart,
				Role:         mgmt.RoleClient,
				Unit:         "lynx-client",
				Binary:       "lynx-client",
				Logger:       lx,
				StartedAt:    started,
				Status: func() map[string]any {
					if rt == nil {
						return map[string]any{"ready": false}
					}
					return rt.Status()
				},
				Config:  store,
				Service: svc,
			})
			if err != nil && ctx.Err() == nil {
				lx.Error("mgmt server stopped", "err", err)
			}
		}()
	}

	if err := <-runErrCh; err != nil && !errors.Is(err, context.Canceled) {
		lx.Error("client stopped", "err", err)
		os.Exit(1)
	}
}

type clientService struct {
	rt     **appclient.Runtime
	stop   context.CancelFunc
	cfgPath string
	lx     *logx.Logger
}

func (c *clientService) Restart() error {
	if err := upgrade.RestartService("lynx-client"); err == nil {
		return nil
	}
	return mgmt.SelfRestart(c.stop)
}

func (c *clientService) Reload() ([]string, bool, error) {
	cfg, err := config.LoadClient(c.cfgPath)
	if err != nil {
		return nil, false, err
	}
	applied := []string{}
	if c.lx != nil && cfg.Log.Level != "" {
		c.lx.SetLevel(logx.ParseLevel(cfg.Log.Level))
		applied = append(applied, "log.level")
	}
	return applied, true, nil
}

func (c *clientService) Reconnect() error {
	if c.rt == nil || *c.rt == nil {
		return fmt.Errorf("runtime not ready")
	}
	return (*c.rt).Reconnect()
}

func (c *clientService) SubscribeRefresh() error {
	cfg, err := config.LoadClient(c.cfgPath)
	if err != nil {
		return err
	}
	if cfg.SubscribeURL == "" {
		return fmt.Errorf("subscribe_url not set")
	}
	fetched, _, err := subscribe.FetchAndApply(context.Background(), cfg.SubscribeURL, cfg.SOCKSListen, cfg.HTTPListen)
	if err != nil {
		return err
	}
	fetched.ProxyChannels = cfg.ProxyChannels
	fetched.SubscribeURL = cfg.SubscribeURL
	fetched.SubscribeRefreshSec = cfg.SubscribeRefreshSec
	fetched.Log = cfg.Log
	fetched.Mgmt = cfg.Mgmt
	fetched.Mode = cfg.Mode
	if cfg.ProxyUsername != "" {
		fetched.ProxyUsername = cfg.ProxyUsername
		fetched.ProxyPassword = cfg.ProxyPassword
	}
	return config.WriteClient(c.cfgPath, fetched)
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
