// Command foya is the kernel entry point. It starts the daemon by default and
// provides exec for one-shot headless runs.
//
// The daemon serves HTTP over a private Unix domain socket by default.
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/freesoulcode/foya/internal/channel/feishu"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/contextdata"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"
	"github.com/freesoulcode/foya/internal/kernel"
	"github.com/freesoulcode/foya/internal/mcpclient"

	"github.com/freesoulcode/foya/internal/server"

	"github.com/freesoulcode/foya/internal/websearch"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "serve":
			runDaemon(os.Args[2:])
			return
		case "exec":
			runExec(os.Args[2:])
			return
		case "skills":
			runSkills(os.Args[2:])
			return
		case "agents":
			runAgents(os.Args[2:])
			return
		case "projects":
			runProjects(os.Args[2:])
			return
		case "rules":
			runContextItems("rules", os.Args[2:])
			return
		case "memory":
			runContextItems("memory", os.Args[2:])
			return
		case "mcp":
			runMCP(os.Args[2:])
			return
		case "web-search":
			runWebSearch(os.Args[2:])
			return
		case "bot":
			runBot(os.Args[2:])
			return
		}
	}
	runDaemon(os.Args[1:])
}

// runDaemon starts the persistent kernel process.
func runDaemon(args []string) {
	if err := serveDaemon(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serveDaemon(args []string) error {
	cfg, err := daemonConfig(args)
	if err != nil {
		return err
	}
	app, err := kernel.New(cfg)
	if err != nil {
		return fmt.Errorf("kernel initialization failed: %w", err)
	}
	defer app.Close()
	srv := server.New(cfg, app.Service())
	srv.SetChannelManager(app.Channels())
	srv.SetAutomationManager(app.Automations())

	ln, desc, err := listen(cfg)
	if err != nil {
		return fmt.Errorf("kernel listen failed: %w", err)
	}
	defer ln.Close()
	if cfg.Transport == config.TransportUnixSocket {
		defer os.Remove(cfg.SocketPath)
	}
	fmt.Printf("foya kernel listening on %s\n", desc)

	httpServer := newHTTPServer(srv.Handler())
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- serveHTTP(httpServer, ln, cfg)
	}()

	ctx, stop := signal.NotifyContext(context.Background(), terminationSignals()...)
	defer stop()
	parentDone := watchParentProcess()
	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("kernel exited: %w", err)
		}
		return nil
	case <-ctx.Done():
	case <-parentDone:
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("kernel shutdown failed: %w", err)
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("kernel exited: %w", err)
	}
	return nil
}

