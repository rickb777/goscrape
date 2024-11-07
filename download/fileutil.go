package download

import (
	"net/url"
	"path/filepath"
)

const (
	// PageExtension is the file extension that downloaded pages get.
	PageExtension = ".html"
	// PageDirIndex is the file name of the index file for every dir.
	PageDirIndex = "index" + PageExtension
)

const externalDomainPrefix = "_" // _ is a prefix for external domains on the filesystem

// getFilePath returns a file path for a URL to store the URL content in.
func (d *Download) getFilePath(url *url.URL, isAPage bool) string {
	fileName := url.Path
	if isAPage {
		fileName = getPageFilePath(url)
	}

	var externalHost string
	if url.Host != d.StartURL.Host {
		externalHost = externalDomainPrefix + url.Host
	}

	return filepath.Join(d.Config.OutputDirectory, d.StartURL.Host, externalHost, fileName)
}

// getPageFilePath returns a filename for a URL that represents a page.
func getPageFilePath(url *url.URL) string {
	fileName := url.Path

	// root of domain will be index.html
	switch {
	case fileName == "" || fileName == "/":
		fileName = PageDirIndex
		// directory index will be index.html in the directory

	case fileName[len(fileName)-1] == '/':
		fileName += PageDirIndex

	default:
		ext := filepath.Ext(fileName)
		// if file extension is missing add .html, otherwise keep the existing file extension
		if ext == "" {
			fileName += PageExtension
		}
	}

	return fileName
}
