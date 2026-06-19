package thunderstore

import "time"

// Package is the top-level Thunderstore package index entry.
type Package struct {
	Name           string    `json:"name"`
	FullName       string    `json:"full_name"`
	Owner          string    `json:"owner"`
	PackageURL     string    `json:"package_url"`
	DateUpdated    time.Time `json:"date_updated"`
	IsDeprecated   bool      `json:"is_deprecated"`
	HasNSFWContent bool      `json:"has_nsfw_content"`
	Categories     []string  `json:"categories"`
	Versions       []Version `json:"versions"`
}

// Version is a concrete published Thunderstore package version.
type Version struct {
	Name          string    `json:"name"`
	FullName      string    `json:"full_name"`
	Description   string    `json:"description"`
	Icon          string    `json:"icon"`
	VersionNumber string    `json:"version_number"`
	Dependencies  []string  `json:"dependencies"`
	DownloadURL   string    `json:"download_url"`
	Downloads     int       `json:"downloads"`
	DateCreated   time.Time `json:"date_created"`
	WebsiteURL    string    `json:"website_url"`
	IsActive      bool      `json:"is_active"`
	FileSize      int64     `json:"file_size"`
}

// DependencyRef is a parsed Thunderstore dependency string in the form
// Namespace-Name-Version.
type DependencyRef struct {
	Namespace string
	Name      string
	Version   string
}

// PackageKey returns Namespace-Name.
func (d DependencyRef) PackageKey() string {
	return d.Namespace + "-" + d.Name
}