func daemonConfig(args []string) (config.Config, error) {
	cfg := config.Default()
	flags := flag.NewFlagSet("foya serve", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	listenAddr := flags.String("listen", cfg.Addr, "TCP listen address")
	socketPath := flags.String("socket", cfg.SocketPath, "Unix socket path")
	dataDir := flags.String("data-dir", cfg.DataDir, "persistent data directory")
	authTokenHashFile := flags.String("auth-token-hash-file", cfg.AuthTokenHashFile, "SHA-256 bearer token digest file")
	tlsCert := flags.String("tls-cert", cfg.TLSCertFile, "TLS certificate file")
	tlsKey := flags.String("tls-key", cfg.TLSKeyFile, "TLS private key file")
	allowPlaintext := flags.Bool("allow-plaintext", cfg.AllowPlaintext, "allow HTTP for a trusted reverse proxy")
	if err := flags.Parse(args); err != nil {
		return config.Config{}, fmt.Errorf("parse server flags: %w", err)
	}
	if flags.NArg() != 0 {
		return config.Config{}, errors.New("usage: foya serve [--listen host:port] [--data-dir path] [--auth-token-hash-file path] [--tls-cert path --tls-key path]")
	}

	previousDataDir := cfg.DataDir
	cfg.DataDir = strings.TrimSpace(*dataDir)
	cfg.SocketPath = strings.TrimSpace(*socketPath)
	socketWasSet := false
	flags.Visit(func(item *flag.Flag) {
		if item.Name == "socket" {
			socketWasSet = true
		}
	})
	if !socketWasSet && cfg.DataDir != previousDataDir {
		cfg.SocketPath = filepath.Join(cfg.DataDir, "kernel.sock")
	}
	cfg.Addr = strings.TrimSpace(*listenAddr)
	cfg.AuthTokenHashFile = strings.TrimSpace(*authTokenHashFile)
	cfg.TLSCertFile = strings.TrimSpace(*tlsCert)
	cfg.TLSKeyFile = strings.TrimSpace(*tlsKey)
	cfg.AllowPlaintext = *allowPlaintext
	if cfg.Addr != "" {
		cfg.Transport = config.TransportTCP
		cfg.Lifecycle = config.LifecycleService
	} else {
		cfg.Transport = config.TransportUnixSocket
		cfg.Lifecycle = config.LifecycleEphemeral
		cfg.AuthTokenSHA256 = ""
	}
	if cfg.Transport == config.TransportTCP && cfg.AuthTokenHashFile != "" {
		if cfg.AuthTokenSHA256 != "" {
			return config.Config{}, errors.New("configure only one of FOYA_AUTH_TOKEN_SHA256 and FOYA_AUTH_TOKEN_HASH_FILE")
		}
		tokenHash, err := os.ReadFile(cfg.AuthTokenHashFile)
		if err != nil {
			return config.Config{}, fmt.Errorf("read auth token hash file: %w", err)
		}
		cfg.AuthTokenSHA256 = strings.ToLower(strings.TrimSpace(string(tokenHash)))
	}
	if err := config.ValidateServerConfig(cfg); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
}

func serveHTTP(httpServer *http.Server, ln net.Listener, cfg config.Config) error {
	if cfg.Transport == config.TransportTCP && cfg.TLSCertFile != "" {
		return httpServer.ServeTLS(ln, cfg.TLSCertFile, cfg.TLSKeyFile)
	}
	return httpServer.Serve(ln)
}

// listen creates the configured transport listener.
func listen(cfg config.Config) (net.Listener, string, error) {
	switch cfg.Transport {
	case config.TransportTCP:
		ln, err := net.Listen("tcp", cfg.Addr)
		if err != nil {
			return nil, "", err
		}
		scheme := "http"
		if cfg.TLSCertFile != "" {
			scheme = "https"
		}
		return ln, scheme + "://" + ln.Addr().String(), nil
	default: // TransportUnixSocket
		return listenUnix(cfg.SocketPath)
	}
}

// listenUnix creates a Unix domain socket in a private directory.
func listenUnix(path string) (net.Listener, string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", fmt.Errorf("create socket dir: %w", err)
	}
	// Remove only stale sockets. Deleting an active socket could let multiple
	// kernels mutate the same persistent state.
	if _, err := os.Lstat(path); err == nil {
		conn, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return nil, "", fmt.Errorf("kernel is already listening on unix:%s", path)
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, "", fmt.Errorf("remove stale socket: %w", err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, "", fmt.Errorf("listen unix: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, "", fmt.Errorf("chmod socket: %w", err)
	}
	return ln, "unix:" + path, nil
}

func newApp() *kernel.App {
	cfg := config.Default()
	cfg.DisableExternalIntegrations = true
	app, err := kernel.New(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return app
}

// runExec performs one headless task.
func runExec(args []string) {
	flags := flag.NewFlagSet("foya exec", flag.ExitOnError)
	projectID := flags.String("project", "", "project id")
	connection := flags.String("connection", "", "model connection id")
	model := flags.String("model", "", "model id")
	mode := flags.String("approval", string(interaction.ModeManual), "manual, auto, or full_access")
	var images stringListFlag
	flags.Var(&images, "image", "image path (repeatable)")
	_ = flags.Parse(args)
	prompt := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if prompt == "" && len(images) == 0 {
		data, _ := io.ReadAll(os.Stdin)
		prompt = strings.TrimSpace(string(data))
	}
	if prompt == "" && len(images) == 0 {
		fmt.Fprintln(os.Stderr, "usage: foya exec [flags] [--image <path>] <prompt>")
		os.Exit(2)
	}
	app := newApp()
	defer app.Close()
	ctx, stop := signal.NotifyContext(context.Background(), terminationSignals()...)
	defer stop()
	sess, err := app.Service().CreateSession(conversation.CreateOptions{
		ConnectionID: *connection, Model: *model, ProjectID: *projectID, ApprovalMode: *mode,
	})
	if err != nil {
		fatal(err)
	}
	attachments := make([]conversation.AttachmentRef, 0, len(images))
	for _, path := range images {
		file, openErr := os.Open(path)
		if openErr != nil {
			fatal(openErr)
		}
		ref, putErr := app.Service().PutImage(ctx, sess.ID, filepath.Base(path), file)
		_ = file.Close()
		if putErr != nil {
			fatal(putErr)
		}
		attachments = append(attachments, ref)
	}
	events := app.Service().Subscribe(ctx, sess.ID)
	if _, err := app.Service().SubmitInput(ctx, sess.ID, conversation.UserInput{
		Text: prompt, Attachments: attachments,
	}); err != nil {
		fatal(err)
	}
	reader := bufio.NewReader(os.Stdin)
	for item := range events {
		switch item.Kind {
		case conversation.KindMessageDelta:
			if text, ok := item.Payload.(string); ok {
				fmt.Print(text)
			}
		case conversation.KindApprovalReq:
			request, ok := item.Payload.(interaction.Request)
			if !ok {
				continue
			}
			fmt.Fprintf(os.Stderr, "\nApprove %s: %s? [y/N] ", request.ToolName, request.Detail)
			answer, _ := reader.ReadString('\n')
			decision := interaction.DecisionDenied
			if strings.EqualFold(strings.TrimSpace(answer), "y") {
				decision = interaction.DecisionApproved
			}
			if err := app.Service().ResolveApproval(request.ID, string(decision)); err != nil {
				fatal(err)
			}
		case conversation.KindError:
			fmt.Fprintln(os.Stderr, "\n", item.Payload)
		case conversation.KindTurnComplete:
			fmt.Println()
			return
		}
	}
}

// runBot starts the kernel and exposes it through a Feishu long connection.
// The regular HTTP listener stays available so desktop clients can share the
// same sessions without opening the data directory from a second process.
func runBot(args []string) {
	flags := flag.NewFlagSet("foya bot", flag.ExitOnError)
	appID := flags.String("app-id", os.Getenv("FOYA_FEISHU_APP_ID"), "Feishu app ID")
	appSecret := flags.String("app-secret", os.Getenv("FOYA_FEISHU_APP_SECRET"), "Feishu app secret")
	connectionID := flags.String("connection", os.Getenv("FOYA_FEISHU_CONNECTION_ID"), "model connection id")
	model := flags.String("model", os.Getenv("FOYA_FEISHU_MODEL"), "model id")
	projectID := flags.String("project", os.Getenv("FOYA_FEISHU_PROJECT_ID"), "project id")
	mode := flags.String("approval", envOrDefault("FOYA_FEISHU_APPROVAL_MODE", string(interaction.ModeAuto)), "auto or full_access")
	allowAll := flags.Bool("allow-all", false, "allow every Feishu user and chat")
	allowUsers := stringListFlag(splitCommaList(os.Getenv("FOYA_FEISHU_ALLOWED_USERS")))
	allowChats := stringListFlag(splitCommaList(os.Getenv("FOYA_FEISHU_ALLOWED_CHATS")))
	flags.Var(&allowUsers, "allow-user", "allowed Feishu user open_id (repeatable)")
	flags.Var(&allowChats, "allow-chat", "allowed Feishu chat_id (repeatable)")
	_ = flags.Parse(args)
	if flags.NArg() != 0 {
		fatal(errors.New("usage: foya bot [flags]"))
	}
	if strings.TrimSpace(*appID) == "" || strings.TrimSpace(*appSecret) == "" {
		fatal(errors.New("Feishu app ID and app secret are required"))
	}

	cfg, err := daemonConfig(nil)
	if err != nil {
		fatal(err)
	}
	cfg.DisableExternalIntegrations = true
	app, err := kernel.New(cfg)
	if err != nil {
		fatal(fmt.Errorf("kernel initialization failed: %w", err))
	}
	defer app.Close()

	channelInput := feishu.UpdateInput{
		Name:         "Feishu Bot",
		Enabled:      true,
		ConnectionID: *connectionID,
		Model:        *model,
		ProjectID:    *projectID,
		ApprovalMode: interaction.Mode(*mode),
		AllowedUsers: append([]string(nil), allowUsers...),
		AllowedChats: append([]string(nil), allowChats...),
		AllowAll:     *allowAll,
		AppID:        *appID,
		AppSecret:    *appSecret,
	}
	var updateErr error
	if channels := app.Channels().List(); len(channels) > 0 {
		_, updateErr = app.Channels().Update(channels[0].ID, channelInput)
	} else {
		_, updateErr = app.Channels().Create(channelInput)
	}
	if updateErr != nil {
		fatal(updateErr)
	}

	ln, desc, err := listen(cfg)
	if err != nil {
		fatal(fmt.Errorf("kernel listen failed: %w", err))
	}
	defer ln.Close()
	if cfg.Transport == config.TransportUnixSocket {
		defer os.Remove(cfg.SocketPath)
	}

	ctx, stop := signal.NotifyContext(context.Background(), terminationSignals()...)
	defer stop()
	srv := server.New(cfg, app.Service())
	srv.SetChannelManager(app.Channels())
	srv.SetAutomationManager(app.Automations())
	httpServer := newHTTPServer(srv.Handler())
	httpResult := make(chan error, 1)
	go func() {
		httpResult <- serveHTTP(httpServer, ln, cfg)
	}()
	fmt.Printf("foya kernel listening on %s\n", desc)
	fmt.Println("foya Feishu bot starting")

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-httpResult:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	}
	stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	_ = ln.Close()
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		fatal(runErr)
	}
}

func splitCommaList(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

type stringListFlag []string

func (values *stringListFlag) String() string { return strings.Join(*values, ",") }
func (values *stringListFlag) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func runAgents(args []string) {
	flags := flag.NewFlagSet("foya agents", flag.ExitOnError)
	projectID := flags.String("project", "", "project id")
	_ = flags.Parse(args)
	if flags.NArg() != 0 {
		fatal(fmt.Errorf("usage: foya agents [--project <id>]"))
	}
	app := newApp()
	defer app.Close()
	var (
		items any
		err   error
	)
	if *projectID == "" {
		items, err = app.Service().Agents(context.Background())
	} else {
		items, err = app.Service().ProjectAgents(context.Background(), *projectID)
	}
	if err != nil {
		fatal(err)
	}
	printJSON(items)
}

func runSkills(args []string) {
	app := newApp()
	defer app.Close()
	if len(args) == 0 || args[0] == "list" {
		flags := flag.NewFlagSet("foya skills list", flag.ExitOnError)
		projectID := flags.String("project", "", "project id")
		if len(args) > 0 {
			_ = flags.Parse(args[1:])
		}
		if flags.NArg() != 0 {
			fatal(fmt.Errorf("usage: foya skills list [--project <id>]|enable <ref>|disable <ref>"))
		}
		var (
			items any
			err   error
		)
		if *projectID == "" {
			items, err = app.Service().AllSkills(context.Background())
		} else {
			items, err = app.Service().ProjectSkills(context.Background(), *projectID)
		}
		if err != nil {
			fatal(err)
		}
		printJSON(items)
		return
	}
	if len(args) != 2 || (args[0] != "enable" && args[0] != "disable") {
		fatal(fmt.Errorf("usage: foya skills list [--project <id>]|enable <ref>|disable <ref>"))
	}
	if err := app.Service().SetSkillEnabled(args[1], args[0] == "enable"); err != nil {
		fatal(err)
	}
}

func runProjects(args []string) {
	app := newApp()
	defer app.Close()
	if len(args) == 0 || (len(args) == 1 && args[0] == "list") {
		items, err := app.Service().Projects()
		if err != nil {
			fatal(err)
		}
		printJSON(items)
		return
	}
	if len(args) == 2 && args[0] == "add" {
		item, err := app.Service().RegisterProject(args[1], "")
		if err != nil {
			fatal(err)
		}
		printJSON(item)
		return
	}
	if len(args) == 2 && args[0] == "delete" {
		if err := app.Service().DeleteProject(context.Background(), args[1]); err != nil {
			fatal(err)
		}
		return
	}
	fatal(fmt.Errorf("usage: foya projects list|add <path>|delete <id>"))
}

func runContextItems(kind string, args []string) {
	app := newApp()
	defer app.Close()
	action := "list"
	if kind == "memory" {
		action = "show"
	}
	if len(args) > 0 {
		action = args[0]
		args = args[1:]
	}
	flags := flag.NewFlagSet("foya "+kind+" "+action, flag.ExitOnError)
	projectID := flags.String("project", "", "project id")
	_ = flags.Parse(args)
	scope := contextdata.ScopeGlobal
	if *projectID != "" {
		scope = contextdata.ScopeProject
	}

	if kind == "memory" {
		switch action {
		case "status":
			if flags.NArg() != 0 {
				break
			}
			settings, err := app.Service().MemorySettings()
			if err != nil {
				fatal(err)
			}
			printJSON(settings)
			return
		case "enable", "disable":
			if flags.NArg() != 0 {
				break
			}
			settings, err := app.Service().UpdateMemorySettings(contextdata.MemorySettings{
				Enabled: action == "enable",
			})
			if err != nil {
				fatal(err)
			}
			printJSON(settings)
			return
		case "show":
			if flags.NArg() != 0 {
				break
			}
			item, err := app.Service().Memory(scope, *projectID)
			if err != nil {
				fatal(err)
			}
			printJSON(item)
			return
		case "set":
			content := contextCommandContent(flags.Args())
			if content == "" {
				break
			}
			item, err := app.Service().SetMemory(scope, *projectID, content)
			if err != nil {
				fatal(err)
			}
			printJSON(item)
			return
		case "clear":
			if flags.NArg() != 0 {
				break
			}
			if err := app.Service().ClearMemory(scope, *projectID); err != nil {
				fatal(err)
			}
			return
		}
		fatal(fmt.Errorf(
			"usage: foya memory status|enable|disable|show [--project <id>]|set [--project <id>] <text>|clear [--project <id>]",
		))
	}

	switch action {
	case "list":
		if flags.NArg() != 0 {
			break
		}
		var (
			items any
			err   error
		)
		items, err = app.Service().Rules(scope, *projectID)
		if err != nil {
			fatal(err)
		}
		printJSON(items)
		return
	case "add":
		content := contextCommandContent(flags.Args())
		if content == "" {
			break
		}
		var (
			item any
			err  error
		)
		item, err = app.Service().CreateRule(scope, *projectID, content)
		if err != nil {
			fatal(err)
		}
		printJSON(item)
		return
	case "edit":
		if flags.NArg() < 2 {
			break
		}
		id := flags.Arg(0)
		content := strings.TrimSpace(strings.Join(flags.Args()[1:], " "))
		var (
			item any
			err  error
		)
		item, err = app.Service().UpdateRule(id, content)
		if err != nil {
			fatal(err)
		}
		printJSON(item)
		return
	case "delete":
		if flags.NArg() != 1 {
			break
		}
		var err error
		err = app.Service().DeleteRule(flags.Arg(0))
		if err != nil {
			fatal(err)
		}
		return
	}
	fatal(fmt.Errorf(
		"usage: foya rules list [--project <id>]|add [--project <id>] <text>|edit <id> <text>|delete <id>",
	))
}

func contextCommandContent(args []string) string {
	content := strings.TrimSpace(strings.Join(args, " "))
	if content != "" {
		return content
	}
	data, _ := io.ReadAll(os.Stdin)
	return strings.TrimSpace(string(data))
}

func runMCP(args []string) {
	app := newApp()
	defer app.Close()
	if len(args) == 0 || args[0] == "list" {
		statuses, err := app.Service().MCPStatuses()
		if err != nil {
			fatal(err)
		}
		printJSON(statuses)
		return
	}
	if len(args) == 2 && args[0] == "apply" {
		data, err := os.ReadFile(args[1])
		if err != nil {
			fatal(err)
		}
		var next mcpclient.Config
		if err := json.Unmarshal(data, &next); err != nil {
			fatal(err)
		}
		if err := app.Service().ReplaceMCPConfig(context.Background(), next); err != nil {
			fatal(err)
		}
		return
	}
	fatal(fmt.Errorf("usage: foya mcp list|apply <config.json>"))
}

func runWebSearch(args []string) {
	app := newApp()
	defer app.Close()
	if len(args) == 0 || args[0] == "show" {
		settings, err := app.Service().WebSearchSettings()
		if err != nil {
			fatal(err)
		}
		printJSON(settings)
		return
	}
	if len(args) >= 2 && args[0] == "test" {
		results, source, err := app.Service().SearchWeb(context.Background(), strings.Join(args[1:], " "))
		if err != nil {
			fatal(err)
		}
		printJSON(struct {
			Provider string `json:"provider"`
			Results  any    `json:"results"`
		}{source, results})
		return
	}
	if len(args) == 4 && args[0] == "set-google" {
		settings := websearch.Settings{
			Enabled: true, DefaultProvider: args[1],
			Providers: []websearch.ProviderConfig{{
				ID: args[1], Name: "Google Custom Search (legacy)", Kind: "google_cse",
				Enabled: true, SearchEngineID: args[2], APIKey: args[3],
			}},
		}
		if err := app.Service().UpdateWebSearchSettings(settings); err != nil {
			fatal(err)
		}
		return
	}
	fatal(fmt.Errorf("usage: foya web-search show|test <query>|set-google <id> <cx> <api-key>"))
}

func printJSON(value any) {
	data, _ := json.MarshalIndent(value, "", "  ")
	fmt.Println(string(data))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
