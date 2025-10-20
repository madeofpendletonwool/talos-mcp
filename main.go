package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

func loadConfig() (*Config, error) {
	config := &Config{}
	
	// Set defaults
	config.Server.Name = "talos-docs-mcp"
	config.Server.Version = "1.0.0"
	
	config.Repository.URL = "https://github.com/siderolabs/docs"
	config.Repository.Branch = "main"
	
	config.Sync.Mode = "hybrid"
	config.Sync.Webhook.Secret = ""
	config.Sync.Webhook.Endpoint = "/webhook/github"
	config.Sync.Polling.Interval = "5m"
	config.Sync.Polling.BackoffMax = "30m"
	config.Sync.HealthCheck.StaleThreshold = "1h"
	config.Sync.HealthCheck.MaxAge = "24h"
	
	config.Search.IndexPath = "./data/search_index"
	config.Search.MaxResults = 20
	config.Search.SnippetLength = 300
	
	config.Cache.TTL = "24h"
	config.Cache.MaxSize = "1GB"
	
	config.Logging.Level = "info"
	config.Logging.Format = "json"
	
	config.Monitoring.MetricsEnabled = true
	config.Monitoring.AlertsEnabled = true

	// Try to load from config file if it exists
	configPath := "config.yaml"
	if _, err := os.Stat(configPath); err == nil {
		// For now, just use defaults since we don't have YAML parsing
		log.Printf("Found config file at %s, using defaults for now", configPath)
	}

	// Override with environment variables
	if name := os.Getenv("TALOS_MCP_NAME"); name != "" {
		config.Server.Name = name
	}
	if version := os.Getenv("TALOS_MCP_VERSION"); version != "" {
		config.Server.Version = version
	}
	if repoURL := os.Getenv("TALOS_MCP_REPO_URL"); repoURL != "" {
		config.Repository.URL = repoURL
	}
	if branch := os.Getenv("TALOS_MCP_BRANCH"); branch != "" {
		config.Repository.Branch = branch
	}
	if indexPath := os.Getenv("TALOS_MCP_INDEX_PATH"); indexPath != "" {
		config.Search.IndexPath = indexPath
	}
	if webhookSecret := os.Getenv("TALOS_MCP_WEBHOOK_SECRET"); webhookSecret != "" {
		config.Sync.Webhook.Secret = webhookSecret
	}
	if logLevel := os.Getenv("TALOS_MCP_LOG_LEVEL"); logLevel != "" {
		config.Logging.Level = logLevel
	}

	return config, nil
}

func setupLogging(config *Config) {
	// Always log to stderr (MCP uses stdout for JSON-RPC)
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.SetOutput(os.Stderr)
}

func main() {
	// Load configuration
	config, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Setup logging
	setupLogging(config)

	log.Printf("Starting %s v%s", config.Server.Name, config.Server.Version)

	// Create MCP server
	log.Printf("DEBUG: Creating MCP server...")
	server, err := NewTalosDocMCPServer(config)
	if err != nil {
		log.Fatalf("Failed to create MCP server: %v", err)
	}
	log.Printf("DEBUG: MCP server created successfully")

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start server in goroutine
	go func() {
		if err := server.Start(); err != nil {
			log.Printf("Server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutdown signal received, stopping server...")
	
	// Cleanup
	server.Stop()
	log.Println("Server stopped")
}