package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type TalosDocMCPServer struct {
	mcpServer   *server.MCPServer
	fetcher     *DocumentationFetcher
	searchEngine *SearchEngine
	config      *Config
}

func NewTalosDocMCPServer(config *Config) (*TalosDocMCPServer, error) {
	// Create directories
	if err := os.MkdirAll(filepath.Dir(config.Search.IndexPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create search index directory: %w", err)
	}

	// Initialize documentation fetcher
	fetcher, err := NewDocumentationFetcher(
		config.Repository.URL,
		filepath.Join(os.TempDir(), "talos-docs-repo"),
		config.Repository.Branch,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize documentation fetcher: %w", err)
	}

	// Initialize search engine
	searchEngine, err := NewSearchEngine(
		config.Search.IndexPath,
		config.Search.MaxResults,
		config.Search.SnippetLength,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize search engine: %w", err)
	}

	// Create MCP server with recovery middleware
	mcpServer := server.NewMCPServer(
		config.Server.Name,
		config.Server.Version,
		server.WithToolCapabilities(true),
		server.WithRecovery(), // Add panic recovery
	)

	talosServer := &TalosDocMCPServer{
		mcpServer:    mcpServer,
		fetcher:      fetcher,
		searchEngine: searchEngine,
		config:       config,
	}

	// Register tools
	if err := talosServer.registerTools(); err != nil {
		return nil, fmt.Errorf("failed to register tools: %w", err)
	}

	return talosServer, nil
}

func (s *TalosDocMCPServer) registerTools() error {
	// Tool 1: search_talos_docs
	searchTool := mcp.NewTool("search_talos_docs",
		mcp.WithDescription("Search across all Talos Linux documentation"),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query"),
		),
		mcp.WithString("version",
			mcp.Description("Talos version (v1.6-v1.11, default: latest)"),
		),
		mcp.WithString("section",
			mcp.Description("Section filter (getting-started, networking, security, etc.)"),
		),
		mcp.WithString("platform",
			mcp.Description("Platform filter (aws, azure, bare-metal, etc.)"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results (default: 20)"),
		),
	)

	s.mcpServer.AddTool(searchTool, s.handleSearchDocs)

	// Tool 2: get_talos_guide
	guideTool := mcp.NewTool("get_talos_guide",
		mcp.WithDescription("Retrieve complete guide for specific Talos topic"),
		mcp.WithString("topic",
			mcp.Required(),
			mcp.Description("Guide topic (quickstart, networking, upgrading, etc.)"),
		),
		mcp.WithString("version",
			mcp.Description("Talos version (default: latest)"),
		),
		mcp.WithString("platform",
			mcp.Description("Platform-specific variant"),
		),
	)

	s.mcpServer.AddTool(guideTool, s.handleGetGuide)

	// Tool 3: compare_talos_versions
	compareTool := mcp.NewTool("compare_talos_versions",
		mcp.WithDescription("Compare documentation across different Talos versions"),
		mcp.WithString("topic",
			mcp.Required(),
			mcp.Description("Topic to compare"),
		),
		mcp.WithString("from_version",
			mcp.Required(),
			mcp.Description("Starting version"),
		),
		mcp.WithString("to_version",
			mcp.Required(),
			mcp.Description("Target version"),
		),
	)

	s.mcpServer.AddTool(compareTool, s.handleCompareVersions)

	// Tool 4: get_platform_specific_docs
	platformTool := mcp.NewTool("get_platform_specific_docs",
		mcp.WithDescription("Get platform-specific installation and configuration guides"),
		mcp.WithString("platform",
			mcp.Required(),
			mcp.Description("Cloud platform or hardware"),
		),
		mcp.WithString("version",
			mcp.Description("Talos version (default: latest)"),
		),
	)

	s.mcpServer.AddTool(platformTool, s.handleGetPlatformDocs)

	// Tool 5: get_latest_release_notes
	releaseTool := mcp.NewTool("get_latest_release_notes",
		mcp.WithDescription("Get latest Talos release information"),
	)

	s.mcpServer.AddTool(releaseTool, s.handleGetReleaseNotes)

	// Tool 6: sync_documentation
	syncTool := mcp.NewTool("sync_documentation",
		mcp.WithDescription("Force sync with latest Talos documentation from GitHub"),
	)

	s.mcpServer.AddTool(syncTool, s.handleSyncDocumentation)

	return nil
}

func (s *TalosDocMCPServer) handleSearchDocs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Extract required query
	query, err := request.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError("query is required"), nil
	}

	// Extract optional parameters
	var version, section, platform string
	var limit int = 20

	version = request.GetString("version", "")
	section = request.GetString("section", "")
	platform = request.GetString("platform", "")
	if v := request.GetFloat("limit", 0); v != 0 {
		limit = int(v)
	}

	// Build search request
	searchReq := &SearchRequest{
		Query:    query,
		Version:  version,
		Section:  section,
		Platform: platform,
		Limit:    limit,
	}

	// Execute search
	response, err := s.searchEngine.Search(ctx, searchReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}

	// Format results
	result := map[string]interface{}{
		"query":    response.Query,
		"total":    response.Total,
		"duration": response.Duration.String(),
		"results":  response.Results,
	}

	resultJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal results: %v", err)), nil
	}

	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (s *TalosDocMCPServer) handleGetGuide(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	topic, err := request.RequireString("topic")
	if err != nil {
		return mcp.NewToolResultError("topic is required"), nil
	}

	var version, platform string
	version = request.GetString("version", "")
	platform = request.GetString("platform", "")

	// Search for the topic guide
	searchReq := &SearchRequest{
		Query:    topic,
		Version:  version,
		Platform: platform,
		Limit:    10,
	}

	response, err := s.searchEngine.Search(ctx, searchReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}

	if len(response.Results) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No guide found for topic: %s", topic)), nil
	}

	// Return the most relevant result with full content
	result := map[string]interface{}{
		"guide":   response.Results[0].Document,
		"topic":   topic,
		"version": version,
		"platform": platform,
	}

	resultJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (s *TalosDocMCPServer) handleCompareVersions(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	topic, err := request.RequireString("topic")
	if err != nil {
		return mcp.NewToolResultError("topic is required"), nil
	}

	fromVersion, err := request.RequireString("from_version")
	if err != nil {
		return mcp.NewToolResultError("from_version is required"), nil
	}

	toVersion, err := request.RequireString("to_version")
	if err != nil {
		return mcp.NewToolResultError("to_version is required"), nil
	}

	// Search for documents in both versions
	searchReqFrom := &SearchRequest{
		Query:   topic,
		Version: fromVersion,
		Limit:   5,
	}

	searchReqTo := &SearchRequest{
		Query:   topic,
		Version: toVersion,
		Limit:   5,
	}

	responseFrom, err := s.searchEngine.Search(ctx, searchReqFrom)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search for from_version failed: %v", err)), nil
	}

	responseTo, err := s.searchEngine.Search(ctx, searchReqTo)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search for to_version failed: %v", err)), nil
	}

	// Compare results
	comparison := map[string]interface{}{
		"topic":        topic,
		"from_version": map[string]interface{}{
			"version":  fromVersion,
			"results":  responseFrom.Results,
			"total":    responseFrom.Total,
		},
		"to_version": map[string]interface{}{
			"version":  toVersion,
			"results":  responseTo.Results,
			"total":    responseTo.Total,
		},
		"changes": s.compareDocuments(responseFrom.Results, responseTo.Results),
	}

	comparisonJSON, err := json.MarshalIndent(comparison, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal comparison: %v", err)), nil
	}

	return mcp.NewToolResultText(string(comparisonJSON)), nil
}

