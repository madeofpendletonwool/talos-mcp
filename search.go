package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blevesearch/bleve/v2"
	index "github.com/blevesearch/bleve_index_api"
)

type SearchEngine struct {
	activeIndex   bleve.Index
	stagingIndex  bleve.Index
	indexPath     string
	mu            sync.RWMutex
	documents     map[string]*Document
	taxonomy      *ContentTaxonomy
	maxResults    int
	snippetLength int
}

type ContentTaxonomy struct {
	Versions   map[string]bool   `json:"versions"`
	Sections   map[string]bool   `json:"sections"`
	Platforms  map[string]bool   `json:"platforms"`
	Tags       map[string]bool   `json:"tags"`
}

type SearchRequest struct {
	Query     string   `json:"query"`
	Version   string   `json:"version,omitempty"`
	Section   string   `json:"section,omitempty"`
	Platform  string   `json:"platform,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Limit     int      `json:"limit,omitempty"`
}

type SearchResponse struct {
	Results   []*SearchResult `json:"results"`
	Total     int             `json:"total"`
	Query     string          `json:"query"`
	Duration  time.Duration   `json:"duration"`
}

func NewSearchEngine(indexPath string, maxResults, snippetLength int) (*SearchEngine, error) {
	se := &SearchEngine{
		indexPath:     indexPath,
		documents:     make(map[string]*Document),
		taxonomy:      &ContentTaxonomy{
			Versions:  make(map[string]bool),
			Sections:  make(map[string]bool),
			Platforms: make(map[string]bool),
			Tags:      make(map[string]bool),
		},
		maxResults:    maxResults,
		snippetLength: snippetLength,
	}

	// Create parent directory if it doesn't exist
	if err := os.MkdirAll(indexPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create index directory: %w", err)
	}

	// Open indices immediately - MUST happen before stdio transport starts
	log.Printf("Opening bleve indices...")
	activeIndexPath := filepath.Join(indexPath, "active")

	// Try to open with read-only config to avoid file locking issues
	config := map[string]interface{}{
		"read_only": true,
	}
	activeIndex, err := bleve.OpenUsing(activeIndexPath, config)
	if err == bleve.ErrorIndexPathDoesNotExist {
		log.Printf("No existing index found, creating empty index")
		activeIndex, err = bleve.New(activeIndexPath, bleve.NewIndexMapping())
		if err != nil {
			return nil, fmt.Errorf("failed to create search index: %w", err)
		}
	} else if err != nil {
		log.Printf("Failed to open in read-only mode, trying regular open: %v", err)
		// Fallback to regular open
		activeIndex, err = bleve.Open(activeIndexPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open existing index: %w", err)
		}
	}

	docCount, _ := activeIndex.DocCount()
	log.Printf("Opened existing index with %d documents", docCount)
	se.activeIndex = activeIndex

	// Create staging index
	stagingPath := filepath.Join(indexPath, "staging")
	os.RemoveAll(stagingPath) // Clean up any old staging
	stagingIndex, err := bleve.New(stagingPath, bleve.NewIndexMapping())
	if err != nil {
		return nil, fmt.Errorf("failed to create staging index: %w", err)
	}
	se.stagingIndex = stagingIndex

	log.Printf("SearchEngine ready")
	return se, nil
}

func (se *SearchEngine) EnsureIndicesOpen() error {
	// Quick check with read lock first (avoid contention)
	se.mu.RLock()
	if se.activeIndex != nil && se.stagingIndex != nil {
		se.mu.RUnlock()
		return nil
	}
	se.mu.RUnlock()

	// Need to open indices - acquire write lock
	se.mu.Lock()
	defer se.mu.Unlock()

	// Double-check after acquiring write lock (another goroutine may have opened them)
	if se.activeIndex != nil && se.stagingIndex != nil {
		return nil
	}

	log.Printf("Opening bleve indices...")

	// Open or create the active index
	activeIndexPath := filepath.Join(se.indexPath, "active")
	if se.activeIndex == nil {
		activeIndex, err := bleve.Open(activeIndexPath)
		if err == bleve.ErrorIndexPathDoesNotExist {
			log.Printf("No existing index found, will create on first indexing")
			activeIndex, err = bleve.New(activeIndexPath, bleve.NewIndexMapping())
			if err != nil {
				return fmt.Errorf("failed to create search index: %w", err)
			}
		} else if err != nil {
			// Index might be corrupted, try to recover
			log.Printf("Warning: failed to open existing index: %v", err)
			log.Printf("Removing corrupted index and creating new one...")
			if err := os.RemoveAll(activeIndexPath); err != nil {
				return fmt.Errorf("failed to remove corrupted index: %w", err)
			}
			activeIndex, err = bleve.New(activeIndexPath, bleve.NewIndexMapping())
			if err != nil {
				return fmt.Errorf("failed to create search index: %w", err)
			}
		} else {
			log.Printf("Opened existing search index at %s", activeIndexPath)
			// Get document count from the index
			docCount, err := activeIndex.DocCount()
			if err == nil && docCount > 0 {
				log.Printf("Found %d documents in existing index", docCount)
			}
		}
		se.activeIndex = activeIndex
	}

	// Create staging index (don't fail if it exists)
	if se.stagingIndex == nil {
		stagingPath := filepath.Join(se.indexPath, "staging")
		if err := os.RemoveAll(stagingPath); err != nil {
			log.Printf("Warning: failed to remove staging index: %v", err)
		}

		stagingIndex, err := bleve.New(stagingPath, bleve.NewIndexMapping())
		if err != nil {
			return fmt.Errorf("failed to create staging index: %w", err)
		}
		se.stagingIndex = stagingIndex
	}

	log.Printf("Bleve indices opened successfully")
	return nil
}

func (se *SearchEngine) IndexDocuments(documents []*Document) error {
	se.mu.Lock()
	defer se.mu.Unlock()

	start := time.Now()
	log.Printf("Starting indexing of %d documents...", len(documents))

	// Clear staging index by creating a new one
	stagingPath := filepath.Join(se.indexPath, "staging")
	if err := os.RemoveAll(stagingPath); err != nil {
		log.Printf("Warning: failed to clear staging index: %v", err)
	}

	// Close existing staging index if it exists
	if se.stagingIndex != nil {
		se.stagingIndex.Close()
	}

	// Create staging directory structure
	if err := os.MkdirAll(stagingPath, 0755); err != nil {
		return fmt.Errorf("failed to create staging directory: %w", err)
	}

	newStagingIndex, err := bleve.New(stagingPath, bleve.NewIndexMapping())
	if err != nil {
		return fmt.Errorf("failed to create new staging index: %w", err)
	}
	se.stagingIndex = newStagingIndex

	// Index documents in staging
	indexed := 0
	for i, doc := range documents {
		if err := se.indexDocument(newStagingIndex, doc); err != nil {
			log.Printf("Error indexing document %s: %v", doc.ID, err)
			continue
		}

		// Update taxonomy
		se.updateTaxonomy(doc)

		// Store in memory map
		se.documents[doc.ID] = doc
		indexed++

		// Progress update every 100 documents
		if (i+1)%100 == 0 {
			log.Printf("  Indexed %d/%d documents...", i+1, len(documents))
		}
	}

	log.Printf("Indexing complete: %d documents indexed", indexed)
	log.Printf("Performing atomic index swap...")

	// Atomic swap
	if err := se.atomicSwap(); err != nil {
		return fmt.Errorf("failed to atomic swap indices: %w", err)
	}

	log.Printf("Index ready! Total time: %v", time.Since(start))
	return nil
}

func (se *SearchEngine) indexDocument(index bleve.Index, doc *Document) error {
	// Prepare document for indexing
	indexDoc := map[string]interface{}{
		"title":        doc.Title,
		"content":      doc.Content,
		"version":      doc.Version,
		"section":      doc.Section,
		"platform":     doc.Platform,
		"tags":         strings.Join(doc.Tags, " "),
		"last_updated": doc.LastUpdated,
	}

	return index.Index(doc.ID, indexDoc)
}

func (se *SearchEngine) updateTaxonomy(doc *Document) {
	if doc.Version != "" {
		se.taxonomy.Versions[doc.Version] = true
	}
	if doc.Section != "" {
		se.taxonomy.Sections[doc.Section] = true
	}
	if doc.Platform != "" {
		se.taxonomy.Platforms[doc.Platform] = true
	}
	for _, tag := range doc.Tags {
		se.taxonomy.Tags[tag] = true
	}
}

func (se *SearchEngine) atomicSwap() error {
	log.Printf("  Closing indices...")
	// Close BOTH indices before filesystem operations
	if se.activeIndex != nil {
		se.activeIndex.Close()
	}
	if se.stagingIndex != nil {
		se.stagingIndex.Close()
	}

	// Define paths
	activeIndexPath := filepath.Join(se.indexPath, "active")
	stagingPath := filepath.Join(se.indexPath, "staging")
	backupPath := filepath.Join(se.indexPath, "backup")

	log.Printf("  Backing up old index...")
	// Remove old backup if it exists
	if err := os.RemoveAll(backupPath); err != nil {
		log.Printf("Warning: failed to remove old backup index: %v", err)
	}

	// Backup current active index
	if err := os.Rename(activeIndexPath, backupPath); err != nil {
		log.Printf("Warning: failed to backup active index: %v", err)
	}

	log.Printf("  Swapping staging to active...")
	// Move staging to active
	if err := os.Rename(stagingPath, activeIndexPath); err != nil {
		return fmt.Errorf("failed to move staging index to active: %w", err)
	}

	log.Printf("  Reopening active index...")
	// Reopen active index
	activeIndex, err := bleve.Open(activeIndexPath)
	if err != nil {
		return fmt.Errorf("failed to reopen active index: %w", err)
	}
	se.activeIndex = activeIndex

	log.Printf("  Creating new staging index...")
	// Create new staging index
	newStagingIndex, err := bleve.New(stagingPath, bleve.NewIndexMapping())
	if err != nil {
		return fmt.Errorf("failed to create new staging index: %w", err)
	}
	se.stagingIndex = newStagingIndex

	log.Printf("  Swap complete!")
	return nil
}

func (se *SearchEngine) Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
	log.Printf("DEBUG: Search called for query: %s", req.Query)
	se.mu.RLock()
	log.Printf("DEBUG: Acquired read lock")
	defer func() {
		se.mu.RUnlock()
		log.Printf("DEBUG: Released read lock")
	}()

	start := time.Now()

	// Build simple query
	query := bleve.NewMatchQuery(req.Query)
	log.Printf("DEBUG: Built match query")
	
	// Create search request with strict limits to avoid buffer overflow
	// Limit to max 5 results to keep JSON-RPC response size manageable
	limit := req.Limit
	if limit == 0 || limit > 5 {
		limit = 5
	}
	searchReq := bleve.NewSearchRequest(query)
	searchReq.Size = limit
	searchReq.From = 0

	// Execute search
	log.Printf("DEBUG: About to execute search in context")
	searchResult, err := se.activeIndex.SearchInContext(ctx, searchReq)
	if err != nil {
		log.Printf("DEBUG: Search failed: %v", err)
		return nil, fmt.Errorf("search failed: %w", err)
	}
	log.Printf("DEBUG: Search complete, found %d hits", len(searchResult.Hits))

	// Convert results - retrieve document fields from index
	results := make([]*SearchResult, 0, len(searchResult.Hits))
	for _, hit := range searchResult.Hits {
		// Try to get from memory first (faster)
		doc, exists := se.documents[hit.ID]
		if exists {
			// Limit content size for in-memory documents too (10KB per doc)
			if len(doc.Content) > 10000 {
				// Create a copy with truncated content
				doc = &Document{
					ID:          doc.ID,
					Title:       doc.Title,
					Content:     doc.Content[:10000] + "\n\n[Content truncated]",
					Path:        doc.Path,
					Version:     doc.Version,
					Section:     doc.Section,
					Platform:    doc.Platform,
					Tags:        doc.Tags,
					LastUpdated: doc.LastUpdated,
					Metadata:    doc.Metadata,
				}
			}
		}
		if !exists {
			// Document not in memory, retrieve from index
			log.Printf("DEBUG: Retrieving document %s from index", hit.ID)
			storedDoc, err := se.activeIndex.Document(hit.ID)
			if err != nil {
				log.Printf("Warning: failed to retrieve document %s from index: %v", hit.ID, err)
				continue
			}
			log.Printf("DEBUG: Retrieved document %s", hit.ID)

			// Reconstruct document from stored fields
			doc = &Document{
				ID: hit.ID,
			}
			storedDoc.VisitFields(func(field index.Field) {
				switch field.Name() {
				case "title":
					doc.Title = string(field.Value())
				case "content":
					// Limit content to first 10KB to avoid buffer overflow
					content := string(field.Value())
					if len(content) > 10000 {
						doc.Content = content[:10000] + "\n\n[Content truncated]"
					} else {
						doc.Content = content
					}
				case "version":
					doc.Version = string(field.Value())
				case "section":
					doc.Section = string(field.Value())
				case "platform":
					doc.Platform = string(field.Value())
				case "tags":
					doc.Tags = strings.Split(string(field.Value()), " ")
				}
			})
		}

		// Apply manual filters
		if req.Version != "" && doc.Version != req.Version {
			continue
		}
		if req.Section != "" && doc.Section != req.Section {
			continue
		}
		if req.Platform != "" && doc.Platform != req.Platform {
			continue
		}

		result := &SearchResult{
			Document: doc,
			Score:    hit.Score,
			Snippet:  se.extractSnippet(doc.Content),
			Context:  se.extractContext(doc.Content),
		}
		results = append(results, result)
	}

	return &SearchResponse{
		Results:  results,
		Total:    int(searchResult.Total),
		Query:    req.Query,
		Duration: time.Since(start),
	}, nil
}

func (se *SearchEngine) extractSnippet(content string) string {
	// Simple snippet extraction - take first few sentences
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") && len(line) > 50 {
			if len(line) > se.snippetLength {
				return line[:se.snippetLength] + "..."
			}
			return line
		}
	}
	return ""
}

func (se *SearchEngine) extractContext(content string) string {
	// For now, return first paragraph or first few sentences
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") && len(line) > 50 {
			if len(line) > 200 {
				return line[:200] + "..."
			}
			return line
		}
	}
	return ""
}

func (se *SearchEngine) GetDocument(id string) (*Document, bool) {
	se.mu.RLock()
	defer se.mu.RUnlock()
	
	doc, exists := se.documents[id]
	return doc, exists
}

func (se *SearchEngine) GetTaxonomy() *ContentTaxonomy {
	se.mu.RLock()
	defer se.mu.RUnlock()
	
	// Return a copy
	taxonomy := &ContentTaxonomy{
		Versions:  make(map[string]bool),
		Sections:  make(map[string]bool),
		Platforms: make(map[string]bool),
		Tags:      make(map[string]bool),
	}
	
	for k, v := range se.taxonomy.Versions {
		taxonomy.Versions[k] = v
	}
	for k, v := range se.taxonomy.Sections {
		taxonomy.Sections[k] = v
	}
	for k, v := range se.taxonomy.Platforms {
		taxonomy.Platforms[k] = v
	}
	for k, v := range se.taxonomy.Tags {
		taxonomy.Tags[k] = v
	}
	
	return taxonomy
}

func (se *SearchEngine) GetStats() map[string]interface{} {
	se.mu.RLock()
	defer se.mu.RUnlock()

	stats := map[string]interface{}{
		"total_documents": len(se.documents),
		"versions":        len(se.taxonomy.Versions),
		"sections":        len(se.taxonomy.Sections),
		"platforms":       len(se.taxonomy.Platforms),
		"tags":            len(se.taxonomy.Tags),
	}

	// Get index stats if available
	if indexStats := se.activeIndex.Stats(); indexStats != nil {
		stats["index_stats"] = indexStats
	}

	return stats
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}