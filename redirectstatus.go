package redirectstatus

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/turingvideo/traefik-plugin-redirect-status/traefik/pkg/types"
)

// Config the plugin configuration.
type Config struct {
	// Status defines which status or range of statuses should result in an error page.
	// It can be either a status code as a number (500),
	// as multiple comma-separated numbers (500,502),
	// as ranges by separating two codes with a dash (500-599),
	// or a combination of the two (404,418,500-599).
	Status []string `json:"status,omitempty" toml:"status,omitempty" yaml:"status,omitempty" export:"true"`
	// To defines the URL for redirecting target.
	// The {status} variable can be used in order to insert the status code in the URL.
	To string `json:"query,omitempty" toml:"query,omitempty" yaml:"query,omitempty" export:"true"`
}

// CreateConfig creates the default plugin configuration.
func CreateConfig() *Config {
	return &Config{}
}

// redirectStatus is a middleware that provides redirection on status.
type redirectStatus struct {
	name           string
	next           http.Handler
	httpCodeRanges types.HTTPCodeRanges
	to             string
}

// New created a new plugin.
func New(ctx context.Context, next http.Handler, config *Config, name string) (http.Handler, error) {
	httpCodeRanges, err := types.NewHTTPCodeRanges(config.Status)
	if err != nil {
		return nil, err
	}

	return &redirectStatus{
		name:           name,
		next:           next,
		httpCodeRanges: httpCodeRanges,
		to:             config.To,
	}, nil
}

func (r *redirectStatus) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	catcher := newCodeCatcher(rw, r.httpCodeRanges)
	r.next.ServeHTTP(catcher, req)
	if !catcher.isFilteredCode() {
		return
	}

	// check the recorder code against the configured http status code ranges
	code := catcher.getCode()

	var to string
	if len(r.to) > 0 {
		to = r.to
		to = strings.ReplaceAll(to, "{status}", strconv.Itoa(code))
		to = strings.ReplaceAll(to, "{url}", url.QueryEscape(req.URL.String()))
	}

	rw.Header().Set("Location", to)

	status := http.StatusFound
	if req.Method != http.MethodGet {
		status = http.StatusTemporaryRedirect
	}

	rw.WriteHeader(status)
	_, err := rw.Write([]byte(http.StatusText(status)))
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
	}
}

type responseInterceptor interface {
	http.ResponseWriter
	http.Flusher
	getCode() int
	isFilteredCode() bool
}

// codeCatcher is a response writer that detects as soon as possible whether the
// response is a code within the ranges of codes it watches for. If it is, it
// simply drops the data from the response. Otherwise, it forwards it directly to
// the original client (its responseWriter) without any buffering.
type codeCatcher struct {
	headerMap          http.Header
	code               int
	httpCodeRanges     types.HTTPCodeRanges
	caughtFilteredCode bool
	responseWriter     http.ResponseWriter
	headersSent        bool
}

type codeCatcherWithCloseNotify struct {
	*codeCatcher
}

// CloseNotify returns a channel that receives at most a
// single value (true) when the client connection has gone away.
func (cc *codeCatcherWithCloseNotify) CloseNotify() <-chan bool {
	return cc.responseWriter.(http.CloseNotifier).CloseNotify()
}

func newCodeCatcher(rw http.ResponseWriter, httpCodeRanges types.HTTPCodeRanges) responseInterceptor {
	catcher := &codeCatcher{
		headerMap:      make(http.Header),
		code:           http.StatusOK, // If backend does not call WriteHeader on us, we consider it's a 200.
		responseWriter: rw,
		httpCodeRanges: httpCodeRanges,
	}
	if _, ok := rw.(http.CloseNotifier); ok {
		return &codeCatcherWithCloseNotify{catcher}
	}
	return catcher
}

func (cc *codeCatcher) Header() http.Header {
	if cc.headerMap == nil {
		cc.headerMap = make(http.Header)
	}

	return cc.headerMap
}

func (cc *codeCatcher) getCode() int {
	return cc.code
}

// isFilteredCode returns whether the codeCatcher received a response code among the ones it is watching,
// and for which the response should be deferred to the error handler.
func (cc *codeCatcher) isFilteredCode() bool {
	return cc.caughtFilteredCode
}

func (cc *codeCatcher) Write(buf []byte) (int, error) {
	// If WriteHeader was already called from the caller, this is a NOOP.
	// Otherwise, cc.code is actually a 200 here.
	cc.WriteHeader(cc.code)

	if cc.caughtFilteredCode {
		// We don't care about the contents of the response,
		// since we want to serve the ones from the error page,
		// so we just drop them.
		return len(buf), nil
	}
	return cc.responseWriter.Write(buf)
}

func (cc *codeCatcher) WriteHeader(code int) {
	if cc.headersSent || cc.caughtFilteredCode {
		return
	}

	cc.code = code
	for _, block := range cc.httpCodeRanges {
		if cc.code >= block[0] && cc.code <= block[1] {
			cc.caughtFilteredCode = true
			// it will be up to the caller to send the headers,
			// so it is out of our hands now.
			return
		}
	}

	copyHeaders(cc.responseWriter.Header(), cc.Header())
	cc.responseWriter.WriteHeader(cc.code)
	cc.headersSent = true
}

// Hijack hijacks the connection.
func (cc *codeCatcher) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := cc.responseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("%T is not a http.Hijacker", cc.responseWriter)
}

// Flush sends any buffered data to the client.
func (cc *codeCatcher) Flush() {
	// If WriteHeader was already called from the caller, this is a NOOP.
	// Otherwise, cc.code is actually a 200 here.
	cc.WriteHeader(cc.code)

	if flusher, ok := cc.responseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// copyHeaders copies http headers from source to destination, it
// does not override, but adds multiple headers.
func copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		dst[k] = append(dst[k], vv...)
	}
}
