package mapping

import (
	"io"
	"log/slog"
	urlpkg "net/url"
	"os"
	"testing"

	"github.com/cornelk/goscrape/logger"
	"github.com/stretchr/testify/assert"
)

func TestGetFilePath(t *testing.T) {
	type filePathCase struct {
		downloadURL      string
		expectedFilePath string
	}

	pathSeparator := string(os.PathSeparator)

	var cases = []filePathCase{
		{downloadURL: "https://github.com/", expectedFilePath: "./index.html"},
		{downloadURL: "https://github.com/#fragment", expectedFilePath: "./index.html"},
		{downloadURL: "https://github.com/test", expectedFilePath: "./test.html"},
		{downloadURL: "https://github.com/test/", expectedFilePath: "./test" + pathSeparator + "index.html"},
		{downloadURL: "https://github.com/test.aspx", expectedFilePath: "./test.aspx"},
		{downloadURL: "https://google.com/settings", expectedFilePath: "./settings.html"},
	}

	logger.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))

	for _, c := range cases {
		url := must(c.downloadURL)

		output := GetFilePath(url, true)
		assert.Equal(t, c.expectedFilePath, output)
	}
}

func must(s string) *urlpkg.URL {
	u, e := urlpkg.Parse(s)
	if e != nil {
		panic(e)
	}
	return u
}
