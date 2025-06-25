package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name        string
		envVars     map[string]string
		wantErr     bool
		expectedCfg *Config
	}{
		{
			name: "valid config with all env vars",
			envVars: map[string]string{
				"GITHUB_ORG":     "testorg",
				"GITHUB_TOKEN":   "token123",
				"HTTP_ENDPOINT":  "http://test:8080/repos",
				"CRON_SCHEDULE":  "0 */6 * * *",
				"RUN_ON_STARTUP": "true",
			},
			wantErr: false,
			expectedCfg: &Config{
				GitHubOrg:      "testorg",
				GitHubToken:    "token123",
				HTTPEndpoint:   "http://test:8080/repos",
				CronSchedule:   "0 */6 * * *",
				RunOnStartup:   true,
				GitHubPageSize: 100,
				RedisHost:      "localhost",
				RedisPort:      "6379",
			},
		},
		{
			name: "valid config with defaults",
			envVars: map[string]string{
				"GITHUB_ORG":   "testorg",
				"GITHUB_TOKEN": "token123",
			},
			wantErr: false,
			expectedCfg: &Config{
				GitHubOrg:      "testorg",
				GitHubToken:    "token123",
				HTTPEndpoint:   "http://localhost:8080/repositories",
				CronSchedule:   "0 0 * * 0",
				RunOnStartup:   false,
				GitHubPageSize: 100,
				RedisHost:      "localhost",
				RedisPort:      "6379",
			},
		},
		{
			name: "missing github org",
			envVars: map[string]string{
				"GITHUB_TOKEN": "token123",
			},
			wantErr: true,
		},
		{
			name: "missing github token is allowed",
			envVars: map[string]string{
				"GITHUB_ORG": "testorg",
			},
			wantErr: false,
			expectedCfg: &Config{
				GitHubOrg:      "testorg",
				GitHubToken:    "",
				HTTPEndpoint:   "http://localhost:8080/repositories",
				CronSchedule:   "0 0 * * 0",
				RunOnStartup:   false,
				GitHubPageSize: 100,
				RedisHost:      "localhost",
				RedisPort:      "6379",
			},
		},
		{
			name: "run on startup false",
			envVars: map[string]string{
				"GITHUB_ORG":     "testorg",
				"GITHUB_TOKEN":   "token123",
				"RUN_ON_STARTUP": "false",
			},
			wantErr: false,
			expectedCfg: &Config{
				GitHubOrg:      "testorg",
				GitHubToken:    "token123",
				HTTPEndpoint:   "http://localhost:8080/repositories",
				CronSchedule:   "0 0 * * 0",
				RunOnStartup:   false,
				GitHubPageSize: 100,
				RedisHost:      "localhost",
				RedisPort:      "6379",
			},
		},
		{
			name: "custom github api settings",
			envVars: map[string]string{
				"GITHUB_ORG":          "testorg",
				"GITHUB_TOKEN":        "token123",
				"GITHUB_PAGE_SIZE":    "50",
				"GITHUB_API_DELAY_MS": "1000",
			},
			wantErr: false,
			expectedCfg: &Config{
				GitHubOrg:      "testorg",
				GitHubToken:    "token123",
				HTTPEndpoint:   "http://localhost:8080/repositories",
				CronSchedule:   "0 0 * * 0",
				RunOnStartup:   false,
				GitHubPageSize: 50,
				GitHubAPIDelay: 1000 * time.Millisecond,
				RedisHost:      "localhost",
				RedisPort:      "6379",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear environment
			clearEnv()

			// Set test environment variables
			for k, v := range tt.envVars {
				os.Setenv(k, v)
			}

			defer clearEnv()

			cfg, err := Load()

			if tt.wantErr {
				if err == nil {
					t.Errorf("Load() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Load() unexpected error: %v", err)
				return
			}

			if cfg.GitHubOrg != tt.expectedCfg.GitHubOrg {
				t.Errorf("GitHubOrg = %v, want %v", cfg.GitHubOrg, tt.expectedCfg.GitHubOrg)
			}
			if cfg.GitHubToken != tt.expectedCfg.GitHubToken {
				t.Errorf("GitHubToken = %v, want %v", cfg.GitHubToken, tt.expectedCfg.GitHubToken)
			}
			if cfg.HTTPEndpoint != tt.expectedCfg.HTTPEndpoint {
				t.Errorf("HTTPEndpoint = %v, want %v", cfg.HTTPEndpoint, tt.expectedCfg.HTTPEndpoint)
			}
			if cfg.CronSchedule != tt.expectedCfg.CronSchedule {
				t.Errorf("CronSchedule = %v, want %v", cfg.CronSchedule, tt.expectedCfg.CronSchedule)
			}
			if cfg.RunOnStartup != tt.expectedCfg.RunOnStartup {
				t.Errorf("RunOnStartup = %v, want %v", cfg.RunOnStartup, tt.expectedCfg.RunOnStartup)
			}
			if cfg.GitHubPageSize != tt.expectedCfg.GitHubPageSize {
				t.Errorf("GitHubPageSize = %v, want %v", cfg.GitHubPageSize, tt.expectedCfg.GitHubPageSize)
			}
			if cfg.GitHubAPIDelay != tt.expectedCfg.GitHubAPIDelay {
				t.Errorf("GitHubAPIDelay = %v, want %v", cfg.GitHubAPIDelay, tt.expectedCfg.GitHubAPIDelay)
			}
			if cfg.RedisHost != tt.expectedCfg.RedisHost {
				t.Errorf("RedisHost = %v, want %v", cfg.RedisHost, tt.expectedCfg.RedisHost)
			}
			if cfg.RedisPort != tt.expectedCfg.RedisPort {
				t.Errorf("RedisPort = %v, want %v", cfg.RedisPort, tt.expectedCfg.RedisPort)
			}
		})
	}
}

func clearEnv() {
	envVars := []string{
		"GITHUB_ORG", "GITHUB_TOKEN", "HTTP_ENDPOINT",
		"CRON_SCHEDULE", "RUN_ON_STARTUP", "GITHUB_PAGE_SIZE", "GITHUB_API_DELAY_MS",
		"REDIS_HOST", "REDIS_PORT", "REDIS_PASSWORD", "REDIS_DB",
	}
	for _, env := range envVars {
		os.Unsetenv(env)
	}
}
