package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config" // Added this import
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

// Constants for documentation generation
const (
	maxLinesPerFile = 50000
	timeFormat      = "2006-01-02 15:04:05"
)

// GitProvider handles Git operations using go-git
type GitProvider struct {
	logger *log.Logger
	auth   transport.AuthMethod
}

// NewGitProvider creates a new GitProvider instance
func NewGitProvider(logger *log.Logger) *GitProvider {
	// Try to load SSH keys if available
	auth, err := ssh.DefaultAuthBuilder("git")
	if err != nil {
		logger.Printf("SSH authentication not available: %v", err)
		// Will proceed without auth, allowing public HTTPS access
	}

	return &GitProvider{
		logger: logger,
		auth:   auth,
	}
}

// Clone clones a repository using either SSH or HTTPS
func (p *GitProvider) Clone(ctx context.Context, repoURL, branch, destPath string) error {
	// Try HTTPS first if it's already an HTTPS URL
	isHTTPS := strings.HasPrefix(repoURL, "https://")
	if isHTTPS {
		err := p.cloneHTTPS(ctx, repoURL, branch, destPath)
		if err == nil {
			return nil
		}
		p.logger.Printf("HTTPS clone failed: %v", err)
	}

	// Try SSH if auth is available
	if p.auth != nil {
		err := p.cloneSSH(ctx, repoURL, branch, destPath)
		if err == nil {
			return nil
		}
		p.logger.Printf("SSH clone failed: %v", err)
	}

	// Fall back to HTTPS if not already tried
	if !isHTTPS {
		httpsURL := convertToHTTPS(repoURL)
		if httpsURL != repoURL {
			p.logger.Printf("Trying HTTPS fallback: %s", httpsURL)
			err := p.cloneHTTPS(ctx, httpsURL, branch, destPath)
			if err == nil {
				return nil
			}
			p.logger.Printf("HTTPS fallback failed: %v", err)
		}
	}

	return fmt.Errorf("failed to clone repository: all methods failed")
}

// cloneSSH attempts to clone using SSH authentication
func (p *GitProvider) cloneSSH(ctx context.Context, repoURL, branch, destPath string) error {
	cloneOpts := &git.CloneOptions{
		URL:          repoURL,
		Progress:     io.Discard,
		Auth:         p.auth,
		SingleBranch: true,
		Tags:         git.NoTags,
		Depth:        1,
	}

	if branch != "" {
		cloneOpts.ReferenceName = plumbing.NewBranchReferenceName(branch)
	}

	_, err := git.PlainCloneContext(ctx, destPath, false, cloneOpts)
	return err
}

// cloneHTTPS attempts to clone using HTTPS without authentication
func (p *GitProvider) cloneHTTPS(ctx context.Context, repoURL, branch, destPath string) error {
	cloneOpts := &git.CloneOptions{
		URL:          repoURL,
		Progress:     os.Stdout,
		Auth:         nil, // No auth for public HTTPS
		SingleBranch: true,
		Depth:        1,
	}

	if branch != "" {
		cloneOpts.ReferenceName = plumbing.NewBranchReferenceName(branch)
	}

	_, err := git.PlainCloneContext(ctx, destPath, false, cloneOpts)
	return err
}

// GetDefaultBranch attempts to detect the default branch
func (p *GitProvider) GetDefaultBranch(ctx context.Context, repoURL string) (string, error) {
	// Create a temporary directory for remote inspection
	tempDir, err := os.MkdirTemp("", "git-remote-*")
	if err != nil {
		return "main", nil
	}
	defer os.RemoveAll(tempDir)

	// List remote references
	remote := git.NewRemote(nil, &config.RemoteConfig{
		Name: "origin",
		URLs: []string{repoURL},
	})

	refs, err := remote.List(&git.ListOptions{Auth: p.auth})
	if err != nil {
		if err == transport.ErrAuthenticationRequired {
			// Try HTTPS if SSH fails
			httpsURL := convertToHTTPS(repoURL)
			if httpsURL != repoURL {
				remote = git.NewRemote(nil, &config.RemoteConfig{
					Name: "origin",
					URLs: []string{httpsURL},
				})
				refs, err = remote.List(&git.ListOptions{})
			}
		}
		if err != nil {
			return "main", nil
		}
	}

	// Look for HEAD reference
	for _, ref := range refs {
		if ref.Name().IsBranch() && strings.HasPrefix(ref.Name().String(), "refs/heads/") {
			if ref.Name().String() == "refs/heads/main" || ref.Name().String() == "refs/heads/master" {
				return ref.Name().Short(), nil
			}
		}
	}

	return "main", nil
}

