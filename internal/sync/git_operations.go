package sync

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/klimeurt/heimdall/internal/logging"
)

// cloneRepository clones a repository to the shared volume
func (s *Service) cloneRepository(ctx context.Context, repo *Repository) error {
	repoPath := filepath.Join(s.cfg.SharedVolumePath, repo.Org, repo.Name)
	orgPath := filepath.Join(s.cfg.SharedVolumePath, repo.Org)

	// Create org directory if it doesn't exist
	if err := os.MkdirAll(orgPath, 0755); err != nil {
		return fmt.Errorf("failed to create org directory: %w", err)
	}

	// Check if repository already exists
	if _, err := os.Stat(repoPath); err == nil {
		s.logger.Info("repository already exists, skipping clone",
			slog.String("org", repo.Org),
			slog.String("repo", repo.Name))
		return nil
	}

	logger := logging.LoggerFromContext(ctx, s.logger)
	logger.Info("cloning repository",
		slog.String("org", repo.Org),
		slog.String("repo", repo.Name),
		slog.String("path", repoPath))

	// Create timeout context
	cloneCtx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.SyncTimeoutMinutes)*time.Minute)
	defer cancel()

	// Determine clone URL based on whether we have a token
	cloneURL := repo.URL
	if s.cfg.GitHubToken != "" && repo.Private {
		// For private repos, use token in URL
		cloneURL = strings.Replace(repo.URL, "https://", fmt.Sprintf("https://%s@", s.cfg.GitHubToken), 1)
	}

	// Clone with all branches and tags
	cmd := exec.CommandContext(cloneCtx, "git", "clone", cloneURL, repoPath)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0") // Disable password prompts
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Clean up partial clone
		os.RemoveAll(repoPath)
		return fmt.Errorf("git clone failed: %w\nOutput: %s", err, string(output))
	}

	// Fetch all branches and tags
	fetchCmd := exec.CommandContext(cloneCtx, "git", "-C", repoPath, "fetch", "--all", "--tags")
	fetchCmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	
	output, err = fetchCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git fetch failed: %w\nOutput: %s", err, string(output))
	}

	// Update sync state
	s.mu.Lock()
	s.syncState[repo.Name] = time.Now()
	s.mu.Unlock()

	logger.Info("successfully cloned repository",
		slog.String("org", repo.Org),
		slog.String("repo", repo.Name))

	// Push to scanner queues immediately
	if err := s.pushRepositoryToQueues(ctx, repo); err != nil {
		logger.Warn("failed to push repository to queues",
			slog.String("org", repo.Org),
			slog.String("repo", repo.Name),
			slog.String("error", err.Error()))
		// Don't fail the clone operation if queue push fails
	}

	return nil
}