func (s *TalosDocMCPServer) handleGetPlatformDocs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	platform, err := request.RequireString("platform")
	if err != nil {
		return mcp.NewToolResultError("platform is required"), nil
	}

	version := request.GetString("version", "")

	// Search for platform-specific docs
	searchReq := &SearchRequest{
		Query:    platform,
		Platform: platform,
		Version:  version,
		Limit:    20,
	}

	response, err := s.searchEngine.Search(ctx, searchReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}

	result := map[string]interface{}{
		"platform": platform,
		"version":  version,
		"total":    response.Total,
		"results":  response.Results,
	}

	resultJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (s *TalosDocMCPServer) handleGetReleaseNotes(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Filter for latest version content
	taxonomy := s.searchEngine.GetTaxonomy()
	latestVersion := s.getLatestVersion(taxonomy.Versions)

	// Search for release notes and what's new in the latest version
	searchReq := &SearchRequest{
		Query:   "what's new release notes",
		Version: latestVersion,
		Limit:   10,
	}

	response, err := s.searchEngine.Search(ctx, searchReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}

	result := map[string]interface{}{
		"latest_version": latestVersion,
		"release_notes":  response.Results,
		"all_versions":   s.getSortedVersions(taxonomy.Versions),
	}

	resultJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (s *TalosDocMCPServer) handleSyncDocumentation(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log.Printf("Manual documentation sync requested")

	// Try to pull latest changes
	if err := s.fetcher.ForceSync(); err != nil {
		log.Printf("Failed to sync repository: %v", err)
		return mcp.NewToolResultError(fmt.Sprintf("Failed to sync repository: %v", err)), nil
	}

	// Reload navigation and documents
	nav, err := s.fetcher.GetNavigation()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get navigation: %v", err)), nil
	}

	documents, err := s.fetcher.ExtractDocuments(nav)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to extract documents: %v", err)), nil
	}

	// Reindex
	if err := s.searchEngine.IndexDocuments(documents); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to reindex documents: %v", err)), nil
	}

	result := map[string]interface{}{
		"status":    "success",
		"message":   "Documentation synced successfully",
		"documents": len(documents),
		"timestamp": time.Now().Format(time.RFC3339),
	}

	resultJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to marshal result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (s *TalosDocMCPServer) compareDocuments(fromResults, toResults []*SearchResult) []string {
	var changes []string

	fromTitles := make(map[string]bool)
	for _, result := range fromResults {
		fromTitles[result.Document.Title] = true
	}

	toTitles := make(map[string]bool)
	for _, result := range toResults {
		toTitles[result.Document.Title] = true
	}

	// Find new content
	for title := range toTitles {
		if !fromTitles[title] {
			changes = append(changes, fmt.Sprintf("NEW: %s", title))
		}
	}

	// Find removed content
	for title := range fromTitles {
		if !toTitles[title] {
			changes = append(changes, fmt.Sprintf("REMOVED: %s", title))
		}
	}

	return changes
}