func (p *GitProvider) GetRepoInfo(ctx context.Context, repoPath string) (RepoInfo, error) {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return RepoInfo{}, fmt.Errorf("failed to open repository: %w", err)
	}

	head, err := repo.Head()
	if err != nil {
		return RepoInfo{}, fmt.Errorf("failed to get HEAD: %w", err)
	}

	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return RepoInfo{}, fmt.Errorf("failed to get commit: %w", err)
	}

	branch := head.Name().Short()
	if branch == "" {
		branch = "HEAD"
	}

	return RepoInfo{
		Branch:     branch,
		CommitHash: head.Hash().String()[:8],
		Author:     commit.Author.Name,
		Date:       commit.Author.When.Format(timeFormat),
		Subject:    commit.Message,
	}, nil
}

// RepoInfo contains repository metadata
type RepoInfo struct {
	Branch     string
	CommitHash string
	Author     string
	Date       string
	Subject    string
}

// RepoDocGenerator handles the documentation generation process
type RepoDocGenerator struct {
	RepoURL       string
	OutputDir     string
	Branch        string
	TempDir       string
	RepoPath      string
	Logger        *log.Logger
	GitProvider   *GitProvider
	excludedFiles map[string]bool
	excludedLangs map[string]bool
}

// NewRepoDocGenerator creates a new RepoDocGenerator instance
func NewRepoDocGenerator(repoURL, outputDir, branch string, logger *log.Logger) *RepoDocGenerator {
	return &RepoDocGenerator{
		RepoURL:     repoURL,
		OutputDir:   outputDir,
		Branch:      branch,
		Logger:      logger,
		GitProvider: NewGitProvider(logger),
		excludedFiles: map[string]bool{
			"CHANGELOG.md":       true,
			"CONTRIBUTING.md":    true,
			"LICENSE.md":         true,
			"OWNERS.md":          true,
			"SECURITY.md":        true,
			"SUPPORT.md":         true,
			"CODE_OF_CONDUCT.md": true,
			"GOVERNANCE.md":      true,
			"README.md":          true,
			"MAINTAINERS.md":     true,
			"CODEOWNERS":         true,
		},
		excludedLangs: map[string]bool{
			"ar": true, "bn": true, "de": true, "es": true,
			"fa": true, "fr": true, "hi": true, "id": true,
			"it": true, "ja": true, "ko": true, "no": true,
			"pl": true, "pt": true, "ru": true, "th": true,
			"tr": true, "uk": true, "vi": true, "zh": true,
		},
	}
}

// Initialize sets up temporary directory and repository path
func (g *RepoDocGenerator) Initialize() error {
	tempDir, err := os.MkdirTemp("", "repo-docs-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	g.TempDir = tempDir
	g.RepoPath = filepath.Join(tempDir, "repo")
	return nil
}

// Cleanup removes temporary directory
func (g *RepoDocGenerator) Cleanup() {
	if g.TempDir != "" {
		os.RemoveAll(g.TempDir)
	}
}

// CloneRepository clones the git repository
func (g *RepoDocGenerator) CloneRepository() error {
	if g.Branch == "" {
		var err error
		g.Branch, err = g.GitProvider.GetDefaultBranch(context.Background(), g.RepoURL)
		if err != nil {
			g.Logger.Printf("Warning: Could not detect default branch: %v", err)
		}
	}

	err := g.GitProvider.Clone(context.Background(), g.RepoURL, g.Branch, g.RepoPath)
	if err != nil && g.Branch == "main" {
		g.Logger.Println("Branch 'main' not found, trying 'master'...")
		g.Branch = "master"
		err = g.GitProvider.Clone(context.Background(), g.RepoURL, g.Branch, g.RepoPath)
	}
	return err
}

// shouldIncludeFile determines if a file should be included in documentation
func (g *RepoDocGenerator) shouldIncludeFile(path string) bool {
	// Check excluded files
	if g.excludedFiles[filepath.Base(path)] {
		return false
	}

	// Check excluded directories
	excludedDirs := map[string]bool{
		"test": true, "tests": true, "vendor": true,
		"node_modules": true, "examples": true, "sample": true,
	}

	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, part := range parts {
		if excludedDirs[strings.ToLower(part)] {
			return false
		}
		if g.excludedLangs[part] {
			return false
		}
	}

	// Check language code patterns
	pathStr := filepath.ToSlash(path)
	for lang := range g.excludedLangs {
		patterns := []string{
			fmt.Sprintf("/docs/%s/", lang),
			fmt.Sprintf("/content/%s/", lang),
			fmt.Sprintf("/%s/docs/", lang),
			fmt.Sprintf("/%s/content/", lang),
			fmt.Sprintf("-%s.md", lang),
			fmt.Sprintf("_%s.md", lang),
			fmt.Sprintf(".%s.md", lang),
		}
		for _, pattern := range patterns {
			if strings.Contains(pathStr, pattern) {
				return false
			}
		}
	}

	return true
}

// findMarkdownFiles finds all markdown files in the repository
func (g *RepoDocGenerator) findMarkdownFiles() ([]MarkdownFile, error) {
	var files []MarkdownFile
	err := filepath.Walk(g.RepoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && strings.HasSuffix(strings.ToLower(path), ".md") {
			relPath, err := filepath.Rel(g.RepoPath, path)
			if err != nil {
				return err
			}

			// Skip hidden directories
			parts := strings.Split(filepath.ToSlash(relPath), "/")
			for _, part := range parts {
				if strings.HasPrefix(part, ".") {
					return nil
				}
			}

			if g.shouldIncludeFile(relPath) {
				content, err := os.ReadFile(path)
				if err != nil {
					g.Logger.Printf("Warning: Skipping file %s due to read error: %v", relPath, err)
					return nil
				}

				files = append(files, MarkdownFile{
					Path:         relPath,
					Content:      string(content),
					ModifiedTime: info.ModTime(),
				})
			}
		}
		return nil
	})

	return files, err
}

