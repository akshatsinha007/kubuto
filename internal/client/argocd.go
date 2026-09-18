package client

import (
	"strings"
)

// IsGitHub checks if a URL is a GitHub repository.
func IsGitHub(url string) bool {
	return strings.Contains(url, "github.com")
}

// IsGitLab checks if a URL is a GitLab repository.
func IsGitLab(url string) bool {
	return strings.Contains(url, "gitlab.com") || strings.Contains(url, "gitlab.")
}

// ExtractGitHubOwnerRepo extracts owner/repo from a GitHub URL.
func ExtractGitHubOwnerRepo(url string) string {
	url = strings.TrimSuffix(url, ".git")
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimPrefix(url, "git@")
	url = strings.ReplaceAll(url, ":", "/")

	parts := strings.Split(url, "/")
	if len(parts) >= 3 && parts[0] == "github.com" {
		return parts[1] + "/" + parts[2]
	}
	return ""
}

// ExtractGitLabProjectPath extracts project path from a GitLab URL.
func ExtractGitLabProjectPath(url string) string {
	url = strings.TrimSuffix(url, ".git")
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimPrefix(url, "git@")
	url = strings.ReplaceAll(url, ":", "/")

	parts := strings.Split(url, "/")
	if len(parts) >= 3 && (parts[0] == "gitlab.com" || strings.Contains(parts[0], "gitlab.")) {
		return strings.Join(parts[1:], "/")
	}
	return ""
}

// NormalizeRepoURL removes common suffixes and trailing slashes.
func NormalizeRepoURL(url string) string {
	url = strings.TrimSuffix(url, ".git")
	url = strings.TrimSuffix(url, "/")
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimPrefix(url, "git@")
	url = strings.ReplaceAll(url, ":", "/")
	return url
}

// ReposMatch checks if two repo URLs refer to the same repository.
func ReposMatch(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return NormalizeRepoURL(a) == NormalizeRepoURL(b)
}