func (s *TalosDocMCPServer) getLatestVersion(versions map[string]bool) string {
	if len(versions) == 0 {
		return "latest"
	}
	latest := ""
	for version := range versions {
		if version > latest {
			latest = version
		}
	}
	return latest
}

func (s *TalosDocMCPServer) getSortedVersions(versions map[string]bool) []string {
	var sorted []string
	for version := range versions {
		sorted = append(sorted, version)
	}
	// Simple version sorting (would need more sophisticated for real semantic versions)
	return sorted
}

func (s *TalosDocMCPServer) Start() error {
	// Start serving MCP immediately - don't block on index initialization
	// The index will be initialized in the background after MCP handshake completes
	log.Printf("==> Starting MCP Server on stdio...")

	// Initialize documents in background (after server starts listening)
	go func() {
		log.Printf("Initializing documentation index in background...")
		if err := s.initializeDocuments(); err != nil {
			log.Printf("WARNING: Failed to initialize documents: %v", err)
			log.Printf("Continuing with empty index - will retry on first search...")
		} else {
			log.Printf("==> Index initialized successfully!")
		}
	}()

	return server.ServeStdio(s.mcpServer)
}

func (s *TalosDocMCPServer) initializeDocuments() error {
	// Check if index already has documents
	docCount, err := s.searchEngine.activeIndex.DocCount()
	if err == nil && docCount > 0 {
		log.Printf("Using existing index with %d documents (skipping rebuild)", docCount)
		return nil
	}

	// No existing documents, need to build index
	log.Printf("Building fresh index...")

	// Get navigation and extract documents
	nav, err := s.fetcher.GetNavigation()
	if err != nil {
		return fmt.Errorf("failed to get navigation: %w", err)
	}

	documents, err := s.fetcher.ExtractDocuments(nav)
	if err != nil {
		return fmt.Errorf("failed to extract documents: %w", err)
	}

	if len(documents) == 0 {
		return fmt.Errorf("no documents found")
	}

	// Index documents
	if err := s.searchEngine.IndexDocuments(documents); err != nil {
		return fmt.Errorf("failed to index documents: %w", err)
	}

	log.Printf("Initialized with %d documents", len(documents))
	return nil
}

func (s *TalosDocMCPServer) Stop() {
	s.fetcher.Stop()
}