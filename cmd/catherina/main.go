// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package main

import (
	"catherina/internal/app"
	"catherina/internal/mengka"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

func main() {
	log.SetOutput(os.Stderr)
	log.SetPrefix("[Catherina] ")
	log.SetFlags(log.Ldate | log.Ltime)
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) == 3 && os.Args[1] == "--apply-update" {
		return app.ApplyUpdate(os.Args[2])
	}
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("Catherina " + app.Version)
		return nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	defaultDir := filepath.Join(base, "Catherina-Mengka")
	dataDir := flag.String("data-dir", defaultDir, "数据目录（绝对路径，升级时保留）")
	listen := flag.String("listen", "127.0.0.1:18096", "本机管理地址")
	open := flag.Bool("open", runtime.GOOS == "windows", "启动后打开浏览器")
	flag.Parse()
	if !filepath.IsAbs(*dataDir) {
		return errors.New("--data-dir 必须为绝对路径")
	}
	if err := app.InitFeeds(os.Getenv); err != nil {
		return err
	}
	managed, err := app.ResolveManaged(os.Getenv, os.Environ)
	if err != nil {
		return err
	}
	if managed != nil {

		*dataDir = managed.DataDir()
		*listen = managed.AdminAddr()
		*open = false
	}
	store, err := app.OpenStore(*dataDir)
	if err != nil {
		return fmt.Errorf("打开数据库失败：%w", err)
	}
	defer store.Close()
	settings, err := app.LoadSettings(store)
	if err != nil {
		return err
	}
	clientOutput := io.Writer(io.Discard)
	if managed != nil && managed.Transport() == "native-ipc-v1" {
		clientOutput = os.Stdout
	}
	client := mengka.New(clientOutput, app.Actions)
	if managed != nil {
		managed.Client = client
	}
	engine := app.NewEngine(client, store, *dataDir)
	defer engine.Stop()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	forward := &mengka.Forward{Client: client, GetConnection: settings.Connection, Permissions: app.EventPermissions, Version: app.Version}
	if managed != nil {
		forward.GetConnection = managed.Connection
		forward.ManagedGate = managed.Active
	}
	client.OnEvent = engine.Submit
	client.OnLifecycle = func(event string) {
		// Codex 修改：兼容旧版 activate 与新版 started 生命周期。
		if event == "activate" || event == "started" {
			engine.Start()
		}
	}
	updateStop := make(chan string, 1)
	updater := app.NewUpdater(settings, *dataDir, "http://"+*listen+"/api/auth/status", func(marker string) {
		select {
		case updateStop <- marker:
		default:
		}
	})
	updater.SetManaged(managed != nil)
	engine.Policy = updater.Policy
	defer updater.Stop()
	server, err := app.StartAdmin(engine, settings, forward, updater, *listen, managed)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = server.Shutdown(shutdownCtx)
	}()
	forwardDone := make(chan struct{})
	if managed != nil && managed.Transport() == "native-ipc-v1" {

		go func() {
			defer close(forwardDone)
			readerDone := make(chan struct{})
			defer close(readerDone)
			go func() {
				select {
				case <-ctx.Done():
					_ = os.Stdin.Close()
				case <-readerDone:
				}
			}()
			if err := client.Hello("catherina", app.Version); err != nil {
				log.Printf("萌卡原生 IPC 握手发送失败：%v", err)
				return
			}
			if err := client.Run(ctx, os.Stdin); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("萌卡原生 IPC 已断开：%v", err)
			}
		}()
	} else {
		go func() { defer close(forwardDone); forward.Run(ctx) }()
	}
	updater.Start()
	log.Printf("Catherina %s，管理页面 http://%s", app.Version, *listen)
	log.Printf("数据目录：%s", *dataDir)
	if *open {
		addr := "http://" + *listen
		if runtime.GOOS == "windows" {
			_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", addr).Start()
		} else {
			_ = exec.Command("xdg-open", addr).Start()
		}
	}
	marker := ""
	select {
	case <-ctx.Done():
	case marker = <-updateStop:
	case <-forwardDone:
	}
	cancel()
	updater.Stop()
	engine.Stop()
	shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	_ = server.Shutdown(shutdownCtx)
	stop()
	<-forwardDone
	if err = store.Close(); err != nil {
		return err
	}
	if marker != "" {
		return os.WriteFile(marker, []byte("stopped"), 0600)
	}
	return nil
}
