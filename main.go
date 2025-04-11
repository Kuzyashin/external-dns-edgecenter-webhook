package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	dnssdk "github.com/Edge-Center/edgecenter-dns-sdk-go"
	"go.uber.org/zap"
	"sigs.k8s.io/external-dns/endpoint"
)

func main() {
	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Printf("Error creating logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	apiKey := os.Getenv("EDGECENTER_API_KEY")
	if apiKey == "" {
		logger.Fatal("EDGECENTER_API_KEY environment variable is required")
	}

	baseURL := os.Getenv("EDGECENTER_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.edgecenter.ru/dns"
	}

	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		logger.Fatal("Error parsing base URL", zap.Error(err))
	}

	client := dnssdk.NewClient(dnssdk.PermanentAPIKeyAuth(apiKey), func(c *dnssdk.Client) {
		c.BaseURL = parsedURL
	})

	domainFilter := endpoint.NewDomainFilter([]string{})

	dryRun := os.Getenv("DRY_RUN") == "true"
	if dryRun {
		logger.Info("Running in dry-run mode")
	}

	provider, err := NewEdgeCenterProvider(client, domainFilter, logger, dryRun)
	if err != nil {
		logger.Fatal("Error creating EdgeCenter provider", zap.Error(err))
	}

	server := NewWebhookServer(provider, logger)

	go func() {
		if err := server.Start(8888); err != nil && err != http.ErrServerClosed {
			logger.Error("Error starting server", zap.Error(err))
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	if err := server.Shutdown(context.Background()); err != nil {
		logger.Error("Error shutting down server", zap.Error(err))
	}
}
