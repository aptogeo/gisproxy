package lib

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// The contextKey type is unexported to prevent collisions with context keys defined in
// other packages
type contextKey string

// GisProxyFromContext retrives GisProxy from context
func GisProxyFromContext(ctx context.Context) *GisProxy {
	v := ctx.Value(contextKey("GisProxy"))
	if v == nil {
		return nil
	}
	return v.(*GisProxy)
}

// GisInfoFromContext retrives GisInfo from context
func GisInfoFromContext(ctx context.Context) *GisInfo {
	v := ctx.Value(contextKey("GisInfo"))
	if v == nil {
		return nil
	}
	return v.(*GisInfo)
}

// StatusError struct
type StatusError struct {
	Message string
	Code    int
}

// NewStatusError constructs StatusError
func NewStatusError(message string, code int) *StatusError {
	return &StatusError{Message: message, Code: code}
}

// Error implements the error interface
func (e *StatusError) Error() string {
	return fmt.Sprintf("%v (%v)", e.Message, e.Code)
}

// BeforeSend defines before send callback function
type BeforeSend func(http.ResponseWriter, *http.Request) error

// AfterReceive defines after receive callback function
type AfterReceive func(http.ResponseWriter, *http.Response) error

var (
	reMapServer     = regexp.MustCompile("(?i)/services/(.+)/mapserver/?")
	reFeatureServer = regexp.MustCompile("(?i)/services/(.+)/featureserver/?")
	reImageServer   = regexp.MustCompile("(?i)/services/(.+)/imageserver/?")
)

