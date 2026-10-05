// Command alertops 启动告警平台；运行外部容器需显式启用宿主执行器。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/mylxy/ai-alertops/backend/internal/platform"
)

// main 只编排配置、服务和优雅退出。
func main() {
	if e := run(); e != nil {
		log.Print(e)
		os.Exit(1)
	}
}

// run 限制演示监听地址，所有真实接入凭据从环境注入。
func run() error {
	listen := flag.String("listen", "127.0.0.1:18080", "HTTP 监听地址")
	demo := flag.Bool("demo", false, "使用隔离模拟外部服务")
	seed := flag.Bool("seed", false, "重置并填充独立演示库")
	runner := flag.Bool("runner", false, "启用本机 Docker 执行器")
	frontend := flag.String("frontend", "../frontend/dist", "前端构建目录")
	migrate := flag.Bool("migrate", false, "仅初始化专用数据库结构后退出")
	flag.Parse()
	if *demo {
		host, _, e := net.SplitHostPort(*listen)
		if e != nil || host != "127.0.0.1" && host != "::1" {
			return errors.New("演示服务只允许监听回环地址")
		}
	}
	node := int64(1)
	if value := os.Getenv("ALERTOPS_NODE_ID"); value != "" {
		var e error
		node, e = strconv.ParseInt(value, 10, 64)
		if e != nil {
			return errors.New("节点号格式错误")
		}
	}
	config := platform.Config{DSN: os.Getenv("ALERTOPS_MYSQL_DSN"), Migrate: *migrate, NodeID: node, SessionKey: os.Getenv("ALERTOPS_SESSION_KEY"), CorpID: os.Getenv("DINGTALK_CORP_ID"), AppID: os.Getenv("DINGTALK_APP_ID"), AppSecret: os.Getenv("DINGTALK_APP_SECRET"), CoolAppCode: os.Getenv("DINGTALK_COOL_APP_CODE"), BootstrapUnionID: os.Getenv("ALERTOPS_BOOTSTRAP_UNION_ID"), BaseURL: os.Getenv("ALERTOPS_BASE_URL"), FrontendDir: *frontend, Demo: *demo, CodeupToken: os.Getenv("CODEUP_TOKEN"), CodeupHookToken: os.Getenv("CODEUP_HOOK_TOKEN"), ArtifactRoot: os.Getenv("ALERTOPS_ARTIFACT_ROOT"), RunnerCallbackURL: os.Getenv("ALERTOPS_RUNNER_CALLBACK_URL"), Templates: map[string]string{}}
	for _, kind := range []string{"LOG", "METRIC", "NOTICE", "TOPBOX"} {
		config.Templates[kind] = os.Getenv("DINGTALK_TEMPLATE_" + kind)
	}
	if *demo {
		config.CorpID = "demo-corp"
		config.AppID = "demo-app"
		config.BootstrapUnionID = "admin"
		if config.SessionKey == "" {
			config.SessionKey = "local-demo-session-key-not-for-production-2026"
		}
		if config.BaseURL == "" {
			config.BaseURL = "http://" + *listen
		}
		config.CodeupHookToken = "demo-codeup-hook-secret"
	}
	if config.DSN == "" {
		return errors.New("缺少 ALERTOPS_MYSQL_DSN")
	}
	if !*demo && !*migrate && (config.BaseURL == "" || config.AppSecret == "") {
		return errors.New("缺少真实服务地址或钉钉应用凭据")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	app, e := platform.Open(ctx, config)
	if e != nil {
		return e
	}
	defer app.Close()
	if *migrate {
		log.Print("专用数据库结构初始化完成")
		return nil
	}
	if *seed {
		if e = app.ResetDemo(ctx); e != nil {
			return e
		}
		for _, kind := range []string{"log", "metric", "notice"} {
			if e = app.DemoScenario(ctx, kind); e != nil {
				return e
			}
		}
	}
	if *runner {
		if config.ArtifactRoot == "" || config.RunnerCallbackURL == "" {
			return errors.New("启用执行器需要绝对产物目录和容器回调 URL")
		}
		app.Runtime = &platform.DockerRuntime{CallbackURL: config.RunnerCallbackURL}
	}
	server := &http.Server{Addr: *listen, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	go app.RunWorkers(ctx, func(e error) {
		if ctx.Err() == nil {
			log.Printf("持久任务处理失败：%v", e)
		}
	})
	if !*demo {
		go func() {
			if e := app.StartStream(ctx); e != nil {
				log.Print(e)
			}
		}()
	}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	log.Printf("告警平台启动：%s；演示=%t；Docker执行器=%t", *listen, *demo, *runner)
	select {
	case e := <-done:
		if !errors.Is(e, http.ErrServerClosed) {
			return fmt.Errorf("HTTP 服务：%w", e)
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
	return nil
}
