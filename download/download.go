package download

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"time"

	"github.com/cornelk/goscrape/config"
	"github.com/cornelk/goscrape/download/ioutil"
	"github.com/cornelk/goscrape/htmlindex"
	"github.com/cornelk/goscrape/logger"
	"github.com/cornelk/goscrape/work"
	"github.com/cornelk/gotokit/log"
	"github.com/rickb777/acceptable/header"
	"github.com/rickb777/acceptable/headername"
	"github.com/spf13/afero"
	"golang.org/x/net/html"
)

type HttpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Download fetches URLs one by one, sequentially.
type Download struct {
	Config   config.Config
	Cookies  *cookiejar.Jar
	StartURL *url.URL

	Auth   string
	Client HttpClient

	Fs       afero.Fs // filesystem can be replaced with in-memory filesystem for testing
	Throttle Throttle // increases when server gives 429 (Too Many Requests) responses
}

func (d *Download) ProcessURL(ctx context.Context, item work.Item) (*url.URL, *work.Result, error) {
	var existingModified time.Time

	filePath := d.getFilePath(item.URL, true)
	if ioutil.FileExists(d.Fs, filePath) {
		fileInfo, err := os.Stat(filePath)
		if err == nil && fileInfo != nil {
			existingModified = fileInfo.ModTime()
		}
	}

	startTime := time.Now()

	resp, err := d.GET(ctx, item.URL, existingModified)
	if err != nil {
		logger.Error("Processing HTTP Request failed",
			log.String("url", item.URL.String()),
			log.Err(err))
		return nil, nil, err
	}

	if resp == nil {
		return nil, nil, nil //response was 304-not modified
	}

	defer closeResponseBody(resp)
	defer logResponse(item.URL, resp, startTime)

	if item.Depth == 0 {
		// take account of redirection (only on the start page)
		item.URL = resp.Request.URL
	}

	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		// put this URL back into the work queue to be re-tried later
		repeat := &work.Result{Item: item, References: []*url.URL{item.URL}}
		repeat.Item.Depth-- // because it gets incremented
		return item.URL, repeat, nil

	case http.StatusNoContent:
		// put this URL back into the work queue to be re-tried later
		return nil, nil, nil

	case http.StatusNotModified:
		return d.response304(item, resp)

	default:
		return d.response200(item, resp)
	}
}

//-------------------------------------------------------------------------------------------------

func (d *Download) response200(item work.Item, resp *http.Response) (*url.URL, *work.Result, error) {
	contentType := header.ParseContentTypeFromHeaders(resp.Header)
	lastModified, _ := header.ParseHTTPDateTime(resp.Header.Get(headername.LastModified))

	switch {
	case isHtml(contentType) || isXHtml(contentType):
		return d.html200(item, resp, lastModified, contentType)

	case contentType.Type == "text" && contentType.Subtype == "css":
		return d.css200(item, resp, lastModified)

	case contentType.Type == "image" && d.Config.ImageQuality != 0:
		return d.image200(item, resp, lastModified, contentType)

	default:
		// store without buffering entire file into memory
		d.storeDownload(item.URL, resp.Body, lastModified, false)
	}

	return nil, &work.Result{Item: item}, nil
}

//-------------------------------------------------------------------------------------------------

func (d *Download) html200(item work.Item, resp *http.Response, lastModified time.Time, contentType header.ContentType) (*url.URL, *work.Result, error) {
	var references htmlindex.Refs

	data, err := bufferEntireResponse(resp)
	if err != nil {
		return nil, nil, fmt.Errorf("buffering %s: %w", contentType.String(), err)
	}

	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", contentType.String(), err)
	}

	index := htmlindex.New()
	index.Index(item.URL, doc)

	fixed, hasChanges, err := d.fixURLReferences(item.URL, doc, index)
	if err != nil {
		logger.Error("Fixing file references failed",
			log.String("url", item.String()),
			log.Err(err))
		return nil, nil, nil
	}

	var rdr io.Reader
	if hasChanges {
		rdr = bytes.NewReader(fixed)
	} else {
		rdr = bytes.NewReader(data)
	}

	d.storeDownload(item.URL, rdr, lastModified, true)

	references, err = d.findReferences(item, index)
	if err != nil {
		return nil, nil, err
	}

	// use the URL that the website returned as new base url for the
	// scrape, in case a redirect changed it (only for the start page)
	return resp.Request.URL, &work.Result{Item: item, References: references}, nil
}

//-------------------------------------------------------------------------------------------------

func (d *Download) css200(item work.Item, resp *http.Response, lastModified time.Time) (*url.URL, *work.Result, error) {
	var references htmlindex.Refs

	data, err := bufferEntireResponse(resp)
	if err != nil {
		return nil, nil, fmt.Errorf("buffering text/css: %w", err)
	}

	data, references = d.checkCSSForUrls(item.URL, data)

	d.storeDownload(item.URL, bytes.NewReader(data), lastModified, false)

	return nil, &work.Result{Item: item, References: references}, nil
}

//-------------------------------------------------------------------------------------------------

