package download

import (
	"github.com/cornelk/goscrape/logger"
	"io"
	"log/slog"
	"net/url"
	"os"
	"testing"

	"github.com/cornelk/goscrape/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetFilePath(t *testing.T) {
	type filePathFixture struct {
		BaseURL          string
		DownloadURL      string
		ExpectedFilePath string
	}

	pathSeparator := string(os.PathSeparator)
	expectedBasePath := "google.com" + pathSeparator
	var fixtures = []filePathFixture{
		{"https://google.com/", "https://github.com/", expectedBasePath + "_github.com" + pathSeparator + "index.html"},
		{"https://google.com/", "https://github.com/#fragment", expectedBasePath + "_github.com" + pathSeparator + "index.html"},
		{"https://google.com/", "https://github.com/test", expectedBasePath + "_github.com" + pathSeparator + "test.html"},
		{"https://google.com/", "https://github.com/test/", expectedBasePath + "_github.com" + pathSeparator + "test" + pathSeparator + "index.html"},
		{"https://google.com/", "https://github.com/test.aspx", expectedBasePath + "_github.com" + pathSeparator + "test.aspx"},
		{"https://google.com/", "https://google.com/settings", expectedBasePath + "settings.html"},
	}

	var cfg config.Config
	logger.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, fix := range fixtures {
		cfg.URL = fix.BaseURL
		u, _ := url.Parse(cfg.URL)
		s := Download{
			Config:   cfg,
			StartURL: u,
		}

		URL, err := url.Parse(fix.DownloadURL)
		require.NoError(t, err)

		output := s.getFilePath(URL, true)
		assert.Equal(t, fix.ExpectedFilePath, output)
	}
}