// generateRepoContext generates repository context information
func (g *RepoDocGenerator) generateRepoContext() (string, error) {
	repoInfo, err := g.GitProvider.GetRepoInfo(context.Background(), g.RepoPath)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# Repository Documentation\n\n")
	b.WriteString("## Repository Context\n\n")
	b.WriteString(fmt.Sprintf("Repository URL: %s\n", g.RepoURL))
	b.WriteString(fmt.Sprintf("Branch: %s\n", repoInfo.Branch))
	b.WriteString(fmt.Sprintf("Last Commit: %s\n", repoInfo.CommitHash))
	b.WriteString(fmt.Sprintf("Last Commit Date: %s\n", repoInfo.Date))
	b.WriteString(fmt.Sprintf("Author: %s\n", repoInfo.Author))
	b.WriteString(fmt.Sprintf("Subject: %s\n\n", repoInfo.Subject))
	b.WriteString("This document is a compilation of selected markdown documentation found in the repository.\n")
	b.WriteString("Each section maintains the original directory structure and content.\n\n")
	b.WriteString(fmt.Sprintf("*Generated on: %s*\n", time.Now().Format(timeFormat)))

	return b.String(), nil
}

// writeDocumentPart writes a single part of the documentation
func (g *RepoDocGenerator) writeDocumentPart(filename string, content []string, includeContext bool) error {
	err := os.MkdirAll(g.OutputDir, 0755)
	if err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	file, err := os.Create(filepath.Join(g.OutputDir, filename))
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)

	if includeContext {
		context, err := g.generateRepoContext()
		if err != nil {
			return fmt.Errorf("failed to generate repo context: %w", err)
		}
		_, err = writer.WriteString(context)
		if err != nil {
			return fmt.Errorf("failed to write context: %w", err)
		}
	}

	for _, section := range content {
		_, err = writer.WriteString(section)
		if err != nil {
			return fmt.Errorf("failed to write section: %w", err)
		}
	}

	return writer.Flush()
}

// CreateMasterDocument creates the master documentation file(s)
func (g *RepoDocGenerator) CreateMasterDocument() error {
	files, err := g.findMarkdownFiles()
	if err != nil {
		return fmt.Errorf("failed to find markdown files: %w", err)
	}

	urlParts := strings.Split(strings.TrimSuffix(g.RepoURL, ".git"), "/")
	repoName := urlParts[len(urlParts)-1]
	orgName := urlParts[len(urlParts)-2]
	fullName := fmt.Sprintf("%s_%s", orgName, repoName)
	timestamp := time.Now().Format("20060102_150405")

	var currentContent []string
	currentLineCount := 0
	currentPart := 1

	for _, file := range files {
		sectionName := strings.TrimSuffix(file.Path, ".md")
		section := fmt.Sprintf("\n## %s\n\n", sectionName)
		section += fmt.Sprintf("*Source: %s*\n", file.Path)
		section += fmt.Sprintf("*Last modified: %s*\n\n", file.ModifiedTime.Format(timeFormat))
		section += file.Content
		section += "\n\n---\n\n"

		sectionLines := len(strings.Split(section, "\n"))

		if currentLineCount+sectionLines > maxLinesPerFile {
			filename := fmt.Sprintf("%s_docs_%s_part%d.md", fullName, timestamp, currentPart)
			err := g.writeDocumentPart(filename, currentContent, currentPart == 1)
			if err != nil {
				return fmt.Errorf("failed to write part %d: %w", currentPart, err)
			}

			currentPart++
			currentContent = nil
			currentLineCount = 0
		}

		currentContent = append(currentContent, section)
		currentLineCount += sectionLines
	}

	if len(currentContent) > 0 {
		filename := fmt.Sprintf("%s_docs_%s_part%d.md", fullName, timestamp, currentPart)
		err := g.writeDocumentPart(filename, currentContent, currentPart == 1)
		if err != nil {
			return fmt.Errorf("failed to write final part: %w", err)
		}
	}

	return nil
}

