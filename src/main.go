package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Constants for documentation generation
const (
	maxLinesPerFile = 50000
	timeFormat      = "2006-01-02 15:04:05"
)

// RepoDocGenerator handles the documentation generation process
type RepoDocGenerator struct {
	RepoURL       string
	OutputDir     string
	Branch        string
	TempDir       string
	RepoPath      string
	Logger        *log.Logger
	excludedFiles map[string]bool
	excludedLangs map[string]bool
}

// NewRepoDocGenerator creates a new RepoDocGenerator instance
func NewRepoDocGenerator(repoURL, outputDir, branch string, logger *log.Logger) *RepoDocGenerator {
	return &RepoDocGenerator{
		RepoURL:   repoURL,
		OutputDir: outputDir,
		Branch:    branch,
		Logger:    logger,
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

// runGitCommand executes a git command and returns its output
func (g *RepoDocGenerator) runGitCommand(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("git command failed: %w\nstderr: %s", err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}

// DetectDefaultBranch attempts to detect the default branch of the repository
func (g *RepoDocGenerator) DetectDefaultBranch() (string, error) {
	output, err := g.runGitCommand("", "ls-remote", "--symref", g.RepoURL, "HEAD")
	if err != nil {
		return "main", nil // fallback to main if detection fails
	}

	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "ref:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return filepath.Base(parts[1]), nil
			}
		}
	}

	return "main", nil
}

// CloneRepository clones the git repository
func (g *RepoDocGenerator) CloneRepository() error {
	if g.Branch == "" {
		var err error
		g.Branch, err = g.DetectDefaultBranch()
		if err != nil {
			g.Logger.Printf("Warning: Could not detect default branch: %v", err)
		}
	}

	_, err := g.runGitCommand("", "clone", "--depth", "1", "-b", g.Branch, g.RepoURL, g.RepoPath)
	if err != nil && g.Branch == "main" {
		g.Logger.Println("Branch 'main' not found, trying 'master'...")
		g.Branch = "master"
		_, err = g.runGitCommand("", "clone", "--depth", "1", "-b", g.Branch, g.RepoURL, g.RepoPath)
	}
	return err
}

// GetRepoInfo retrieves repository information
func (g *RepoDocGenerator) GetRepoInfo() (map[string]string, error) {
	branch, err := g.runGitCommand(g.RepoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, err
	}

	commitInfo, err := g.runGitCommand(g.RepoPath, "log", "-1", "--format=%H%n%an%n%ad%n%s")
	if err != nil {
		return nil, err
	}

	parts := strings.Split(commitInfo, "\n")
	if len(parts) < 4 {
		return nil, fmt.Errorf("unexpected git log output format")
	}

	return map[string]string{
		"branch":      branch,
		"commit_hash": parts[0][:8],
		"author":      parts[1],
		"date":        parts[2],
		"subject":     parts[3],
	}, nil
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
	repoInfo, err := g.GetRepoInfo()
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# Repository Documentation\n\n")
	b.WriteString("## Repository Context\n\n")
	b.WriteString(fmt.Sprintf("Repository URL: %s\n", g.RepoURL))
	b.WriteString(fmt.Sprintf("Branch: %s\n", repoInfo["branch"]))
	b.WriteString(fmt.Sprintf("Last Commit: %s\n", repoInfo["commit_hash"]))
	b.WriteString(fmt.Sprintf("Last Commit Date: %s\n", repoInfo["date"]))
	b.WriteString(fmt.Sprintf("Author: %s\n", repoInfo["author"]))
	b.WriteString(fmt.Sprintf("Subject: %s\n\n", repoInfo["subject"]))
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

func processRepository(repoURL, outputDir, branch string, verbose bool) error {
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
			urls = append(urls, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return urls, nil
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

	// Check if input is a file
	if info, err := os.Stat(input); err == nil && !info.IsDir() {
		log.Printf("Reading repository URLs from file: %s", input)
		urls, err := readRepoURLs(input)
		if err != nil {
			log.Fatalf("Failed to read repository URLs: %v", err)
		}

		// Process repositories concurrently with a worker pool
		var wg sync.WaitGroup
		semaphore := make(chan struct{}, 4) // Limit concurrent processing

		for _, url := range urls {
			wg.Add(1)
			go func(repoURL string) {
				defer wg.Done()
				semaphore <- struct{}{}        // Acquire
				defer func() { <-semaphore }() // Release

				log.Printf("Processing repository: %s", repoURL)
				if err := processRepository(repoURL, outputDir, branch, verbose); err != nil {
					log.Printf("Error processing repository %s: %v", repoURL, err)
				}
			}(url)
		}

		wg.Wait()
	} else {
		// Process single repository
		if err := processRepository(input, outputDir, branch, verbose); err != nil {
			log.Fatalf("Failed to process repository: %v", err)
		}
	}
}
