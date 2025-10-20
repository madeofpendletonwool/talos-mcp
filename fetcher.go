package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

type DocumentationFetcher struct {
	repoURL      string
	localPath    string
	branch       string
	gitRepo      *git.Repository
	lastSync     time.Time
	syncMode     SyncMode
	webhookChan  chan WebhookEvent
	mu           sync.RWMutex
	stopChan     chan struct{}
}

func NewDocumentationFetcher(repoURL, localPath, branch string) (*DocumentationFetcher, error) {
	df := &DocumentationFetcher{
		repoURL:     repoURL,
		localPath:   localPath,
		branch:      branch,
		syncMode:    Hybrid,
		webhookChan: make(chan WebhookEvent, 100),
		stopChan:    make(chan struct{}),
	}

	if err := df.initRepository(); err != nil {
		return nil, fmt.Errorf("failed to initialize repository: %w", err)
	}

	// Don't start background sync - it interferes with stdio
	// TODO: Re-enable background sync with proper coordination
	// go df.backgroundSync()

	return df, nil
}

func (df *DocumentationFetcher) initRepository() error {
	// Check if repo already exists
	if _, err := os.Stat(df.localPath); os.IsNotExist(err) {
		// Clone fresh repository
		log.Printf("Cloning repository from %s to %s (this may take 30-60 seconds)...", df.repoURL, df.localPath)

		cloneOpts := &git.CloneOptions{
			URL:           df.repoURL,
			SingleBranch:  true,
			ReferenceName: plumbing.NewBranchReferenceName(df.branch),
			Depth:         1,
			Tags:          git.NoTags,
			Progress:      os.Stderr, // Show clone progress
		}

		repo, err := git.PlainClone(df.localPath, false, cloneOpts)
		if err != nil {
			return fmt.Errorf("failed to clone repository: %w", err)
		}
		log.Printf("Repository cloned successfully")
		df.gitRepo = repo
	} else {
		log.Printf("Repository already exists at %s, skipping git operations", df.localPath)
		// Skip opening the git repo - just use the files directly
		// This avoids hanging when run under MCP stdio transport
		df.gitRepo = nil
	}

	df.lastSync = time.Now()
	return nil
}

func (df *DocumentationFetcher) pullLatest() error {
	wt, err := df.gitRepo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	// Fetch latest changes
	if err := df.gitRepo.Fetch(&git.FetchOptions{
		RemoteURL: df.repoURL,
		Depth:     1,
		Tags:      git.NoTags,
	}); err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("failed to fetch: %w", err)
	}

	// Pull changes
	if err := wt.Pull(&git.PullOptions{
		SingleBranch: true,
	}); err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("failed to pull: %w", err)
	}

	return nil
}

func (df *DocumentationFetcher) backgroundSync() {
	ticker := time.NewTicker(5 * time.Minute) // Adaptive polling
	defer ticker.Stop()

	for {
		select {
		case <-df.stopChan:
			return
		case event := <-df.webhookChan:
			if err := df.handleWebhookEvent(event); err != nil {
				log.Printf("Error handling webhook event: %v", err)
			}
		case <-ticker.C:
			if df.syncMode == Polling || df.syncMode == Hybrid {
				if err := df.pollForUpdates(); err != nil {
					log.Printf("Error during polling: %v", err)
				}
			}
		}
	}
}

func (df *DocumentationFetcher) handleWebhookEvent(event WebhookEvent) error {
	log.Printf("Processing webhook event: %s from %s", event.Type, event.Repository)
	
	if event.Branch != df.branch {
		log.Printf("Ignoring webhook for branch %s (watching %s)", event.Branch, df.branch)
		return nil
	}

	return df.syncWithCommit(event.CommitSHA)
}

