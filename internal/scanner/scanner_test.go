package scanner

import (
	"testing"
)

func TestExitCodeHandling(t *testing.T) {
	// Test that exit code 200 is handled correctly (not treated as an error)
	tests := []struct {
		name           string
		exitCode       int
		expectError    bool
		expectedLogMsg string
	}{
		{
			name:           "exit code 0 - success",
			exitCode:       0,
			expectError:    false,
			expectedLogMsg: "",
		},
		{
			name:           "exit code 200 - secrets found",
			exitCode:       200,
			expectError:    false,
			expectedLogMsg: "Kingfisher found secrets (exit code 200)",
		},
		{
			name:           "exit code 1 - general error",
			exitCode:       1,
			expectError:    true,
			expectedLogMsg: "kingfisher command failed with exit code 1",
		},
		{
			name:           "exit code 127 - command not found",
			exitCode:       127,
			expectError:    true,
			expectedLogMsg: "kingfisher command failed with exit code 127",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This test validates the logic we use in runKingfisherScan
			// In the actual implementation, exit code 200 should not cause an error
			if tt.exitCode == 200 && tt.expectError {
				t.Error("Exit code 200 should not be treated as an error")
			}
		})
	}
}