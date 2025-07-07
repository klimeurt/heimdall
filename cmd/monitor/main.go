package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
)

// MonitorConfig holds the monitor configuration
type MonitorConfig struct {
	RedisHost       string
	RedisPort       string
	RedisPassword   string
	RedisDB         int
	RefreshRate     time.Duration
	QueuePattern    string // Pattern to match queue names (e.g., "*_queue")
	SharedVolumeDir string
}

// QueueStats holds statistics for a Redis queue
type QueueStats struct {
	Name        string
	Length      int64
	Rate        float64 // items per minute
	LastLength  int64
	LastCheck   time.Time
	RecentItems []string
}

// FolderStats holds statistics for the shared volume folder
type FolderStats struct {
	TotalSize  int64
	RepoCount  int
	LastSize   int64
	LastCheck  time.Time
	GrowthRate float64 // MB per minute
}

// Monitor holds the monitor state
type Monitor struct {
	client      *redis.Client
	config      *MonitorConfig
	stats       map[string]*QueueStats
	folderStats *FolderStats
	statsMutex  sync.RWMutex
	startTime   time.Time
	knownQueues []string // Dynamically discovered queues
}

// ProcessedRepository represents a processed repository
type ProcessedRepository struct {
	Org          string `json:"org"`
	Name         string `json:"name"`
	ClonePath    string `json:"clone_path,omitempty"`
	ProcessedAt  string `json:"processed_at,omitempty"`
	ErrorMessage string `json:"error,omitempty"`
}

func main() {
	cfg := loadConfig()

	// Create Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", cfg.RedisHost, cfg.RedisPort),
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	defer redisClient.Close()

	// Test Redis connection
	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	// Create monitor
	monitor := &Monitor{
		client: redisClient,
		config: cfg,
		stats:  make(map[string]*QueueStats),
		folderStats: &FolderStats{
			LastCheck: time.Now(),
		},
		startTime:   time.Now(),
		knownQueues: []string{},
	}

	// Discover initial queues
	monitor.discoverQueues(ctx)

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start monitoring
	ticker := time.NewTicker(cfg.RefreshRate)
	defer ticker.Stop()

	// Initial display
	monitor.updateFolderStats()
	monitor.displayDashboard(ctx)

	// Monitor queues
	for {
		select {
		case <-sigChan:
			fmt.Println("\n\nShutting down monitor...")
			return
		case <-ticker.C:
			monitor.discoverQueues(ctx)
			monitor.updateStats(ctx)
			monitor.updateFolderStats()
			monitor.displayDashboard(ctx)
		}
	}
}

