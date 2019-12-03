package gisproxy

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	var listen string
	var prefix string
	flag.StringVar(&listen, "listen", "localhost:8181", "host:port to listen on")
	flag.StringVar(&prefix, "prefix", "/", "prefix path")
	log.Println("Listen:", listen, "Prefix:", prefix)
	gisProxy := NewGisProxy(prefix)
	http.HandleFunc(prefix, gisProxy.ServeHTTP)
	http.ListenAndServe(listen, nil)
}
