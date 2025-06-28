package cleaner

import (
	"testing"

	"github.com/klimeurt/heimdall/internal/config"
)

func TestIsPathSafe(t *testing.T) {
	tests := []struct {
		name            string
		sharedVolumeDir string
		path            string
		expected        bool
	}{
		{
			name:            "Valid path within shared volume",
			sharedVolumeDir: "/shared/heimdall-repos",
			path:            "/shared/heimdall-repos/org_repo_uuid",
			expected:        true,
		},
		{
			name:            "Valid nested path within shared volume",
			sharedVolumeDir: "/shared/heimdall-repos",
			path:            "/shared/heimdall-repos/org_repo_uuid/subdir",
			expected:        true,
		},
		{
			name:            "Invalid path outside shared volume",
			sharedVolumeDir: "/shared/heimdall-repos",
			path:            "/tmp/malicious",
			expected:        false,
		},
		{
			name:            "Invalid path with traversal attempt",
			sharedVolumeDir: "/shared/heimdall-repos",
			path:            "/shared/heimdall-repos/../../../etc/passwd",
			expected:        false,
		},
		{
			name:            "Valid relative path that resolves within shared volume",
			sharedVolumeDir: "./shared",
			path:            "./shared/repo",
			expected:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleaner := &Cleaner{
				config: &config.CleanerConfig{
					SharedVolumeDir: tt.sharedVolumeDir,
				},
			}

			result := cleaner.isPathSafe(tt.path)
			if result != tt.expected {
				t.Errorf("isPathSafe(%s) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}