type MarkdownFile struct {
	Path         string
	Content      string
	ModifiedTime time.Time
}

// Helper functions
func convertToHTTPS(repoURL string) string {
	if strings.HasPrefix(repoURL, "git@github.com:") {
		// Convert SSH URL to HTTPS
		repoPath := strings.TrimPrefix(repoURL, "git@github.com:")
		return fmt.Sprintf("https://github.com/%s", strings.TrimSuffix(repoPath, ".git"))
	}
	return repoURL
}

func isValidGitURL(urlStr string) bool {
	// Check for valid SSH format
	if strings.HasPrefix(urlStr, "git@github.com:") && strings.HasSuffix(urlStr, ".git") {
		return true
	}

	// Check for valid HTTPS format
	if strings.HasPrefix(urlStr, "https://github.com/") && strings.HasSuffix(urlStr, ".git") {
		return true
	}

	return false
}

func processRepository(ctx context.Context, repoURL, outputDir, branch string, verbose bool) error {
	logger := log.New(os.Stdout, "", log.LstdFlags)
	if verbose {
		logger.SetFlags(log.LstdFlags | log.Lshortfile)
	}

	generator := NewRepoDocGenerator(repoURL, outputDir, branch, logger)

	err := generator.Initialize()
	if err != nil {
		return fmt.Errorf("failed to initialize generator: %w", err)
	}
	defer generator.Cleanup()

	err = generator.CloneRepository()
	if err != nil {
		return fmt.Errorf("failed to clone repository: %w", err)
	}

	err = generator.CreateMasterDocument()
	if err != nil {
		return fmt.Errorf("failed to create master document: %w", err)
	}

	return nil
}

func readRepoURLs(filePath string) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	var urls []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			if !isValidGitURL(line) {
				log.Printf("Warning: Skipping invalid Git URL: %s", line)
				continue
			}
			urls = append(urls, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return urls, nil
}

func processRepositories(ctx context.Context, inputFile string, outputDir string, branch string, verbose bool) error {
	urls, err := readRepoURLs(inputFile)
	if err != nil {
		return fmt.Errorf("failed to read repository URLs: %w", err)
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 4) // Limit concurrent processing
	errors := make(chan error, len(urls))

	for _, url := range urls {
		wg.Add(1)
		go func(repoURL string) {
			defer wg.Done()
			semaphore <- struct{}{}        // Acquire
			defer func() { <-semaphore }() // Release

			if err := processRepository(ctx, repoURL, outputDir, branch, verbose); err != nil {
				errors <- fmt.Errorf("error processing repository %s: %w", repoURL, err)
			}
		}(url)
	}

	wg.Wait()
	close(errors)

	// Collect all errors
	var errMsgs []string
	for err := range errors {
		errMsgs = append(errMsgs, err.Error())
	}

	if len(errMsgs) > 0 {
		return fmt.Errorf("encountered errors:\n%s", strings.Join(errMsgs, "\n"))
	}

	return nil
}

func main() {
	var (
		outputDir string
		branch    string
		verbose   bool
	)

	// Parse command line flags
	fs := flag.NewFlagSet("repo-docs", flag.ExitOnError)
	fs.StringVar(&outputDir, "output-dir", "./output", "Output directory for the generated documentation")
	fs.StringVar(&branch, "branch", "", "Branch to clone (if not specified, will detect default branch)")
	fs.BoolVar(&verbose, "verbose", false, "Enable verbose logging")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <repository-url or file with repository URLs>\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(1)
	}

	input := fs.Arg(0)
	ctx := context.Background()

	// Check if input is a file
	if info, err := os.Stat(input); err == nil && !info.IsDir() {
		err = processRepositories(ctx, input, outputDir, branch, verbose)
		if err != nil {
			log.Fatalf("Failed to process repositories: %v", err)
		}
	} else {
		// Process single repository
		err = processRepository(ctx, input, outputDir, branch, verbose)
		if err != nil {
			log.Fatalf("Failed to process repository: %v", err)
		}
	}
}
