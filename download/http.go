package download

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/cornelk/goscrape/logger"
	"github.com/cornelk/gotokit/log"
)

const (
	initialRetryDelay = 5 * time.Second
	bigRetryDelay     = 30 * time.Second
)

var DownloadURL = func(ctx context.Context, d *Download, u *url.URL) (resp *http.Response, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}

	if d.Config.UserAgent != "" {
		req.Header.Set("User-Agent", d.Config.UserAgent)
	}

	if d.Auth != "" {
		req.Header.Set("Authorization", d.Auth)
	}

	for key, values := range d.Config.Header {
		for _, value := range values {
			req.Header.Set(key, value)
		}
	}

	retryDelay := initialRetryDelay // used every retry

	// this loop provides retries if 5xx server errors arise
	for i := 0; i < d.Config.Tries; i++ {
		resp, err = d.Client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("sending HTTP request: %w", err)
		}

		switch {
		// 1xx status codes are never returned
		// 3xx redirect status code - handled by http.Client (up to 10 redirections)

		// 5xx status code = server error - retry the specified number of times
		case resp.StatusCode >= 500:
			retryDelay = backoff5xx(retryDelay)
			// retry logic continues below

		case resp.StatusCode == http.StatusTooManyRequests:
			retryDelay = backoff429(retryDelay)
			// retry logic continues below

		// 4xx status code = client error
		case resp.StatusCode >= 400:
			logger.Error("HTTP client error", log.String("url", u.String()),
				log.Int("code", resp.StatusCode), log.String("status", http.StatusText(resp.StatusCode)))
			return nil, nil

		// 304 not modified - no further action
		case resp.StatusCode == http.StatusNotModified:
			return nil, nil

		// 2xx status code = success
		case 200 <= resp.StatusCode && resp.StatusCode < 300:
			logger.Debug(http.MethodGet,
				log.String("url", u.String()),
				log.Int("status", resp.StatusCode),
				log.String("Content-Type", resp.Header.Get("Content-Type")),
				log.String("Content-Length", resp.Header.Get("Content-Length")),
				log.String("Last-Modified", resp.Header.Get("Last-Modified")))
			return resp, nil

		default:
			return nil, fmt.Errorf("unexpected HTTP response %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
		}

		logger.Warn("HTTP server error",
			log.String("url", req.URL.String()),
			log.Int("code", resp.StatusCode),
			log.String("status", http.StatusText(resp.StatusCode)),
			log.String("sleep", retryDelay.String()))

		time.Sleep(retryDelay)
	}

	if resp == nil {
		return nil, fmt.Errorf("%s response status unknown", u)
	}
	return nil, fmt.Errorf("%s response status %d %s", resp.Request.URL, resp.StatusCode, http.StatusText(resp.StatusCode))
}

func backoff5xx(t time.Duration) time.Duration {
	const factor = 7
	const divisor = 4 // must be less than factor

	if t < initialRetryDelay {
		return initialRetryDelay
	}

	return time.Duration(t*factor) / divisor
}

func backoff429(t time.Duration) time.Duration {
	const factor = 9
	const divisor = 8 // must be less than factor

	if t < bigRetryDelay {
		return bigRetryDelay
	}

	return time.Duration(t*factor) / divisor
}

func closeResponseBody(resp *http.Response) {
	if err := resp.Body.Close(); err != nil {
		logger.Error("Closing HTTP response body failed",
			log.String("url", resp.Request.URL.String()),
			log.Err(err))
	}
}

func bufferEntireResponse(resp *http.Response) ([]byte, error) {
	buf := &bytes.Buffer{}
	if _, err := io.Copy(buf, resp.Body); err != nil {
		return nil, fmt.Errorf("%s reading response body: %w", resp.Request.URL, err)
	}
	return buf.Bytes(), nil
}