// updateRepository updates an existing repository
func (s *Service) updateRepository(ctx context.Context, repo *Repository) error {
	repoPath := filepath.Join(s.cfg.SharedVolumePath, repo.Org, repo.Name)

	// Verify repository exists
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		s.logger.Info("repository not found locally, will clone instead",
			slog.String("org", repo.Org),
			slog.String("repo", repo.Name))
		return s.cloneRepository(ctx, repo)
	}

	logger := logging.LoggerFromContext(ctx, s.logger)
	logger.Info("updating repository",
		slog.String("org", repo.Org),
		slog.String("repo", repo.Name),
		slog.String("path", repoPath))

	// Create timeout context
	updateCtx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.SyncTimeoutMinutes)*time.Minute)
	defer cancel()

	// Configure remote URL with token if needed
	if s.cfg.GitHubToken != "" && repo.Private {
		remoteURL := strings.Replace(repo.URL, "https://", fmt.Sprintf("https://%s@", s.cfg.GitHubToken), 1)
		
		cmd := exec.CommandContext(updateCtx, "git", "-C", repoPath, "remote", "set-url", "origin", remoteURL)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to update remote URL: %w\nOutput: %s", err, string(output))
		}
	}

	// Fetch all updates with prune
	cmd := exec.CommandContext(updateCtx, "git", "-C", repoPath, "fetch", "--all", "--tags", "--prune", "--prune-tags")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Log the fetch failure
		logger.Warn("git fetch failed, attempting recovery by re-cloning",
			slog.String("org", repo.Org),
			slog.String("repo", repo.Name),
			slog.String("error", err.Error()),
			slog.String("output", string(output)))
		
		// Attempt recovery: delete the repository and clone fresh
		logger.Info("removing corrupted repository for re-clone",
			slog.String("org", repo.Org),
			slog.String("repo", repo.Name),
			slog.String("path", repoPath))
		
		// Remove the corrupted repository
		if removeErr := os.RemoveAll(repoPath); removeErr != nil {
			return fmt.Errorf("failed to remove corrupted repository for re-clone: %w (original error: %w)", removeErr, err)
		}
		
		// Remove from sync state
		s.mu.Lock()
		delete(s.syncState, repo.Name)
		s.mu.Unlock()
		
		// Attempt fresh clone
		logger.Info("attempting fresh clone after fetch failure",
			slog.String("org", repo.Org),
			slog.String("repo", repo.Name))
		
		if cloneErr := s.cloneRepository(ctx, repo); cloneErr != nil {
			return fmt.Errorf("failed to re-clone repository after fetch failure: %w (original error: %w)", cloneErr, err)
		}
		
		logger.Info("successfully recovered repository with fresh clone",
			slog.String("org", repo.Org),
			slog.String("repo", repo.Name))
		
		// Return success - the clone operation already pushed to queues
		return nil
	}

	// Ensure the working directory is properly checked out (in case it was previously cloned with --no-checkout)
	// Get the current branch
	getCurrentBranchCmd := exec.CommandContext(updateCtx, "git", "-C", repoPath, "symbolic-ref", "--short", "HEAD")
	currentBranch, err := getCurrentBranchCmd.Output()
	if err != nil {
		// If we can't get current branch (detached HEAD or no HEAD), checkout the default branch
		checkoutCmd := exec.CommandContext(updateCtx, "git", "-C", repoPath, "checkout", "-f", "HEAD")
		checkoutCmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		
		if output, err := checkoutCmd.CombinedOutput(); err != nil {
			logger.Warn("could not checkout HEAD",
				slog.String("org", repo.Org),
				slog.String("repo", repo.Name),
				slog.String("error", err.Error()),
				slog.String("output", string(output)))
		}
	} else {
		// Reset the working directory to match the current branch
		branch := strings.TrimSpace(string(currentBranch))
		
		// Check if the remote branch exists before attempting to reset
		checkRemoteCmd := exec.CommandContext(updateCtx, "git", "-C", repoPath, "rev-parse", "--verify", fmt.Sprintf("origin/%s", branch))
		checkRemoteCmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		
		if _, err := checkRemoteCmd.Output(); err != nil {
			logger.Warn("remote branch does not exist, skipping reset",
				slog.String("org", repo.Org),
				slog.String("repo", repo.Name),
				slog.String("branch", branch),
				slog.String("remote_branch", fmt.Sprintf("origin/%s", branch)))
		} else {
			// Remote branch exists, safe to reset
			resetCmd := exec.CommandContext(updateCtx, "git", "-C", repoPath, "reset", "--hard", fmt.Sprintf("origin/%s", branch))
			resetCmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
			
			if output, err := resetCmd.CombinedOutput(); err != nil {
				logger.Warn("could not reset to origin branch",
					slog.String("org", repo.Org),
					slog.String("repo", repo.Name),
					slog.String("branch", branch),
					slog.String("error", err.Error()),
					slog.String("output", string(output)))
			}
		}
	}

	// Update sync state
	s.mu.Lock()
	s.syncState[repo.Name] = time.Now()
	s.mu.Unlock()

	logger.Info("successfully updated repository",
		slog.String("org", repo.Org),
		slog.String("repo", repo.Name))

	// Push to scanner queues immediately
	if err := s.pushRepositoryToQueues(ctx, repo); err != nil {
		logger.Warn("failed to push repository to queues",
			slog.String("org", repo.Org),
			slog.String("repo", repo.Name),
			slog.String("error", err.Error()))
		// Don't fail the update operation if queue push fails
	}

	return nil
}

// removeRepository removes a repository from the shared volume
func (s *Service) removeRepository(ctx context.Context, repoName string) error {
	repoPath := filepath.Join(s.cfg.SharedVolumePath, s.cfg.GitHubOrg, repoName)

	// Verify path is within shared volume (security check)
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	absSharedVolume, err := filepath.Abs(s.cfg.SharedVolumePath)
	if err != nil {
		return fmt.Errorf("failed to get absolute shared volume path: %w", err)
	}

	if !strings.HasPrefix(absPath, absSharedVolume) {
		return fmt.Errorf("repository path %s is outside shared volume", absPath)
	}

	logger := logging.LoggerFromContext(ctx, s.logger)
	logger.Info("removing repository",
		slog.String("org", s.cfg.GitHubOrg),
		slog.String("repo", repoName),
		slog.String("path", repoPath))

	// Remove the repository directory
	if err := os.RemoveAll(repoPath); err != nil {
		return fmt.Errorf("failed to remove repository: %w", err)
	}

	// Remove from sync state
	s.mu.Lock()
	delete(s.syncState, repoName)
	s.mu.Unlock()

	logger.Info("successfully removed repository",
		slog.String("org", s.cfg.GitHubOrg),
		slog.String("repo", repoName))
	return nil
}