func loadConfig() *MonitorConfig {
	cfg := &MonitorConfig{
		RedisHost:       getEnv("REDIS_HOST", "localhost"),
		RedisPort:       getEnv("REDIS_PORT", "6379"),
		RedisPassword:   os.Getenv("REDIS_PASSWORD"),
		RefreshRate:     5 * time.Second,
		QueuePattern:    getEnv("QUEUE_PATTERN", "*_queue"),
		SharedVolumeDir: getEnv("SHARED_VOLUME_DIR", "./shared-repos"),
	}

	// Parse Redis DB
	if redisDB := os.Getenv("REDIS_DB"); redisDB != "" {
		db, err := strconv.Atoi(redisDB)
		if err != nil {
			log.Fatalf("Invalid REDIS_DB value: %v", err)
		}
		cfg.RedisDB = db
	}

	// Parse refresh rate
	if refreshRate := os.Getenv("REFRESH_RATE_SECONDS"); refreshRate != "" {
		rate, err := strconv.Atoi(refreshRate)
		if err != nil {
			log.Fatalf("Invalid REFRESH_RATE_SECONDS value: %v", err)
		}
		cfg.RefreshRate = time.Duration(rate) * time.Second
	}

	return cfg
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func (m *Monitor) discoverQueues(ctx context.Context) {
	m.statsMutex.Lock()
	defer m.statsMutex.Unlock()

	// Get all keys matching the pattern
	keys, err := m.client.Keys(ctx, m.config.QueuePattern).Result()
	if err != nil {
		log.Printf("Error discovering queues: %v", err)
		return
	}

	// Filter to only include lists (queues)
	newQueues := []string{}
	for _, key := range keys {
		keyType, err := m.client.Type(ctx, key).Result()
		if err != nil {
			continue
		}
		if keyType == "list" {
			newQueues = append(newQueues, key)

			// Initialize stats for new queues
			if _, exists := m.stats[key]; !exists {
				m.stats[key] = &QueueStats{
					Name:      key,
					LastCheck: time.Now(),
				}
			}
		}
	}

	// Remove stats for queues that no longer exist
	existingKeys := make(map[string]bool)
	for _, key := range newQueues {
		existingKeys[key] = true
	}

	for key := range m.stats {
		if !existingKeys[key] {
			delete(m.stats, key)
		}
	}

	// Sort queues for consistent display
	sort.Strings(newQueues)
	m.knownQueues = newQueues
}

func (m *Monitor) updateStats(ctx context.Context) {
	m.statsMutex.Lock()
	defer m.statsMutex.Unlock()

	for queueName, stat := range m.stats {
		// Get current length
		length, err := m.client.LLen(ctx, queueName).Result()
		if err != nil {
			log.Printf("Error getting length for queue %s: %v", queueName, err)
			continue
		}

		// Calculate rate
		now := time.Now()
		if stat.LastLength > 0 {
			timeDiff := now.Sub(stat.LastCheck).Minutes()
			if timeDiff > 0 {
				itemsDiff := float64(length - stat.LastLength)
				stat.Rate = itemsDiff / timeDiff
			}
		}

		stat.LastLength = length
		stat.Length = length
		stat.LastCheck = now

		// Get recent items (up to 3)
		stat.RecentItems = []string{}
		if length > 0 {
			items, err := m.client.LRange(ctx, queueName, 0, 2).Result()
			if err == nil {
				for _, item := range items {
					// Try to parse and format the item
					formattedItem := m.formatQueueItem(queueName, item)
					stat.RecentItems = append(stat.RecentItems, formattedItem)
				}
			}
		}
	}
}

func (m *Monitor) formatQueueItem(queueName string, item string) string {
	// Try to parse as JSON and extract key info
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(item), &data); err == nil {
		// Try to extract org/name pattern (common across all queues)
		if org, ok := data["org"].(string); ok {
			if name, ok := data["name"].(string); ok {
				return fmt.Sprintf("%s/%s", org, name)
			}
		}

		// Try other common patterns
		if repo, ok := data["repository"].(string); ok {
			return repo
		}
		if url, ok := data["url"].(string); ok {
			// Extract repo name from URL if possible
			parts := strings.Split(url, "/")
			if len(parts) >= 2 {
				return fmt.Sprintf("%s/%s", parts[len(parts)-2], parts[len(parts)-1])
			}
			return url
		}

		// For unknown structure, show first string field found
		for key, value := range data {
			if str, ok := value.(string); ok && str != "" {
				return fmt.Sprintf("%s: %s", key, str)
			}
		}
	}

	// If we can't parse, return truncated version
	if len(item) > 50 {
		return item[:47] + "..."
	}
	return item
}

func (m *Monitor) displayDashboard(ctx context.Context) {
	m.statsMutex.RLock()
	defer m.statsMutex.RUnlock()

	// Clear screen
	fmt.Print("\033[H\033[2J")

	// Header
	fmt.Println("╔════════════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                          Heimdall Pipeline Monitor                         ║")
	fmt.Println("╚════════════════════════════════════════════════════════════════════════════╝")
	fmt.Printf("  Connected to Redis at %s:%s | Uptime: %s\n", m.config.RedisHost, m.config.RedisPort, m.formatDuration(time.Since(m.startTime)))
	fmt.Printf("  Last updated: %s | Refresh: %v\n", time.Now().Format("15:04:05"), m.config.RefreshRate)
	fmt.Println()

	// Show discovered queues
	fmt.Printf("  Discovered Queues: %d queues matching pattern '%s'\n", len(m.knownQueues), m.config.QueuePattern)
	fmt.Println()

	// Queue details
	fmt.Println("  Queue Details:")
	fmt.Println("  ┌────────────────────┬──────────┬────────────┬─────────────────────────────┐")
	fmt.Println("  │ Queue Name         │  Items   │ Rate (i/m) │ Recent Items                │")
	fmt.Println("  ├────────────────────┼──────────┼────────────┼─────────────────────────────┤")

	totalItems := int64(0)
	activeQueues := 0

	// Display each queue
	for _, queueName := range m.knownQueues {
		stat := m.stats[queueName]
		totalItems += stat.Length
		if stat.Length > 0 {
			activeQueues++
		}

		// Format recent items
		recentItemsStr := "-"
		if len(stat.RecentItems) > 0 {
			recentItemsStr = stat.RecentItems[0]
			if len(recentItemsStr) > 25 {
				recentItemsStr = recentItemsStr[:22] + "..."
			}
		}

		// Format rate
		rateStr := "0.0"
		if stat.Rate != 0 {
			rateStr = fmt.Sprintf("%+.1f", stat.Rate)
		}

		fmt.Printf("  │ %-18s │ %8d │ %10s │ %-27s │\n",
			queueName,
			stat.Length,
			rateStr,
			recentItemsStr)
	}

	fmt.Println("  └────────────────────┴──────────┴────────────┴─────────────────────────────┘")
	fmt.Println()

	// Shared Volume Stats
	fmt.Println("  Shared Volume Statistics:")
	fmt.Println("  ┌────────────────────┬──────────────┬────────────┬────────────────────────┐")
	fmt.Println("  │ Metric             │ Value        │ Growth     │ Details                │")
	fmt.Println("  ├────────────────────┼──────────────┼────────────┼────────────────────────┤")

	// Format growth rate
	growthStr := "0.0 MB/m"
	if m.folderStats.GrowthRate != 0 {
		growthStr = fmt.Sprintf("%+.1f MB/m", m.folderStats.GrowthRate)
	}

	// Format details
	details := fmt.Sprintf("%d repositories", m.folderStats.RepoCount)
	if len(details) > 20 {
		details = details[:17] + "..."
	}

	fmt.Printf("  │ %-18s │ %12s │ %10s │ %-22s │\n",
		"Disk Usage",
		formatBytes(m.folderStats.TotalSize),
		growthStr,
		details)

	fmt.Println("  └────────────────────┴──────────────┴────────────┴────────────────────────┘")
	fmt.Println()

	// Summary
	fmt.Printf("  Summary: %d discovered queues | %d active queues | %d total items\n", len(m.knownQueues), activeQueues, totalItems)

	// Display Redis info
	m.displayRedisInfo(ctx)

	fmt.Println("\n  Press Ctrl+C to exit")
}

