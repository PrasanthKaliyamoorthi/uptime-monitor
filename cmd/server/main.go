package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"uptime-monitor/internal/checker"
	"uptime-monitor/internal/db"
	"uptime-monitor/internal/handler"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/notifier"
)

func main() {
	var (
		portFlag          = flag.Int("port", 8080, "HTTP server port")
		dbPathFlag        = flag.String("db", "uptime.db", "SQLite database file path")
		workersFlag       = flag.Int("workers", 20, "Maximum concurrent background check workers")
		resetAdminFlag    = flag.Bool("reset-admin", false, "Reset the administrator password from the command line")
		adminUserFlag     = flag.String("admin-user", "", "Username of the admin account to reset (optional)")
		adminPassFlag     = flag.String("admin-pass", "", "New password to set for the admin account")
		unblockIPFlag      = flag.String("unblock-ip", "", "Remove an IP address from the admin console blacklist")
		unbanIPFlag        = flag.String("unban-ip", "", "Alias for -unblock-ip")
		listBlacklistFlag  = flag.Bool("list-blacklist", false, "List all IP addresses currently blacklisted from admin console")
		maxAttemptsFlag    = flag.Int("max-login-attempts", 5, "Maximum failed login attempts before IP blacklist")
		adminDomainFlag    = flag.String("admin-domain", "", "Restrict admin console to this domain only (e.g. console.status.sagasoft.xyz, or 'clear' to allow all)")
		getAdminDomainFlag = flag.Bool("get-admin-domain", false, "Print the currently configured admin console domain")
	)
	flag.Parse()

	// Environment variable overrides
	dbPath := *dbPathFlag
	if envDB := os.Getenv("DB_PATH"); envDB != "" {
		dbPath = envDB
	}

	port := *portFlag
	if envPort := os.Getenv("PORT"); envPort != "" {
		var p int
		if _, err := fmt.Sscanf(envPort, "%d", &p); err == nil && p > 0 {
			port = p
		}
	}

	// Initialize SQLite Database
	database, err := db.InitDB(dbPath)
	if err != nil {
		log.Fatalf("[Main] Failed to initialize database at %s: %v", dbPath, err)
	}
	defer database.Close()

	adminRepo := db.NewAdminRepo(database)
	blacklistRepo := db.NewBlacklistRepo(database)

	// Handle Admin IP Blacklist CLI Operations
	ipToUnblock := strings.TrimSpace(*unblockIPFlag)
	if ipToUnblock == "" {
		ipToUnblock = strings.TrimSpace(*unbanIPFlag)
	}
	if ipToUnblock != "" {
		if err := blacklistRepo.Remove(ipToUnblock); err != nil {
			log.Fatalf("[Blacklist] Failed to unblock IP %s: %v", ipToUnblock, err)
		}
		fmt.Printf("✓ Successfully removed IP %s from admin blacklist.\n", ipToUnblock)
		os.Exit(0)
	}

	if *listBlacklistFlag {
		entries, err := blacklistRepo.GetAll()
		if err != nil {
			log.Fatalf("[Blacklist] Failed to list blacklisted IPs: %v", err)
		}
		if len(entries) == 0 {
			fmt.Println("No IP addresses are currently blacklisted from the admin console.")
		} else {
			fmt.Printf("Currently blacklisted IP addresses (%d total):\n", len(entries))
			for _, e := range entries {
				blTime := "-"
				if e.BlacklistedAt != nil {
					blTime = e.BlacklistedAt.Format(time.RFC3339)
				}
				fmt.Printf(" - IP: %-15s | Failed Attempts: %d | Since: %s | Reason: %s\n",
					e.IP, e.FailedAttempts, blTime, e.Reason)
			}
		}
		os.Exit(0)
	}

	// Handle Admin Console Allowed Domain CLI Flags
	settingsRepo := db.NewSettingsRepo(database)

	if *getAdminDomainFlag {
		domain, err := settingsRepo.GetAdminAllowedDomain()
		if err != nil {
			log.Fatalf("[Settings] Failed to query admin domain: %v", err)
		}
		if domain == "" {
			fmt.Println("Admin console domain restriction: none (accessible from any host)")
		} else {
			fmt.Printf("Admin console domain restriction: %s\n", domain)
		}
		os.Exit(0)
	}

	adminDomainPassed := false
	portExplicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "admin-domain" {
			adminDomainPassed = true
		}
		if f.Name == "port" {
			portExplicit = true
		}
	})

	if adminDomainPassed {
		targetDomain := strings.TrimSpace(strings.ToLower(*adminDomainFlag))
		targetDomain = strings.TrimPrefix(targetDomain, "https://")
		targetDomain = strings.TrimPrefix(targetDomain, "http://")
		if targetDomain == "clear" || targetDomain == "none" || targetDomain == "all" || targetDomain == `""` {
			targetDomain = ""
		}
		if err := settingsRepo.SetAdminAllowedDomain(targetDomain); err != nil {
			log.Fatalf("[Settings] Failed to set admin allowed domain: %v", err)
		}
		if targetDomain != "" {
			fmt.Printf("✓ Successfully restricted admin console to domain %q\n", targetDomain)
		} else {
			fmt.Println("✓ Successfully cleared admin console domain restriction (accessible from any host)")
		}
		if !portExplicit {
			os.Exit(0)
		}
	}

	// Handle Admin Password Reset CLI Flag
	if *resetAdminFlag {
		newPass := *adminPassFlag
		if newPass == "" {
			fmt.Print("Enter new administrator password: ")
			reader := bufio.NewReader(os.Stdin)
			input, _ := reader.ReadString('\n')
			newPass = strings.TrimSpace(input)
		}

		if newPass == "" {
			log.Fatalf("[Reset] Password cannot be empty.")
		}

		user := *adminUserFlag
		if err := adminRepo.ResetPassword(user, newPass); err != nil {
			log.Fatalf("[Reset] Failed to reset administrator password: %v", err)
		}

		if user != "" {
			fmt.Printf("✓ Successfully reset password for admin user %q\n", user)
		} else {
			fmt.Println("✓ Successfully reset password for administrator account")
		}
		os.Exit(0)
	}

	// Repositories & Subsystems
	monitorRepo := db.NewMonitorRepo(database)
	checkRepo := db.NewCheckRepo(database)
	statusPageRepo := db.NewStatusPageRepo(database)
	incidentRepo := db.NewIncidentRepo(database)
	webhookRepo := db.NewWebhookRepo(database)
	logRepo := db.NewLogRepo(database)

	sysLogger := logger.New(logRepo)
	webhookDispatcher := notifier.NewDispatcher(webhookRepo, sysLogger)

	// Checker Coordinator & Scheduler
	coordinator := checker.NewCoordinator(monitorRepo, checkRepo, webhookDispatcher, sysLogger)
	scheduler := checker.NewScheduler(coordinator, monitorRepo, sysLogger, *workersFlag)

	// Context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start background scheduler
	scheduler.Start(ctx)

	// Start background retention pruner (every 6 hours)
	db.StartPruner(ctx, checkRepo, logRepo, 6*time.Hour)

	// Build HTTP Router
	rt, err := handler.NewRouter(
		adminRepo,
		blacklistRepo,
		monitorRepo,
		checkRepo,
		statusPageRepo,
		incidentRepo,
		webhookRepo,
		logRepo,
		scheduler,
		coordinator,
		sysLogger,
		*maxAttemptsFlag,
	)
	if err != nil {
		log.Fatalf("[Main] Failed to initialize template renderer / router: %v", err)
	}

	tr, _ := handler.NewTemplateRenderer()
	rt.SetAuxHandlers(
		handler.NewAdminIncidentsHandler(incidentRepo, webhookDispatcher, tr, sysLogger),
		handler.NewAdminWebhooksHandler(webhookRepo, webhookDispatcher, tr, sysLogger),
	)

	serverAddr := fmt.Sprintf(":%d", port)
	srv := &http.Server{
		Addr:         serverAddr,
		Handler:      rt,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		sysLogger.Info("system", fmt.Sprintf("Uptime Monitor started on port %d (db: %s)", port, dbPath), nil)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[Main] Server error: %v", err)
		}
	}()

	<-sigChan
	sysLogger.Info("system", "Shutting down server gracefully...", nil)

	// Stop scheduler workers
	scheduler.Stop()

	// Shutdown HTTP server with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[Main] Error during server shutdown: %v", err)
	}

	sysLogger.Info("system", "Server shutdown complete.", nil)
}