// repositoryNeedsUpdate checks if a repository needs to be updated by comparing
// remote and local refs (branches and tags). Returns true if update is needed.
func (s *Service) repositoryNeedsUpdate(ctx context.Context, repo *Repository) (bool, error) {
	repoPath := filepath.Join(s.cfg.SharedVolumePath, repo.Org, repo.Name)

	// Verify repository exists locally
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		return true, nil // Repository doesn't exist locally, needs "update" (clone)
	}

	logger := logging.LoggerFromContext(ctx, s.logger)
	logger.Debug("checking if repository needs update",
		slog.String("org", repo.Org),
		slog.String("repo", repo.Name))

	// Create timeout context for remote operations
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Get remote refs using git ls-remote
	remoteRefs, err := s.getRemoteRefs(checkCtx, repo, repoPath)
	if err != nil {
		// If we can't get remote refs, fallback to updating
		logger.Warn("failed to get remote refs, will perform full update",
			slog.String("org", repo.Org),
			slog.String("repo", repo.Name),
			slog.String("error", err.Error()))
		return true, nil
	}

	// Get local refs using git show-ref
	localRefs, err := s.getLocalRefs(checkCtx, repoPath)
	if err != nil {
		// If we can't get local refs, fallback to updating
		logger.Warn("failed to get local refs, will perform full update",
			slog.String("org", repo.Org),
			slog.String("repo", repo.Name),
			slog.String("error", err.Error()))
		return true, nil
	}

	// Compare refs to determine if update is needed
	needsUpdate := s.compareRefs(remoteRefs, localRefs)
	
	if needsUpdate {
		logger.Debug("repository needs update",
			slog.String("org", repo.Org),
			slog.String("repo", repo.Name))
	} else {
		logger.Debug("repository is up to date",
			slog.String("org", repo.Org),
			slog.String("repo", repo.Name))
	}

	return needsUpdate, nil
}

// getRemoteRefs retrieves all remote refs (branches and tags) with their commit hashes
func (s *Service) getRemoteRefs(ctx context.Context, repo *Repository, repoPath string) (map[string]string, error) {
	// Determine remote URL with authentication if needed
	remoteURL := repo.URL
	if s.cfg.GitHubToken != "" && repo.Private {
		remoteURL = strings.Replace(repo.URL, "https://", fmt.Sprintf("https://%s@", s.cfg.GitHubToken), 1)
	}

	// Use git ls-remote to get all remote refs
	cmd := exec.CommandContext(ctx, "git", "ls-remote", remoteURL)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git ls-remote failed: %w\nOutput: %s", err, string(output))
	}

	// Parse the output to extract refs
	refs := make(map[string]string)
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		
		commitHash := parts[0]
		refName := parts[1]
		
		// Only include branches and tags, skip other refs like pull requests
		if strings.HasPrefix(refName, "refs/heads/") || strings.HasPrefix(refName, "refs/tags/") {
			refs[refName] = commitHash
		}
	}

	return refs, nil
}

// getLocalRefs retrieves all local refs (branches and tags) with their commit hashes
func (s *Service) getLocalRefs(ctx context.Context, repoPath string) (map[string]string, error) {
	// Use git show-ref to get all local refs
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "show-ref")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		// show-ref fails if there are no refs, which is valid for empty repos
		if strings.Contains(string(output), "no such ref") || strings.Contains(err.Error(), "exit status 1") {
			return make(map[string]string), nil
		}
		return nil, fmt.Errorf("git show-ref failed: %w\nOutput: %s", err, string(output))
	}

	// Parse the output to extract refs
	refs := make(map[string]string)
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		
		commitHash := parts[0]
		refName := parts[1]
		
		// Convert local branch refs to match remote format
		if strings.HasPrefix(refName, "refs/remotes/origin/") {
			// Convert refs/remotes/origin/branch to refs/heads/branch
			branchName := strings.TrimPrefix(refName, "refs/remotes/origin/")
			if branchName != "HEAD" { // Skip the HEAD ref
				refs["refs/heads/"+branchName] = commitHash
			}
		} else if strings.HasPrefix(refName, "refs/tags/") {
			// Keep tags as-is
			refs[refName] = commitHash
		}
	}

	return refs, nil
}

// compareRefs compares remote and local refs to determine if update is needed
func (s *Service) compareRefs(remoteRefs, localRefs map[string]string) bool {
	// Check if any remote ref is missing locally or has a different commit hash
	for refName, remoteHash := range remoteRefs {
		localHash, exists := localRefs[refName]
		if !exists || localHash != remoteHash {
			return true // Update needed
		}
	}

	// Check if any local ref no longer exists on remote (for cleanup)
	for refName := range localRefs {
		if _, exists := remoteRefs[refName]; !exists {
			return true // Update needed to clean up deleted refs
		}
	}

	return false // No update needed
}