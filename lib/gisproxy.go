package lib

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// StatusError struct
type StatusError struct {
	Cause error
	Code  int
}

// NewStatusError constructs StatusError
func NewStatusError(cause error, code int) *StatusError {
	return &StatusError{Cause: cause, Code: code}
}

// Error implements the error interface
func (e StatusError) Error() string {
	return e.Cause.Error()
}

// BeforeSend defines before send callback function
type BeforeSend func(*GisInfo, *http.Request) (*http.Request, error)

var (
	reForm          = regexp.MustCompile("(?i)[+-/]form($|[+-;])")
	reMapServer     = regexp.MustCompile("(?i)/services/(.+)/mapserver[/$]")
	reFeatureServer = regexp.MustCompile("(?i)/services/(.+)/featureserver[/$]")
	reImageServer   = regexp.MustCompile("(?i)/services/(.+)/imageserver[/$]")
	reOWSType       = regexp.MustCompile("(?i)&?service=([^&]+)")
	reOWSName       = regexp.MustCompile("(?i)&?layers?=([^&]+)")
)

// GisProxy structure
type GisProxy struct {
	prefix         string
	client         *http.Client
	next           http.Handler
	beforeSendFunc BeforeSend
}

// GisInfo structure
type GisInfo struct {
	ServerURL   string
	ServerType  string
	ServiceType string
	ServiceName string
}

func (gi *GisInfo) String() string {
	return fmt.Sprintf("GisInfo ServerURL=%v ServerType=%v ServiceType=%v ServiceName=%v", gi.ServerURL, gi.ServerType, gi.ServiceType, gi.ServiceName)
}

// NewGisProxy constructs GisProxy
func NewGisProxy(prefix string) *GisProxy {
	gp := new(GisProxy)
	gp.SetPrefix(prefix)
	// create http client
	gp.client = &http.Client{}
	gp.client.Transport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
			DualStack: true,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
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

// SetBeforeSendFunc sets BeforeSend callback function
func (gp *GisProxy) SetBeforeSendFunc(beforeSendFunc BeforeSend) {
	gp.beforeSendFunc = beforeSendFunc
}

// ServeHTTP serves rest request
func (gp *GisProxy) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	requestURL := request.URL.String()
	idx := strings.Index(requestURL, "://")
	if idx != -1 {
		requestURL = requestURL[idx+3:]
	}
	if request.Method == "POST" && reForm.MatchString(request.Header.Get("Content-type")) {
		bodyBytes, err := ioutil.ReadAll(request.Body)
		if err == nil {
			requestURL += "?" + string(bodyBytes)
		}
	}
	log.Println(requestURL)
	re := regexp.MustCompile("(" + gp.prefix + ")([^/\\?]+)([/\\?]?.*)?")
	submatch := re.FindStringSubmatch(requestURL)
	if submatch != nil && submatch[2] != "" {
		// replace '%2B' by '+', '%2F' by '/' and '%3D' by '='
		b64URL := strings.ReplaceAll(submatch[2], "%2B", "+")
		b64URL = strings.ReplaceAll(b64URL, "%2F", "/")
		b64URL = strings.ReplaceAll(b64URL, "%3D", "=")
		decURL, err := base64.StdEncoding.DecodeString(b64URL)
		if err != nil {
			http.Error(writer, "Base64 decoding error for "+b64URL, http.StatusInternalServerError)
			return
		}
		url := string(decURL) + submatch[3]
		res, err := gp.SendRequestWithContext(request.Context(), request.Method, url, request.Body, request.Header)
		if err != nil {
			statusError, valid := err.(*StatusError)
			if valid {
				http.Error(writer, "Requesting server "+url+" error: "+err.Error(), statusError.Code)
			} else {
				http.Error(writer, "Requesting server "+url+" error: "+err.Error(), http.StatusInternalServerError)
			}
			return
		}
		gp.write(writer, res)
		if err != nil {
			http.Error(writer, "Writing response error", http.StatusInternalServerError)
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

func (gp *GisProxy) extractInfo(req *http.Request) *GisInfo {
	serverURL := ""
	serverType := "unknown"
	serviceType := "unknown"
	serviceName := ""
	lowerURL := strings.ToLower(req.URL.String())
	path := req.URL.Path
	rawQuery := req.URL.RawQuery
	if res := reMapServer.FindStringSubmatch(path); res != nil {
		serverURL = strings.Split(lowerURL, "/rest/services/")[0] + "/rest/services/"
		serverType = "ArcGIS"
		serviceType = "MapServer"
		serviceName = res[1]
	} else if res := reFeatureServer.FindStringSubmatch(path); res != nil {
		serverURL = strings.Split(lowerURL, "/rest/services/")[0] + "/rest/services/"
		serverType = "ArcGIS"
		serviceType = "FeatureServer"
		serviceName = res[1]
	} else if res := reImageServer.FindStringSubmatch(path); res != nil {
		serverURL = strings.Split(lowerURL, "/rest/services/")[0] + "/rest/services/"
		serverType = "ArcGIS"
		serviceType = "ImageServer"
		serviceName = res[1]
	} else if res1 := reOWSType.FindStringSubmatch(rawQuery); res1 != nil {
		serverURL = strings.Split(lowerURL, "?")[0]
		serverType = strings.ToUpper(res1[1])
		serviceType = serverType
		if res2 := reOWSName.FindStringSubmatch(rawQuery); res2 != nil {
			serviceName = res2[1]
		}
	}
	return &GisInfo{ServerURL: serverURL, ServerType: serverType, ServiceType: serviceType, ServiceName: serviceName}
}

// SendRequest sends request
func (gp *GisProxy) SendRequest(method string, url string, body io.Reader, header http.Header) (*http.Response, error) {
	return gp.SendRequestWithContext(context.Background(), method, url, body, header)
}

// SendRequestWithContext sends request with context
func (gp *GisProxy) SendRequestWithContext(ctx context.Context, method string, url string, body io.Reader, header http.Header) (*http.Response, error) {
	// Create request
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	// Add request header
	for n, h := range header {
		for _, h := range h {
			req.Header.Add(n, h)
		}
	}
	if gp.beforeSendFunc != nil {
		// Extract info
		gisInfo := gp.extractInfo(req)
		// Call before send function
		req, err = gp.beforeSendFunc(gisInfo, req)
		if err != nil {
			return nil, err
		}
	}
	// Send
	return gp.client.Do(req)
}

func (gp *GisProxy) write(writer http.ResponseWriter, res *http.Response) error {
	// Add response header
	for h, v := range res.Header {
		for _, v := range v {
			writer.Header().Add(h, v)
		}
	}
	// Allow access origin
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Access-Control-Allow-Credentials", "true")
	writer.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, HEAD, TRACE, DELETE, PATCH, COPY, HEAD, LINK, OPTIONS")
	// Set status
	writer.WriteHeader(res.StatusCode)
	// Copy body
	_, err := io.Copy(writer, res.Body)
	if err != nil {
		return err
	}
	return nil
}