func (df *DocumentationFetcher) pollForUpdates() error {
	df.mu.RLock()
	lastSync := df.lastSync
	df.mu.RUnlock()

	// Check if we need to poll (adaptive logic)
	if time.Since(lastSync) < 5*time.Minute {
		return nil
	}

	// Check for new commits
	remote, err := df.gitRepo.Remote("origin")
	if err != nil {
		return fmt.Errorf("failed to get remote: %w", err)
	}

	if err := remote.Fetch(&git.FetchOptions{
		Depth: 1,
		Tags:  git.NoTags,
	}); err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("failed to fetch during poll: %w", err)
	}

	// Check if HEAD changed
	ref, err := df.gitRepo.Head()
	if err != nil {
		return fmt.Errorf("failed to get HEAD: %w", err)
	}

	currentCommit := ref.Hash().String()
	
	// If we have new changes, sync
	if err := df.pullLatest(); err == nil {
		log.Printf("Polled and found new changes, updating...")
		return df.syncWithCommit(currentCommit)
	}

	return nil
}

func (df *DocumentationFetcher) syncWithCommit(commitSHA string) error {
	df.mu.Lock()
	defer df.mu.Unlock()

	if err := df.pullLatest(); err != nil {
		return fmt.Errorf("failed to sync with commit %s: %w", commitSHA, err)
	}

	df.lastSync = time.Now()
	log.Printf("Successfully synced to commit %s", commitSHA)
	return nil
}

func (df *DocumentationFetcher) GetNavigation() (*DocsNavigation, error) {
	docsPath := filepath.Join(df.localPath, "public", "docs.json")
	
	file, err := os.Open(docsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open docs.json: %w", err)
	}
	defer file.Close()

	var nav DocsNavigation
	if err := json.NewDecoder(file).Decode(&nav); err != nil {
		return nil, fmt.Errorf("failed to decode docs.json: %w", err)
	}

	return &nav, nil
}

func (df *DocumentationFetcher) ExtractDocuments(nav *DocsNavigation) ([]*Document, error) {
	var documents []*Document
	startTime := time.Now()

	log.Printf("Starting document extraction (Talos docs only)...")

	for _, tab := range nav.Navigation.Tabs {
		// Only process the "Talos" tab
		if tab.Tab != "Talos" {
			log.Printf("Skipping tab: %s (only processing Talos docs)", tab.Tab)
			continue
		}

		log.Printf("Processing Talos tab with %d versions", len(tab.Versions))
		for _, version := range tab.Versions {
			versionStart := time.Now()
			tabDocs := df.extractDocumentsFromVersion(tab.Tab, version.Version, version.Groups)
			documents = append(documents, tabDocs...)
			log.Printf("  %s: extracted %d documents in %v", version.Version, len(tabDocs), time.Since(versionStart))
		}
	}

	log.Printf("Extraction complete: %d documents in %v", len(documents), time.Since(startTime))
	return documents, nil
}

func (df *DocumentationFetcher) extractDocumentsFromVersion(tab, version string, groups []Group) []*Document {
	var documents []*Document

	for _, group := range groups {
		groupDocs := df.extractDocumentsFromGroup(tab, version, group.Group, "", group.Pages)
		documents = append(documents, groupDocs...)
	}

	return documents
}

func (df *DocumentationFetcher) extractDocumentsFromGroup(tab, version, groupName, platform string, pages []PageItem) []*Document {
	var documents []*Document

	for _, page := range pages {
		switch p := page.(type) {
		case string:
			// Direct page reference
			doc := df.extractDocument(tab, version, groupName, platform, p)
			if doc != nil {
				documents = append(documents, doc)
			}
		case map[string]interface{}:
			// Nested group
			if nestedGroup, ok := p["group"].(string); ok {
				if nestedPages, ok := p["pages"].([]interface{}); ok {
					// Convert pages to PageItem slice
					var pageItems []PageItem
					for _, np := range nestedPages {
						pageItems = append(pageItems, np)
					}
					nestedDocs := df.extractDocumentsFromGroup(tab, version, nestedGroup, platform, pageItems)
					documents = append(documents, nestedDocs...)
				}
			}
		case Page:
			doc := df.extractDocument(tab, version, groupName, platform, p.Page)
			if doc != nil {
				documents = append(documents, doc)
			}
		case NestedGroup:
			nestedDocs := df.extractDocumentsFromGroup(tab, version, p.Group, platform, p.Pages)
			documents = append(documents, nestedDocs...)
		}
	}

	return documents
}