func (m *Monitor) formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	min := d / time.Minute
	d -= min * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, min, s)
	}
	if min > 0 {
		return fmt.Sprintf("%dm %ds", min, s)
	}
	return fmt.Sprintf("%ds", s)
}

func (m *Monitor) displayRedisInfo(ctx context.Context) {
	// Get Redis memory usage
	info, err := m.client.Info(ctx, "memory").Result()
	if err != nil {
		return
	}

	// Parse memory info
	lines := parseInfoOutput(info)
	if memUsage, ok := lines["used_memory_human"]; ok {
		fmt.Printf("\n  Redis Memory: %s", memUsage)
	}

	// Get connected clients
	clientInfo, err := m.client.Info(ctx, "clients").Result()
	if err == nil {
		clientLines := parseInfoOutput(clientInfo)
		if connectedClients, ok := clientLines["connected_clients"]; ok {
			fmt.Printf(" | Connected Clients: %s", connectedClients)
		}
	}
}

func parseInfoOutput(info string) map[string]string {
	result := make(map[string]string)
	lines := strings.Split(info, "\n")

	for _, line := range lines {
		if strings.Contains(line, ":") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				result[key] = value
			}
		}
	}

	return result
}

func (m *Monitor) updateFolderStats() {
	m.statsMutex.Lock()
	defer m.statsMutex.Unlock()

	var totalSize int64
	var repoCount int

	// Log the directory being monitored
	log.Printf("[DiskMonitor] Monitoring directory: %s", m.config.SharedVolumeDir)

	// Check if directory exists
	if _, err := os.Stat(m.config.SharedVolumeDir); os.IsNotExist(err) {
		// Directory doesn't exist, set zero values
		log.Printf("[DiskMonitor] Directory does not exist: %s", m.config.SharedVolumeDir)
		m.folderStats.TotalSize = 0
		m.folderStats.RepoCount = 0
		m.folderStats.GrowthRate = 0
		return
	}

	// Walk through the directory
	var fileCount int
	err := filepath.WalkDir(m.config.SharedVolumeDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Log error but continue walking
			if !os.IsPermission(err) {
				log.Printf("[DiskMonitor] Error accessing path %s: %v", path, err)
			}
			return nil
		}

		// Count top-level directories as repositories
		if d.IsDir() && filepath.Dir(path) == m.config.SharedVolumeDir && path != m.config.SharedVolumeDir {
			repoCount++
		}

		// Add file sizes
		if !d.IsDir() {
			info, err := d.Info()
			if err == nil {
				totalSize += info.Size()
				fileCount++
			}
		}

		return nil
	})
	if err != nil {
		log.Printf("[DiskMonitor] Error walking directory %s: %v", m.config.SharedVolumeDir, err)
		return
	}

	// Log summary
	log.Printf("[DiskMonitor] Found %d repositories with %d files, total size: %s", repoCount, fileCount, formatBytes(totalSize))

	// Calculate growth rate
	now := time.Now()
	if m.folderStats.LastSize > 0 {
		timeDiff := now.Sub(m.folderStats.LastCheck).Minutes()
		if timeDiff > 0 {
			sizeDiffMB := float64(totalSize-m.folderStats.LastSize) / (1024 * 1024)
			m.folderStats.GrowthRate = sizeDiffMB / timeDiff
		}
	}

	m.folderStats.LastSize = totalSize
	m.folderStats.TotalSize = totalSize
	m.folderStats.RepoCount = repoCount
	m.folderStats.LastCheck = now
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
