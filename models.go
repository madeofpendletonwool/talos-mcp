package main

import (
	"time"
)

type Document struct {
	ID          string                 `json:"id"`
	Title       string                 `json:"title"`
	Content     string                 `json:"content"`
	Path        string                 `json:"path"`
	Version     string                 `json:"version"`
	Section     string                 `json:"section"`
	Platform    string                 `json:"platform,omitempty"`
	Tags        []string               `json:"tags"`
	LastUpdated time.Time              `json:"last_updated"`
	Metadata    map[string]interface{} `json:"metadata"`
}

type SearchResult struct {
	Document *Document `json:"document"`
	Score    float64   `json:"score"`
	Snippet  string    `json:"snippet"`
	Context  string    `json:"context"`
}

type DocsNavigation struct {
	Schema   string            `json:"$schema"`
	Theme    string            `json:"theme"`
	Name     string            `json:"name"`
	Navigation NavigationTabs  `json:"navigation"`
}

type NavigationTabs struct {
	Tabs []Tab `json:"tabs"`
}

type Tab struct {
	Tab      string   `json:"tab"`
	Icon     string   `json:"icon"`
	Versions []Version `json:"versions"`
}

type Version struct {
	Version string  `json:"version"`
	Groups  []Group `json:"groups"`
}

type Group struct {
	Group  string      `json:"group"`
	Pages  []PageItem  `json:"pages"`
}

type PageItem interface{}

type Page struct {
	Page string `json:"page"`
}

type NestedGroup struct {
	Group string     `json:"group"`
	Pages []PageItem `json:"pages"`
}

type SyncMode int

const (
	Polling SyncMode = iota
	Webhook
	Hybrid
)

type WebhookEvent struct {
	Type      string    `json:"type"`     // push, release, etc.
	CommitSHA string    `json:"commit_sha"`
	Timestamp time.Time `json:"timestamp"`
	Branch    string    `json:"branch"`
	Repository string   `json:"repository"`
}

type Config struct {
	Server struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	} `yaml:"server"`
	
	Repository struct {
		URL    string `yaml:"url"`
		Branch string `yaml:"branch"`
	} `yaml:"repository"`
	
	Sync struct {
		Mode string `yaml:"mode"`
		Webhook struct {
			Secret   string `yaml:"secret"`
			Endpoint string `yaml:"endpoint"`
		} `yaml:"webhook"`
		Polling struct {
			Interval  string `yaml:"interval"`
			BackoffMax string `yaml:"backoff_max"`
		} `yaml:"polling"`
		HealthCheck struct {
			StaleThreshold string `yaml:"stale_threshold"`
			MaxAge         string `yaml:"max_age"`
		} `yaml:"health_check"`
	} `yaml:"sync"`
	
	Search struct {
		IndexPath      string `yaml:"index_path"`
		MaxResults     int    `yaml:"max_results"`
		SnippetLength  int    `yaml:"snippet_length"`
	} `yaml:"search"`
	
	Cache struct {
		TTL    string `yaml:"ttl"`
		MaxSize string `yaml:"max_size"`
	} `yaml:"cache"`
	
	Logging struct {
		Level  string `yaml:"level"`
		Format string `yaml:"format"`
	} `yaml:"logging"`
	
	Monitoring struct {
		MetricsEnabled bool `yaml:"metrics_enabled"`
		AlertsEnabled  bool `yaml:"alerts_enabled"`
	} `yaml:"monitoring"`
}