// GisProxy structure
type GisProxy struct {
	server           *http.Server
	serverMux        *http.ServeMux
	client           *http.Client
	Prefix           string
	AllowCrossOrigin bool
	https            bool
	crtfile          string
	keyfile          string
	next             http.Handler
	beforeSendFunc   BeforeSend
	afterReceiveFunc AfterReceive
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
func NewGisProxy(listen string, prefix string, allowCrossOrigin bool) *GisProxy {
	gp := new(GisProxy)
	gp.serverMux = http.NewServeMux()
	gp.server = &http.Server{Addr: listen, Handler: gp.serverMux}
	gp.Prefix = prefix
	gp.AllowCrossOrigin = allowCrossOrigin
	gp.https = false
	// create http client
	gp.client = &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
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

// UseHttps uses Https with certificate
func (gp *GisProxy) UseHttps(crtfile string, keyfile string) {
	gp.https = true
	gp.crtfile = crtfile
	gp.keyfile = keyfile
}

func (rp *GisProxy) Start() error {
	log.Println("Start server")
	log.Println("Listen=", rp.server.Addr)
	log.Println("Prefix=", rp.Prefix)
	log.Println("AllowCrossOrigin=", rp.AllowCrossOrigin)
	log.Println("https=", rp.https)
	if rp.https {
		log.Println("crtfile=", rp.crtfile)
		log.Println("keyfile=", rp.keyfile)
	}
	rp.serverMux.HandleFunc("/", rp.serveHTTP)
	if rp.https {
		rp.server.ListenAndServeTLS(rp.crtfile, rp.keyfile)
	}
	return rp.server.ListenAndServe()
}

func (gp *GisProxy) Stop(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return gp.server.Shutdown(ctx)
}

// SetNextHandler sets next handler for middleware use
func (gp *GisProxy) SetNextHandler(next http.Handler) {
	gp.next = next
}

// SetBeforeSendFunc sets BeforeSend callback function
func (gp *GisProxy) SetBeforeSendFunc(beforeSendFunc BeforeSend) {
	gp.beforeSendFunc = beforeSendFunc
}

// SetAfterReceiveFunc sets AfterReceive callback function
func (gp *GisProxy) SetAfterReceiveFunc(afterReceiveFunc AfterReceive) {
	gp.afterReceiveFunc = afterReceiveFunc
}

// serveHTTP serves rest request
func (gp *GisProxy) serveHTTP(writer http.ResponseWriter, incomingRequest *http.Request) {
	if gp.Prefix == "" {
		gp.Prefix = "/"
	}
	if !strings.HasPrefix(gp.Prefix, "/") {
		gp.Prefix = "/" + gp.Prefix
	}
	if !strings.HasSuffix(gp.Prefix, "/") {
		gp.Prefix = gp.Prefix + "/"
	}
	if forwardUrl, err := gp.ComputeForwardUrl(incomingRequest); err != nil {
		if gp.next != nil {
			gp.next.ServeHTTP(writer, incomingRequest)
		} else {
			gp.writeError(writer, incomingRequest, err)
			return
		}
	} else {
		// Set GisProxy to context
		ctx := context.WithValue(incomingRequest.Context(), contextKey("GisProxy"), gp)
		// Set GisInfo to context
		ctx = context.WithValue(ctx, contextKey("GisInfo"), gp.extractInfo(incomingRequest, forwardUrl))
		response, err := gp.sendRequestWithContext(ctx, writer, incomingRequest.Method, forwardUrl, incomingRequest.Body, incomingRequest.Header)
		if response != nil && response.Body != nil {
			defer response.Body.Close()
		}
		if err != nil {
			gp.writeError(writer, incomingRequest, err)
			return
		}
		gp.writeResponse(writer, incomingRequest, response)
	}
}

// ComputeRewriteUrl computes forward url
func (gp *GisProxy) ComputeForwardUrl(incomingRequest *http.Request) (*url.URL, error) {
	incomingRequestURL := incomingRequest.URL.String()
	idx := strings.Index(incomingRequestURL, "://")
	if idx != -1 && idx < 10 {
		incomingRequestURL = incomingRequestURL[idx+3:]
	}
	re := regexp.MustCompile("(" + gp.Prefix + ")([^/\\?]+)([/\\?]?.*)?")
	submatch := re.FindStringSubmatch(incomingRequestURL)
	if submatch != nil && submatch[2] != "" {
		// replace '%2B' by '+', '%2F' by '/' and '%3D' by '='
		b64URL := strings.ReplaceAll(submatch[2], "%2B", "+")
		b64URL = strings.ReplaceAll(b64URL, "%2F", "/")
		b64URL = strings.ReplaceAll(b64URL, "%3D", "=")
		if decURL, err := base64.StdEncoding.DecodeString(b64URL); err != nil {
			return nil, err
		} else {
			if forwardUrl, err := url.Parse(string(decURL) + submatch[3]); err != nil {
				return nil, err
			} else {
				return forwardUrl, nil
			}
		}
	} else {
		return nil, errors.New("Prefix " + gp.Prefix + " not found in request")
	}
}

func (gp *GisProxy) extractInfo(request *http.Request, forwardUrl *url.URL) *GisInfo {
	serverURL := ""
	serverType := "unknown"
	serviceType := "unknown"
	serviceName := ""
	lowerURL := strings.ToLower(forwardUrl.String())
	path := forwardUrl.Path
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
	} else {
		if request.Method == "PUT" || request.Method == "POST" || request.Method == "PATCH" {
			if strings.Contains(strings.ToLower(request.Header.Get("Content-Type")), "application/x-www-form-urlencoded") ||
				strings.Contains(strings.ToLower(request.Header.Get("Content-Type")), "multipart/form-data") {
				if bodyByte, err := ioutil.ReadAll(request.Body); err == nil {
					request.Body = ioutil.NopCloser(bytes.NewBuffer(bodyByte))
					request.ParseMultipartForm(2 << 20)
					request.Body = ioutil.NopCloser(bytes.NewBuffer(bodyByte))
				}
			}
		}
		serverURL = strings.Split(lowerURL, "?")[0]
		for key, values := range request.Form {
			lowerKey := strings.ToLower(key)
			if lowerKey == "service" {
				if len(values) > 0 {
					serverType = strings.ToUpper(values[0])
					serviceType = serverType
				}
			}
			if serverType == "WMS" {
				if lowerKey == "layers" {
					serviceName = strings.Join(values, ",")
				} else if lowerKey == "query_layers" {
					serviceName = strings.Join(values, ",")
				}
			} else if serverType == "WMTS" {
				if lowerKey == "layer" {
					serviceName = strings.Join(values, ",")
				}
			} else if serverType == "WFS" {
				if lowerKey == "typenames" {
					serviceName = strings.Join(values, ",")
				} else if lowerKey == "typename" {
					serviceName = strings.Join(values, ",")
				}
			}
		}
	}
	return &GisInfo{ServerURL: serverURL, ServerType: serverType, ServiceType: serviceType, ServiceName: serviceName}
}

// SendRequestWithContext sends request with context
func (gp *GisProxy) sendRequestWithContext(ctx context.Context, writer http.ResponseWriter, method string, url *url.URL, body io.Reader, header http.Header) (*http.Response, error) {
	// Create request
	var request *http.Request
	var err error
	if method == "PUT" || method == "POST" || method == "PATCH" {
		request, err = http.NewRequestWithContext(ctx, method, url.String(), body)
	} else {
		request, err = http.NewRequestWithContext(ctx, method, url.String(), nil)
	}
	if err != nil {
		log.Println("New request error")
		return nil, err
	}
	// Add request header
	for h, vs := range header {
		for _, v := range vs {
			request.Header.Add(h, v)
		}
	}
	if gp.beforeSendFunc != nil {
		// Call before send function
		err := gp.beforeSendFunc(writer, request)
		if err != nil {
			statusError, valid := err.(*StatusError)
			if !valid || statusError.Code != 302 {
				log.Println("Before send error", err, request.URL)
			}
			return nil, err
		}
	}
	// Send
	return gp.client.Do(request)
}

// writeResponse writes response
func (gp *GisProxy) writeResponse(writer http.ResponseWriter, request *http.Request, response *http.Response) {
	if gp.afterReceiveFunc != nil {
		// Call after receive function
		if err := gp.afterReceiveFunc(writer, response); err != nil {
			statusError, valid := err.(*StatusError)
			if !valid || statusError.Code != 302 {
				log.Println("After receive error", err, request.URL)
			}
			gp.writeError(writer, request, err)
			return
		}
	}
	if response.StatusCode == 302 {
		location, _ := response.Location()
		gp.writeError(writer, request, NewStatusError(location.String(), 302))
		return
	}
	// Write header
	gp.writeResponseHeader(writer, request, response.Header)
	// Set status
	writer.WriteHeader(response.StatusCode)
	// Copy body
	if _, err := io.Copy(writer, response.Body); err != nil {
		log.Println("Copy response error")
		gp.writeError(writer, request, err)
	}
}

// writeResponse writes error
func (gp *GisProxy) writeError(writer http.ResponseWriter, request *http.Request, err error) {
	gp.writeResponseHeader(writer, request, nil)
	statusError, valid := err.(*StatusError)
	if valid {
		if statusError.Code == 200 {
			writer.Write([]byte(statusError.Message))
		} else if statusError.Code == 302 {
			writer.Header().Set("Location", statusError.Message)
			writer.WriteHeader(302)
		} else {
			log.Println("Error", http.StatusInternalServerError, err)
			http.Error(writer, err.Error(), statusError.Code)
		}
	} else {
		log.Println("Error", http.StatusInternalServerError, err)
		http.Error(writer, err.Error(), http.StatusInternalServerError)
	}
}

// writeResponseHeader writes response header
func (gp *GisProxy) writeResponseHeader(writer http.ResponseWriter, request *http.Request, header http.Header) {
	// Add response header
	for h, vs := range header {
		for _, v := range vs {
			writer.Header().Add(h, v)
		}
	}
	if gp.AllowCrossOrigin {
		// Allow access origin
		origin := request.Header.Get("Origin")
		if origin != "" {
			writer.Header().Set("Access-Control-Allow-Origin", origin)
			writer.Header().Set("Access-Control-Allow-Credentials", "true")
			writer.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, HEAD, TRACE, DELETE, PATCH, COPY, HEAD, LINK, OPTIONS")
		} else {
			writer.Header().Set("Access-Control-Allow-Origin", "*")
			writer.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, HEAD, TRACE, DELETE, PATCH, COPY, HEAD, LINK, OPTIONS")
		}
	}
}
