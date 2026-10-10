package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/yude/tunwatch/internal/config"
	"github.com/yude/tunwatch/internal/model"
	"github.com/yude/tunwatch/internal/monitor"
	"github.com/yude/tunwatch/internal/notify"
	"github.com/yude/tunwatch/internal/server"
	"github.com/yude/tunwatch/internal/store"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("tunwatch stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("config", "config.json", "JSON configuration file")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return nil
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	vendors, err := monitor.LoadVendors(c.VendorFile)
	if err != nil {
		return err
	}
	journal, err := store.Open(c.DataDir, c.RetentionDays, c.HistoryMaxMB)
	if err != nil {
		return err
	}
	defer journal.Close()
	restored, err := journal.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var captures []*monitor.Capture
	defer func() {
		cancel()
		wg.Wait()
		for _, cap := range captures {
			cap.Close()
		}
	}()
	errorsCh := make(chan error, len(c.Interfaces)+4)
	engine := monitor.NewEngine(c, journal, vendors, restored, time.Now().UTC())
	var worker *notify.Worker
	if c.Webhook != "" && !c.Demo {
		provider, e := notify.NewDiscord(c.Webhook)
		if e != nil {
			return e
		}
		worker = notify.NewWorker(provider)
		engine.Notify = worker.Enqueue
		wg.Add(1)
		go func() { defer wg.Done(); worker.Run(ctx) }()
	}
	observations := make(chan model.Observation, 2048)
	if !c.Demo {
		for _, name := range c.Interfaces {
			cap, e := monitor.OpenCapture(name, c)
			if e != nil {
				return e
			}
			captures = append(captures, cap)
		}
		totalTargets := 0
		for _, cap := range captures {
			totalTargets += cap.TargetCount()
		}
		if c.ActiveARP && totalTargets == 0 {
			return errors.New("active_arp networks do not match any connected interface")
		}
	}
	if !c.Demo {
		if err := monitor.DropCapabilities(); err != nil {
			return err
		}
	}
	api := server.New(engine, journal, c)
	api.Stats = func() any {
		items := []map[string]any{}
		for _, cap := range captures {
			items = append(items, map[string]any{"interface": cap.Name(), "observations": cap.Received.Load(), "queue_dropped": cap.Dropped.Load(), "kernel_dropped": cap.KernelDropped.Load(), "probe_targets": cap.TargetCount(), "arp_sent": cap.ARPSent.Load(), "mdns_sent": cap.MDNSSent.Load()})
		}
		stats := map[string]any{"interfaces": items, "version": version, "demo": c.Demo}
		if worker != nil {
			stats["notifications"] = map[string]uint64{"sent": worker.Sent.Load(), "failed": worker.Failed.Load(), "dropped": worker.Dropped.Load()}
		}
		return stats
	}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	httpServer := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errorsCh <- err
		}
	}()
	defer func() {
		api.Ready.Store(false)
		cancel()
		shutdownCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			httpServer.Close()
		}
	}()
	for _, cap := range captures {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := cap.Run(ctx, c, observations); e != nil {
				api.Ready.Store(false)
				errorsCh <- e
			}
		}()
	}
	if c.Demo {
		wg.Add(1)
		go func() { defer wg.Done(); demo(ctx, observations) }()
	}
	if c.ReverseDNS && !c.Demo {
		wg.Add(1)
		go func() { defer wg.Done(); reverseDNS(ctx, engine, errorsCh) }()
	}
	api.Ready.Store(true)
	slog.Info("tunwatch ready", "listen", c.Listen, "demo", c.Demo, "node", c.NodeName, "interfaces", c.Interfaces)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	checkpoint := time.NewTicker(30 * time.Second)
	defer checkpoint.Stop()
	var lastDrops uint64
	var agingSuppressedUntil time.Time
	for {
		select {
		case <-ctx.Done():
			return engine.Save(time.Now().UTC())
		case err := <-errorsCh:
			return err
		case o := <-observations:
			if err := engine.Observe(o); err != nil {
				return err
			}
		case now := <-tick.C:
			var drops uint64
			for _, cap := range captures {
				drops += cap.Dropped.Load() + cap.KernelDropped.Load()
			}
			if drops != lastDrops {
				lastDrops = drops
				agingSuppressedUntil = now.Add(c.Offline)
				slog.Warn("capture loss detected; silence-based status transitions temporarily suspended")
			}
			if api.Ready.Load() && now.After(agingSuppressedUntil) {
				if err := engine.Tick(now.UTC()); err != nil {
					return err
				}
			}
		case now := <-checkpoint.C:
			if err := engine.Save(now.UTC()); err != nil {
				return err
			}
			if err := journal.Maintain(now); err != nil {
				return err
			}
		}
	}
}
func reverseDNS(ctx context.Context, e *monitor.Engine, errs chan<- error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	tried := map[string]time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for id, t := range tried {
				if now.Sub(t) > time.Hour {
					delete(tried, id)
				}
			}
			for _, d := range e.Devices() {
				if d.Status != "online" || d.Hostname.Value != "" || len(d.IPs) == 0 || !tried[d.ID].IsZero() {
					continue
				}
				tried[d.ID] = now
				c, cancel := context.WithTimeout(ctx, 2*time.Second)
				names, err := net.DefaultResolver.LookupAddr(c, d.IPs[len(d.IPs)-1])
				cancel()
				if err == nil && len(names) > 0 {
					if err = e.SetHostname(d.ID, names[0]); err != nil {
						select {
						case errs <- err:
						case <-ctx.Done():
						}
						return
					}
				}
				break
			}
		}
	}
}
func demo(ctx context.Context, out chan<- model.Observation) {
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	cycle := 0
	for {
		for i, host := range []string{"atlas-workstation", "living-room-tv", "home-server", "pixel-phone", "studio-speaker", "access-point"} {
			if i == 3 && cycle%20 > 9 {
				continue
			}
			o := model.Observation{Interface: "demo0", MAC: fmt.Sprintf("02:54:00:12:34:%02x", i+1), IP: fmt.Sprintf("192.0.2.%d", i+10), Source: "dhcp", Hostname: host, TTL: 64, At: time.Now().UTC()}
			if i == 0 {
				o.VendorClass = "MSFT 5.0"
			}
			if i == 3 {
				o.VendorClass = "android-dhcp"
			}
			select {
			case out <- o:
			case <-ctx.Done():
				return
			}
		}
		cycle++
		select {
		case <-tick.C:
		case <-ctx.Done():
			return
		}
	}
}
