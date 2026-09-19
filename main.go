package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"mailservice/internal/admin"
	"mailservice/internal/auth"
	"mailservice/internal/config"
	"mailservice/internal/public"
	"mailservice/internal/routes"
	"mailservice/internal/services"
	"mailservice/internal/store"
	"mailservice/internal/web"
)

const (
	minSuperKeyLength = 32
	mailWorkers       = 3
	version           = "1.1.0"
)

func main() {
	if err := config.LoadDotEnv(".env"); err != nil {
		log.Fatalf("failed to load .env: %v", err)
	}
	trustProxy := envBool("TRUST_PROXY")

	keyCipher, err := store.NewCipher(strings.TrimSpace(os.Getenv("API_KEY_ENCRYPTION_KEY")))
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	ctx := context.Background()
	db, err := store.Open(ctx, os.Getenv("DATABASE_URL"), keyCipher)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()

	superKey := strings.TrimSpace(os.Getenv("SUPER_API_KEY"))
	if superKey != "" && len(superKey) < minSuperKeyLength {
		log.Fatalf("SUPER_API_KEY must be at least %d characters", minSuperKeyLength)
	}
	revoked, err := db.SyncConfigSuperKey(ctx, superKey)
	if err != nil {
		log.Fatalf("register SUPER_API_KEY: %v", err)
	}
	if revoked > 0 {
		log.Printf("disabled %d super key(s) from a previous SUPER_API_KEY value", revoked)
	}

	// The queue lives in memory, so mail still waiting when the service last stopped was lost.
	if lost, err := db.FailInterruptedDeliveries(ctx); err != nil {
		log.Fatalf("mail history: %v", err)
	} else if lost > 0 {
		log.Printf("marked %d mail(s) interrupted by the last shutdown as failed", lost)
	}

	mailService := services.NewMailService(mailWorkers, services.NewSMTPMailer())
	mailService.EnableRecording(db, services.DefaultSMTPConfig())
	mailService.Start()

	adminHandler, err := admin.New(db, admin.Config{
		Username:     os.Getenv("SUPERUSER_USERNAME"),
		Password:     os.Getenv("SUPERUSER_PASSWORD"),
		SecureCookie: envBool("COOKIE_SECURE"),
		TrustProxy:   trustProxy,
		DefaultSMTP:  services.DefaultSMTPConfig(),
		SendTest:     services.SendTest,
		Submit:       mailService.Submit,
		Enqueue:      mailService.Enqueue,
		SenderFor:    mailService.SenderFor,
	})
	if err != nil {
		log.Fatalf("admin configuration error: %v", err)
	}

	mux := http.NewServeMux()
	routes.RegisterRoutes(mux, mailService, db)
	adminHandler.Register(mux)
	delivery := "simulation"
	if services.SMTPConfigured() {
		delivery = "smtp"
	}
	public.New(db, public.Config{
		Version: version, Delivery: delivery, Workers: mailWorkers, TrustProxy: trustProxy,
		Submit: mailService.Submit,
	}).Register(mux)
	web.Register(mux)

	// Every route needs an API key except the health check, the public site and docs, the public
	// API, and the admin UI and API (which are protected by the super user session instead).
	publicPaths := append([]string{"/health"}, web.PublicPaths()...)
	publicPaths = append(publicPaths, public.PublicPaths()...)
	apiKeyAuth := auth.NewAPIKeyAuth(db, trustProxy, publicPaths...)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           apiKeyAuth.Middleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("Mail Service %s listening on :%s (site /, docs /docs/, admin /admin/)", version, port)
	log.Fatal(server.ListenAndServe())
}

func envBool(name string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return v == "1" || v == "true" || v == "yes"
}
