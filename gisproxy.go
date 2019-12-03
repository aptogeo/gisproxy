package gisproxy

import (
	"crypto/tls"
	"encoding/base64"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
)

// GisProxy structure
type GisProxy struct {
	prefix string
	client *http.Client
	next   http.Handler
}

// NewGisProxy constructs GisProxy
func NewGisProxy(prefix string) *GisProxy {
	gp := new(GisProxy)
	gp.SetPrefix(prefix)
	// create http client
	gp.client = &http.Client{}
	gp.client.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return gp
}

// SetPrefix sets prefix
func (gp *GisProxy) SetPrefix(prefix string) {
	gp.prefix = prefix
	if gp.prefix == "" {
		gp.prefix = "/"
	}
	if !strings.HasPrefix(gp.prefix, "/") {
		gp.prefix = "/" + gp.prefix
	}
	if !strings.HasSuffix(gp.prefix, "/") {
		gp.prefix = gp.prefix + "/"
	}
}

// SetNextHandler sets next handler for middleware use
func (gp *GisProxy) SetNextHandler(next http.Handler) {
	gp.next = next
}

// ServeHTTP serves rest request
func (gp *GisProxy) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	requestURL := request.URL.String()
	idx := strings.Index(requestURL, "://")
	if idx != -1 {
		requestURL = requestURL[idx+3:]
	}
	re := regexp.MustCompile("(" + gp.prefix + ")([^/\\?]+)([/\\?]?.*)?")
	res := re.FindStringSubmatch(requestURL)
	if res != nil && res[2] != "" {
		// replace '%2B' by '+', '%2F' by '/' and '%3D' by '='
		b64URL := strings.ReplaceAll(res[2], "%2B", "+")
		b64URL = strings.ReplaceAll(b64URL, "%2F", "/")
		b64URL = strings.ReplaceAll(b64URL, "%3D", "=")
		decURL, err := base64.StdEncoding.DecodeString(b64URL)
		if err != nil {
			http.Error(writer, "Base64 decoding error for "+b64URL, http.StatusInternalServerError)
			return
		}
		url := string(decURL) + res[3]
		err = gp.sendRequest(writer, request.Method, url, request.Body, request.Header)
		if err != nil {
			http.Error(writer, "Requesting server "+url+" error", http.StatusInternalServerError)
			return
		}
	} else {
		if gp.next != nil {
			gp.next.ServeHTTP(writer, request)
		} else {
			http.Error(writer, "Request isn't GIS Proxy request", http.StatusInternalServerError)
			return
		}
	}
}

func (gp *GisProxy) sendRequest(writer http.ResponseWriter, method string, url string, body io.Reader, header http.Header) error {
	// Create request
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return err
	}
	// Add request header
	for n, h := range header {
		for _, h := range h {
			req.Header.Add(n, h)
		}
	}
	log.Println("method:", method, " | url:", url, " | body", body)
	// Send
	res, err := gp.client.Do(req)
	if err != nil {
		return err
	}
	// Add response header
	for h, v := range res.Header {
		for _, v := range v {
			writer.Header().Add(h, v)
		}
	}
	// Allow access origin
	origin := header.Get("Origin")
	if origin == "" {
		origin = "*"
	}
	writer.Header().Set("Access-Control-Allow-Origin", origin)
	writer.Header().Set("Access-Control-Allow-Credentials", "true")
	writer.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, HEAD, TRACE, DELETE, PATCH, COPY, HEAD, LINK, OPTIONS")
	// Set status
	writer.WriteHeader(res.StatusCode)
	// Copy body
	_, err = io.Copy(writer, res.Body)
	if err != nil {
		return err
	}
	return nil
}