func (df *DocumentationFetcher) extractDocument(tab, version, groupName, platform, pagePath string) *Document {
	// Convert page path to file path
	filePath := filepath.Join(df.localPath, "public", pagePath+".mdx")
	
	// Try .md extension if .mdx doesn't exist
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		filePath = filepath.Join(df.localPath, "public", pagePath+".md")
	}

	// Read file content
	content, err := os.ReadFile(filePath)
	if err != nil {
		// File doesn't exist or can't be read - skip silently
		return nil
	}

	// Skip empty files
	if len(content) == 0 {
		return nil
	}

	// Extract title from content or path
	title := df.extractTitle(string(content), pagePath)

	// Create document ID
	id := fmt.Sprintf("%s-%s-%s", version, strings.ToLower(groupName), strings.ToLower(filepath.Base(pagePath)))

	// Determine platform from path if not explicitly set
	if platform == "" {
		platform = df.extractPlatformFromPath(pagePath)
	}

	doc := &Document{
		ID:          id,
		Title:       title,
		Content:     string(content),
		Path:        pagePath,
		Version:     version,
		Section:     groupName,
		Platform:    platform,
		Tags:        df.extractTags(string(content), pagePath),
		LastUpdated: df.getFileModTime(filePath),
		Metadata: map[string]interface{}{
			"tab":      tab,
			"file_path": filePath,
		},
	}

	return doc
}

func (df *DocumentationFetcher) extractTitle(content, pagePath string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	
	// Fallback to filename
	return filepath.Base(pagePath)
}

func (df *DocumentationFetcher) extractPlatformFromPath(pagePath string) string {
	lowerPath := strings.ToLower(pagePath)
	platforms := []string{"aws", "azure", "gcp", "digitalocean", "hetzner", "vultr", "exoscale", 
		"scaleway", "upcloud", "oracle", "akamai", "cloudstack", "nocloud", "openstack",
		"equinix-metal", "proxmox", "vmware", "kvm", "hyper-v", "xen", "docker", "qemu", "virtualbox",
		"banana", "nanopi", "jetson", "orangepi", "pine64", "rock", "rpi"}
	
	for _, platform := range platforms {
		if strings.Contains(lowerPath, platform) {
			return platform
		}
	}
	
	return ""
}

func (df *DocumentationFetcher) extractTags(content, pagePath string) []string {
	var tags []string
	
	// Extract from path
	pathTags := strings.Split(strings.ToLower(pagePath), "/")
	tags = append(tags, pathTags...)
	
	// Extract common keywords
	keywords := []string{"installation", "configuration", "networking", "security", "upgrade", 
		"troubleshooting", "api", "cli", "kernel", "storage", "monitoring"}
	
	lowerContent := strings.ToLower(content)
	for _, keyword := range keywords {
		if strings.Contains(lowerContent, keyword) {
			tags = append(tags, keyword)
		}
	}
	
	// Remove duplicates and empty strings
	uniqueTags := make([]string, 0)
	seen := make(map[string]bool)
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag != "" && !seen[tag] {
			seen[tag] = true
			uniqueTags = append(uniqueTags, tag)
		}
	}
	
	return uniqueTags
}

func (df *DocumentationFetcher) getFileModTime(filePath string) time.Time {
	if info, err := os.Stat(filePath); err == nil {
		return info.ModTime()
	}
	return time.Now()
}

func (df *DocumentationFetcher) HandleWebhook(event WebhookEvent) {
	select {
	case df.webhookChan <- event:
	default:
		log.Printf("Webhook channel full, dropping event")
	}
}

func (df *DocumentationFetcher) Stop() {
	close(df.stopChan)
}

func (df *DocumentationFetcher) ForceSync() error {
	return df.pollForUpdates()
}