func (d *Download) image200(item work.Item, resp *http.Response, lastModified time.Time, contentType header.ContentType) (*url.URL, *work.Result, error) {
	data, err := bufferEntireResponse(resp)
	if err != nil {
		return nil, nil, fmt.Errorf("buffering %s: %w", contentType.String(), err)
	}

	data = d.Config.ImageQuality.CheckImageForRecode(item.URL, data)
	if d.Config.ImageQuality != 0 {
		lastModified = time.Time{} // altered images can't be safely time-stamped
	}

	d.storeDownload(item.URL, bytes.NewReader(data), lastModified, false)

	return nil, &work.Result{Item: item}, nil
}

//-------------------------------------------------------------------------------------------------

func (d *Download) response304(item work.Item, resp *http.Response) (*url.URL, *work.Result, error) {
	contentType := header.ParseContentTypeFromHeaders(resp.Header)

	switch {
	case isHtml(contentType) || isXHtml(contentType):
		return d.html304(item, resp, contentType)

	case contentType.Type == "text" && contentType.Subtype == "css":
		return d.css304(item, contentType)

	default:
		// no further action
	}

	// use the URL that the website returned as new base url for the
	// scrape, in case a redirect changed it (only for the start page)
	return resp.Request.URL, &work.Result{Item: item}, nil
}

//-------------------------------------------------------------------------------------------------

func (d *Download) html304(item work.Item, resp *http.Response, contentType header.ContentType) (*url.URL, *work.Result, error) {
	var references htmlindex.Refs
	filePath := d.getFilePath(item.URL, true)
	data, err := ioutil.ReadFile(d.Fs, d.StartURL, filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("existing file %s: %w", contentType.String(), err)
	}

	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", contentType.String(), err)
	}

	index := htmlindex.New()
	index.Index(item.URL, doc)

	references, err = d.findReferences(item, index)
	if err != nil {
		return nil, nil, err
	}

	// use the URL that the website returned as new base url for the
	// scrape, in case a redirect changed it (only for the start page)
	return resp.Request.URL, &work.Result{Item: item, References: references}, nil
}

//-------------------------------------------------------------------------------------------------

func (d *Download) css304(item work.Item, contentType header.ContentType) (*url.URL, *work.Result, error) {
	var references htmlindex.Refs
	filePath := d.getFilePath(item.URL, false)
	data, err := ioutil.ReadFile(d.Fs, d.StartURL, filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("existing file %s: %w", contentType.String(), err)
	}

	_, references = d.checkCSSForUrls(item.URL, data)

	return nil, &work.Result{Item: item, References: references}, nil
}

//-------------------------------------------------------------------------------------------------

var tagsWithReferences = []string{
	htmlindex.ATag,
	htmlindex.LinkTag,
	htmlindex.ScriptTag,
	htmlindex.BodyTag,
}

func (d *Download) findReferences(item work.Item, index *htmlindex.Index) (htmlindex.Refs, error) {
	var result htmlindex.Refs
	for _, tag := range tagsWithReferences {
		references, err := index.URLs(tag)
		if err != nil {
			logger.Error("Getting node URLs failed",
				log.String("url", item.String()),
				log.String("node", tag),
				log.Err(err))
		}

		for _, ur := range references {
			ur.Fragment = ""
			result = append(result, ur)
		}
	}

	references, err := index.URLs(htmlindex.ImgTag)
	if err != nil {
		logger.Error("Getting <img> URLs failed", log.String("url", item.String()), log.Err(err))
	}

	for _, ur := range references {
		ur.Fragment = ""
		result = append(result, ur)
	}

	return result, nil
}

// storeDownload writes the download to a file, if a known binary file is detected,
// processing of the file as page to look for links is skipped.
func (d *Download) storeDownload(u *url.URL, data io.Reader, lastModified time.Time, isAPage bool) {
	filePath := d.getFilePath(u, isAPage)

	if !isAPage && ioutil.FileExists(d.Fs, filePath) {
		return
	}

	if err := ioutil.WriteFileAtomically(d.Fs, d.StartURL, filePath, data); err != nil {
		logger.Error("Writing to file failed",
			log.String("URL", u.String()),
			log.String("file", filePath),
			log.Err(err))
		return
	}

	if !lastModified.IsZero() {
		if err := os.Chtimes(filePath, lastModified, lastModified); err != nil {
			logger.Error("Updating file timestamps failed",
				log.String("URL", u.String()),
				log.String("file", filePath),
				log.Err(err))
		}
	}
}

func isHtml(contentType header.ContentType) bool {
	return contentType.Type == "text" && contentType.Subtype == "html"
}

func isXHtml(contentType header.ContentType) bool {
	return contentType.Type == "application" && contentType.Subtype == "xhtml+xml"
}

func timeTaken(before time.Time) string {
	return time.Now().Sub(before).Round(time.Millisecond).String()
}

func logResponse(item *url.URL, resp *http.Response, before time.Time) {
	level := log.InfoLevel
	switch {
	case resp.StatusCode >= 400:
		level = log.WarnLevel
	}
	logger.Log(level, http.StatusText(resp.StatusCode), log.String("url", item.String()), log.Int("code", resp.StatusCode), log.String("took", timeTaken(before)))
}
